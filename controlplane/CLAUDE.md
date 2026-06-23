# Control Plane Package

The clawker control plane. A containerized, privileged, long-lived Go service that owns authoritative state for managed containers. The `clawker-controlplane` container runs `cmd/clawkercp` as PID 1, owns the firewall stack (Envoy + CoreDNS) and eBPF state, and serves the `AdminService` gRPC surface consumed by the CLI via `f.AdminClient(ctx)`.

## Naming — "domain" is data-layer design talk, never a runtime label

"Domain" (DDD bounded context) is a way to *think* about the data-layer design — how a
piece of state owns and projects its own data. It is NOT a thing in the running system,
NOT a name for a package, and NOT a vehicle you start, wire, or call.

The implementation names its sub-components for what they are: a **Store**, a **Storage
Repository** — components that live inside a package. There is no "domain handler", no
"agent domain", no "future domains embed alongside". There is the agent package with its
Store; the firewall handler; concrete RPC handlers.

- It is the **agent package**, not "the agent domain". Packages are packages.
- Wire **concrete things by their real names**: the dialer (`agent.New`), the watcher
  (`NewAgentWatcher`), the init executor (`NewExecutor`), the registry subscriptions
  (`agent.Start`). No `*Domain` symbol names. No `startAgentDomain`-style blobs — a
  package is not a startable vehicle.
- The CP wires those concrete constructors inline; it is not "an orchestrator calling
  domain wrappers". Don't oversell DDD/"orchestrator" terminology.
- `run()` reads top-to-bottom through concrete calls. No numbered `// Phase N` comment
  scaffolding — a function that needs a comment table-of-contents is a god-function.

## Resilience contract — CP crashing is a security incident, NOT an availability one

This is the most important invariant in this package. Read it before adding any failure path to CP code. See the root `CLAUDE.md` for the canonical statement; this section enforces it for code under `internal/controlplane/`.

### Why CP must not crash

A panic, `log.Fatal`, `os.Exit`, or unrecovered goroutine in CP code:

1. **Kills PID 1.** CP container exits non-zero. `on-failure` restart policy retries `consts.CPMaxRestartRetries` (3) times; deterministic bugs replay each time, then CP stays dead.
2. **Skips the clean drain-to-zero path.** `firewall.Stack.Stop()` and `ebpfMgr.FlushAll()` are only called by the `AgentWatcher`'s drain callback in `internal/controlplane/cmd.go`. A panic bypasses both.
3. **Leaves eBPF programs pinned and unsupervised.** Programs are attached to cgroups and survive in `/sys/fs/bpf`. The kernel keeps filtering agent egress against whatever rules were loaded at the moment of death.
4. **Strands the stack trace on `os.Stderr` → `docker logs <cp>`.** It is NOT in the rotating `ControlPlaneLogFile`; it is NOT visible via `clawker controlplane status` (which only reports up/down).
5. **Leaves agent containers running with no supervisor.** clawkerd has no awareness CP died; agents keep serving workloads.

### What this looks like to the user

- They see agents running. They assume the firewall is enforcing (it technically is, against frozen rules) and that CP is observing and ready to dispatch containment (it isn't).
- `clawker firewall add <domain>` writes to the rules file but Envoy/CoreDNS reload requires CP — silently drops.
- A `clawker firewall bypass <duration>` in flight when CP died has no expiry timer — the bypass is now permanent until manual intervention.
- No CP→clawkerd Session means no observation of agent behavior, no command dispatch, no containment available even if a compromise is detected.
- Agents are exposed to prompt injection, exfiltration, and lateral-movement attempts CP would otherwise see and contain. The user's mental model — "CP has my agents covered" — is silently false.

### Hard rules

1. **No `panic()`. No `log.Fatal()`. No `os.Exit()`** in any code reachable from `internal/controlplane/cmd.go` after `SetReady`. The only acceptable hard exits are:
   - Pre-`SetReady` startup failures (exit code 1). Any error returned from `run()` before `SetReady` is such an exit — ordinary wiring failures (Ory health, eBPF load, gRPC listen) and deliberate startup gates (state/policy checks elevated to fail startup, e.g. `CleanupStaleBypass` (INV-B2-013) and the settings-driven `firewall.enable` stack bringup) alike. These exit WITHOUT flushing eBPF, so agents enrolled by a previous CP stay fail-closed rather than fail-open.
   - The orchestrator's intentional drain-to-zero clean exit (code 0).
2. **Constructors return `(*T, error)`.** Pattern: `agent.New`, `agent.NewExecutor`. Nil deps, cert load failures, schema errors → return error, never panic. `run()` in `internal/controlplane/cmd.go` logs structurally and degrades the subsystem (`dialer = nil`, `initExec = nil`).
3. **Long-lived goroutines must recover.** Wrap heartbeats, watchers, dispatch handlers, RPC interceptors with `defer func() { if r := recover(); r != nil { log.Error().Interface("panic", r)... } }()`. The agent-watcher goroutine in `run()` (`internal/controlplane/cmd.go`, `event=agent_watcher_panic`) — which converts a panic into a terminal shutdown error so drain-to-zero / eBPF flush still runs — is the template. One bad event must not silently strand eBPF.
4. **Subsystem failures degrade.** Broken Executor → `initExec = nil` → dialer logs `agent_init_executor_unset` per dial → entrypoint fifo timeout is the user-visible failure. CP itself, firewall, registry, AdminService unaffected. Broken dialer → `dialer = nil` → CP→clawkerd dispatch disabled; everything else stays up. Copy `wireInitExecutor` (initExec; emits `event=agent_init_executor_unavailable`) and the `agent.New(...)` block that degrades on error to `event=agent_dialer_unavailable` in `internal/controlplane/cmd.go` as templates for new subsystems.
5. **Every degraded path emits a structured log line** (`event=<subsystem>_unavailable`) with enough fields for an operator to triage root cause AND blast radius. They will never see panic stacks; the structured log is the only surface.
6. **Treat any urge to panic as a security review trigger.** Ask: "would this leave eBPF programs pinned with no supervisor?" If yes — you are about to silently break the firewall enforcement boundary the user trusts. Return an error.

### Existing escape hatches you'll find in code (and must not add to)

- The `register_panic` recover in `clawkerd/session.go::handleRegisterRequired` (NOT in CP — clawkerd-side, agent container can crash).
- The agent-watcher goroutine recover in `run()` (`internal/controlplane/cmd.go`, `event=agent_watcher_panic`) — keep doing this for new long-lived goroutines.
- `firewall/handler.go` returns `status.Error(...)` for handler-level failures; never panics.

### What about `consts.CPMaxRestartRetries`?

It's a safety net, not a recovery strategy. By the time it triggers, eBPF has been pinned with no supervisor for at least the time it took to crash → restart → crash 3× → backoff. The restart policy exists for transient hardware/scheduler hiccups, not for software bugs we should have caught.

## Responsibilities

1. **Authoritative eBPF management** — the CP owns `ebpf.Manager.Load()` lifetime for its process lifetime. BPF programs are loaded once at boot and stay live. The manager exposes read-only accessors for the netlogger pipeline: `EventsRingbuf()`, `EventsDrops()`, `RatelimitDrops()`, `DNSCache()` (all nil before `Load`; callers MUST nil-check).
2. **AdminService gRPC surface** — the host CLI calls firewall/eBPF operations as typed gRPC over mTLS TCP with OAuth2 JWT authorization.
3. **Ory auth stack** — Hydra (OAuth2), Oathkeeper (reverse proxy), Kratos (identity, placeholder for webui).
4. **Aggregate health reporting** — `/healthz` actively probes all 7 service ports before returning 200.
5. **Per-decision eBPF egress event emission (netlogger)** — drains BPF `events_ringbuf`, enriches by `cgroup_id` via the typed eBPF-enrolled pub/sub topic (`enrolledTopic`, subscribed by netlogger), ships OTLP log records (`service.name=ebpf-egress`) to the trusted-infra OTLP receiver via the `otel.NewOtelLoggerProvider` factory (`controlplane/otel`). Degraded paths emit `event=netlogger_unavailable` and leave firewall enforcement untouched.

## Auth (Hydra introspection + mTLS + JWT)

The auth stack uses Ory Hydra as the OAuth2 provider (replaces the earlier custom OIDC provider):

| Layer | Purpose | Implementation |
|-------|---------|----------------|
| mTLS over TCP | Authenticate the channel; server cert + CLI client cert both signed by CLI CA | Server: `RequireAndVerifyClientCert` + `ClientCAs` from bind-mounted CA. Client: `LoadClientCert()` in `controlplane/adminclient/dial.go` |
| Hydra OAuth2 | Issue JWTs via `client_credentials` + `private_key_jwt` (ES256) | Hydra subprocess with in-memory DSN |
| gRPC AuthInterceptor | Validate bearer tokens via Hydra introspection (RFC 7662); enforce per-method scopes | `controlplane/auth/authz.go` — `HydraIntrospector` calls POST `/admin/oauth2/introspect` |

**CLI auth flow**: CLI presents mTLS client cert (signed by CLI CA) during TLS handshake → server verifies against CA → CLI signs a JWT assertion with its ES256 private key → POST to Hydra `/oauth2/token` (plain TLS, separate config) with `client_credentials` grant + `client_assertion` → Hydra validates signature against registered JWKS → returns access token (JWT) → CLI sends bearer token on gRPC calls → CP's AuthInterceptor introspects token via Hydra admin API → grants/denies based on scope.

**Two TLS configs on CLI side** (`controlplane/adminclient/dial.go`): `tokenTLSCfg` (plain TLS for Hydra token endpoint) and `grpcTLSCfg` (mTLS with client cert for AdminService). This pattern scales to future agent clients that will have their own CA-signed certs.

**Failure mode**: Fail-closed. Any error (network, introspection failure, unmapped method) returns `codes.Unauthenticated`.

## Subpackages

`controlplane/` has no root `.go` files — it is purely the parent of the CP's bounded-context subpackages. Each row is a package directory under `controlplane/`.

| Subpackage | Purpose |
|------------|---------|
| `pubsub/` | Generic, dumb in-memory pub/sub pipe — `Topic[T]`/`Event[T]` (the typed bus), `NewStatsHeartbeat`. Zero imports of any CP sibling; recover-per-delivery so a panicking subscriber can't strand eBPF. |
| `dockerevents/` | Docker-event bounded context: `feeder.go` (sole `DockerEvent` producer), dispatch/reconcile of `purpose=agent` container lifecycle onto the typed topic. |
| `agent/` | Agent bounded context — sqlite registry, in-memory worldview repository, CP→clawkerd dialer (`agent.New`), `NewAgentWatcher`, `NewExecutor`, `IdentityInterceptor`. See `controlplane/agent/CLAUDE.md`. |
| `server/` | gRPC composition: `NewAdminServer(fw, agents, log) (adminv1.AdminServiceServer, error)` (`server.go`) + `NewGRPCStack(GRPCDeps) (*GRPCStack, error)` (`grpc_stack.go`) — builds both listeners (admin + agent), wires interceptors, registers services. |
| `auth/` | Ory auth stack: `AuthInterceptor`/`HydraIntrospector` (`authz.go`), `RegisterCLIClient`/`RegisterAgentClient` (`hydra_client.go`), `WriteOryConfigs` (`ory_configs.go`), Ory subprocess bringup (`ory_stack.go`). Mocks in `auth/mocks/`. |
| `subprocess/` | `SubprocessManager` + `NewSubprocessManager` — Ory subprocess lifecycle (start, health, crash detection, reverse-order shutdown). |
| `otel/` | `NewOtelLoggerProvider(OtelClientOptions) (*sdklog.LoggerProvider, error)` (`otelclient.go`) — generic per-subsystem OTel log-provider factory pushing OTLP/gRPC over mTLS to the trusted-infra receiver. |
| `firewall/` | Envoy + CoreDNS + eBPF egress enforcement; `firewall.Handler` (the 13 firewall RPCs), `firewall.Stack`, Envoy/CoreDNS config generation, and the `ebpf/` subtree (loader + netlogger). See `controlplane/firewall/CLAUDE.md`. |
| `manager/` | **Host-side CP lifecycle.** `EnsureRunning`/`Stop`/`CPRunning` (`bootstrap.go`), `BuildCPContainerConfig` (`cp_container.go`), `Manager` interface + `NewManager` (`manager.go`), the `//go:embed` of `clawkercp` + `ebpf-manager` (`embed_cp.go`/`embed_ebpf.go`). Replaces the former `cpboot/`. See `controlplane/manager/CLAUDE.md`. |
| `adminclient/` | CLI-side AdminService dialer (`dial.go`): `Dial`, `ProbeCPTime`, `LoadClientCert`, the two TLS configs (token-endpoint plain TLS vs gRPC mTLS), token source. |
| `infracerts/` | Trusted-infra (OTLP/monitoring) mTLS cert material. See `controlplane/infracerts/CLAUDE.md`. |
| `otelcerts/` | OTel client cert provisioning. See `controlplane/otelcerts/CLAUDE.md`. |

## AdminService composition

`controlplane/server/server.go` exposes the unexported `adminServer` type that embeds `*firewall.Handler` (and, in future branches, additional RPC handlers). Method promotion produces the AdminServiceServer surface. `server.NewAdminServer(fw, agents, log) (adminv1.AdminServiceServer, error)` is the composition constructor — it returns an error (e.g. `ErrNilRegistry`) rather than panicking, per the CP no-crash contract. It is composed into the gRPC stack by `server.NewGRPCStack` (`controlplane/server/grpc_stack.go`), which `buildGRPCStack` in `internal/controlplane/cmd.go` calls to build and serve both listeners.

The 13 firewall RPCs live in `controlplane/firewall/handler.go` — see `controlplane/firewall/CLAUDE.md` for the per-RPC table. Future handlers (Monitor, Hostproxy, Clawkerd) embed alongside; the `<Subsystem><Action>[<Object>]` proto naming convention prevents method-name collisions.

All RPCs require the uniform `admin` scope (INV-B2-009) with one deliberate exception: `GetSystemTime` is mapped to the public scope (`consts.ScopePublic`) in `AdminMethodScopes()`, making it PUBLIC so the CLI can call it during token-exchange bootstrap before it holds a bearer token (the mTLS client cert is still required at the listener). An empty or unmapped scope fails closed (deny) — public is the explicit `ScopePublic` sentinel, never the zero value. Per-method scope diversification beyond this is intentionally not used — see Spec §8.

## Startup Sequence (`run()` in `internal/controlplane/cmd.go`)

`run()` reads top-to-bottom through these concrete helper calls (one line each — see each helper's body for detail):

1. `bootLogging` — trusted-lane OTel certs + the file/OTEL logger (degraded `os.Stderr` fallback when `New` fails).
2. Config + signal-aware contexts — `config.NewConfig`, `signalCtx` (SIGTERM/SIGINT), `watcherCtx` (long-lived workers), `subprocess.NewSubprocessManager`, `NewControlPlane`.
3. `startOryStack` — writes Ory configs, starts Kratos + Hydra + Oathkeeper subprocesses, waits healthy, configures service probes, registers the CLI + agent Hydra clients; returns the single CA pool + CA TLS surface (startup gate).
4. `buildEnforcement` — Docker client + `firewall.Stack` + rules store + `ebpfMgr.Load()` + `CleanupStaleBypass` (INV-B2-013); returns the joined cleanup (startup gates, pre-`SetReady`).
5. `buildTopics` — the typed pub/sub topics (`dockerTopic`, `agentTopic`, `enrolledTopic`); one topic per payload type, the generic audit hook self-attaches in `NewTopic`.
6. `buildAgentInfra` — agent sqlite registry + `MobyPeerLookup` + `ContainerLister` + the in-memory `agent.Repository` (worldview) with its agent-event and docker-event subscriptions wired.
7. `buildGRPCStack` — firewall `ActionQueue` + `fwhandler.Handler` (holds publish-only `enrolledTopic`) + the admin (`cp.AdminPort`, mTLS + CLI-scope AuthInterceptor) and agent (`cp.AgentPort`, clawker-net only, agent-scope AuthInterceptor chained ahead of `agent.IdentityInterceptor`) gRPC listeners; starts serving. The admin surface hosts the 13 firewall RPCs + `ListAgents` + the lone public-scope `GetSystemTime`. `IdentityInterceptor` runs a universal three-stage gate (CN pin to `consts.ContainerClawkerd` → peer-IP→`purpose=agent` container resolution reading `dev.clawker.{project,agent}` labels → constant-time `AgentFullName` vs `urn:clawker:agent:` URI SAN compare). CP→clawkerd dispatch is the OUTBOUND dialer (step 13), not this listener — see `internal/controlplane/agent/CLAUDE.md` and the asymmetric-trust clarification in the root `CLAUDE.md`.
8. `firewallBringupGate` — when `firewall.enable` (settings.yaml) is true, runs `FirewallInit` synchronously BEFORE `SetReady` so a green `/healthz` means "everything the settings enable is enforcing". A failure FAILS startup (pre-`SetReady` exit 1, same doctrine as `CleanupStaleBypass`; logged `event=firewall_bringup_failed`, bounded by `consts.FirewallStackBringupTimeout`, does NOT flush eBPF so enrolled agents stay fail-closed). Caveat: re-enrollment events published by this gate precede netlogger construction (step 12), so netlogger's label cache stays cold for agents that outlived the previous CP until the next FirewallInit/FirewallEnable — telemetry enrichment only, enforcement unaffected.
9. `orchestrator.SetReady()` — the ready gate flips; everything below is post-`SetReady`.
10. `startHealthz` — serves aggregate `/healthz` on `HealthPort`.
11. `startFeeder` — the `dockerevents` feeder, sole producer of `DockerEvent` onto its typed topic.
12. `startWorkers` — the long-lived observability workers: the `pubsub.NewStatsHeartbeat`, the `netlogger.Service` (subscribes `enrolledTopic` to hydrate its label cache; degrades to `netloggerSvc=nil` with `event=netlogger_unavailable` on any chain failure), and the `dns_cache` GC goroutine (`event=dns_gc_*`, escalates `dns_gc_degraded` after `dnsGCDegradedThreshold` consecutive reclaim-failures). All run on `watcherCtx`.
13. Agent watcher + `startAgentDialer` — `agent.NewAgentWatcher` (drain-to-zero trigger; its goroutine recovers panics into a terminal shutdown error, `event=agent_watcher_panic`) plus the init executor, CP→clawkerd dialer, and agent-axis subscriptions (§3.4 degrade contract).
14. Serve + drain — the select waits on signal / drain-to-zero / subprocess crash / serve failure, then runs the drain callback (`actionQueue.Close()` → `grpcStack.GracefulStop()` → `handler.CancelAllBypassTimers()` → `firewall.Stack.Stop()` → `netloggerSvc.Stop` → `stopDNSGC()` → `ebpfMgr.FlushAll()`, INV-B2-007) exactly once (sync.Once), then tears the container down at exit code 0 (the `on-failure` restart policy does NOT retrigger).

## Aggregate Health (`internal/controlplane/cmd.go`)

The `ControlPlane` orchestrator type (`NewControlPlane`, `SetReady`, `HealthzHandler`, `SetServiceProbes`) manages the `/healthz` endpoint:

- **Before ready**: returns 503
- **After ready**: actively probes all 7 service ports on every request:
  - Hydra public (TLS), Hydra admin (TLS)
  - Kratos public (TLS), Kratos admin (TLS)
  - Oathkeeper proxy (TLS), Oathkeeper API (TLS)
  - gRPC admin (raw TCP)
- Returns 200 only when ALL probes succeed

## Container Config (`controlplane/manager/cp_container.go`)

```go
func BuildCPContainerConfig(cfg config.Config, opts CPContainerOpts) (*CPContainerConfig, error)
```

All ports from `cfg.Settings().ControlPlane` (defaults via struct tags). Published to `127.0.0.1` only:

| Published Port | Purpose |
|----------------|---------|
| AdminPort (7443) | gRPC AdminService |
| HydraPublicPort (4444) | OAuth2 token endpoint |
| OathkeeperPort (4456) | HTTP reverse proxy (future webui) |
| HealthPort (7080) | /healthz endpoint |

**Not published**: Hydra admin (4445), Kratos ports, Oathkeeper API — internal-only (`127.0.0.1` bind inside container).

**Key mounts**: config dir (RO), CA cert (RO), CLI public JWK (RO), server TLS cert+key (RO), `FirewallDataSubdir` → `/var/lib/clawker/firewall` (RW — egress rules, Envoy/CoreDNS configs, MITM CA), `/sys/fs/cgroup` (RO), `/sys/fs/bpf` (RW), logs dir.

**Restart policy**: `on-failure` with `MaximumRetryCount=3`. A clean drain-to-zero exit (code 0 from `AgentWatcher`) does NOT retrigger the policy.

**Invariant INV-B1-006**: CLI private signing key is NEVER mounted into the container.

**Capabilities**: `BPF`, `SYS_ADMIN` (for eBPF program attachment).

## eBPF Subsystem

The eBPF subsystem lives at `firewall/ebpf/` — see `firewall/ebpf/CLAUDE.md` for full reference. Key surface consumed by CP core:

- `ebpf.Manager` — concrete loader. `Load()` runs once at CP startup; `CleanupStaleBypass` runs before `SetReady` (INV-B2-013); `FlushAll` runs during drain-to-zero (INV-B2-007).
- `ebpf.EBPFManager` interface — consumed by `firewall.Handler`. Methods: `Install`, `Remove`, `Enable`, `Disable`, `SyncRoutes`, `FlushAll`.
- `ebpf.Route` + `ebpf.BPFContainerConfig` + `ebpf.DomainHash` — shared types / hash function used by `internal/dnsbpf` and `controlplane/firewall` (`normalizeDomain`).
- `ebpf.Manager.EventsRingbuf()` / `EventsDrops()` / `RatelimitDrops()` / `DNSCache()` — read-only map accessors consumed by `netlogger.Service` for the per-decision egress event pipeline. All return nil before `Load()` — callers MUST nil-check.

## Ory Config Generation (`controlplane/auth/ory_configs.go`)

```go
func WriteOryConfigs(cp config.ControlPlaneSettings, hydraSecret string) error
```

Generates `/etc/clawker/{hydra,kratos,oathkeeper}.yaml`:

- **Hydra**: in-memory DSN, JWT access tokens, admin at `127.0.0.1:4445` (internal-only), public at `0.0.0.0:4444`, 1h access token TTL
- **Kratos**: in-memory DSN, `127.0.0.1:4480` (placeholder for future webui identity)
- **Oathkeeper**: HTTP reverse proxy at `0.0.0.0:4455` (placeholder for future webui auth), API at `127.0.0.1:4456`

## Subprocess Management (`controlplane/subprocess/subprocess.go`)

```go
type SubprocessManager struct { ... }
func (sm *SubprocessManager) Start(name string, cmd *exec.Cmd) error
func (sm *SubprocessManager) WaitHealthy(ctx context.Context, name string, check HealthCheck) error
func (sm *SubprocessManager) CrashChan() <-chan error
func (sm *SubprocessManager) Shutdown(timeout time.Duration)
```

Manages Ory service lifecycle. Crash reporting via channel. Shutdown sends SIGTERM then SIGKILL, reverse start order.

## Test seam overview

- `EBPFManager` interface — `controlplane/firewall/ebpf/mocks/EBPFManagerMock` for firewall handler tests.
- `Introspector` interface — `controlplane/auth/mocks/IntrospectorMock` for authz tests (no real Hydra).
- `manager.Manager` interface — `controlplane/manager/mocks/ManagerMock` for break-glass `controlplane up/down/status` CLI tests.
- `adminv1.AdminServiceClient` — `api/admin/v1/mocks.AdminServiceClientMock` for CLI tests that speak to the AdminService.
- `firewall.ContainerResolver` — handler-side injectable Docker lookup (see `controlplane/firewall/CLAUDE.md`).
- `agent.Registry` — moq-generated `RegistryMock` (in `controlplane/agent/mocks/registry_mock.go`) for `IdentityInterceptor`, `ListAgents`, and the dialer-side classification tests that need a deterministic snapshot independent of dockerevents wiring.

## Test coverage

| File | Invariants | What |
|------|------------|------|
| `controlplane/auth/authz_test.go` | INV-B1-011 | Token validation, scope enforcement, unmapped method denial, Hydra introspection mock |
| `controlplane/auth/grpc_mtls_test.go` | — | mTLS connection acceptance (valid cert), rejection (no cert), rejection (no TLS) |
| `controlplane/manager/container_config_test.go` | INV-B1-005, 006, 008, 009, 015, 017, 018, 020 | Port bindings, mounts, labels, private key exclusion (signing + client), config-driven ports |
| `internal/controlplane/cmd_test.go` | INV-B1-010, 013 | `IsReady()`/`SetReady()` atomic gate; /healthz 503→200 transition across the single ready boundary |
| `controlplane/firewall/ebpf/manager_test.go` | — | Link cleanup, map schema detection, Install/Remove/Enable/Disable, SyncRoutes, DNS cache GC |
| `controlplane/subprocess/subprocess_test.go` | — | Start/WaitHealthy, crash detection, SIGTERM/SIGKILL shutdown |
| `controlplane/manager/ebpf_regression_test.go` | — | eBPF package regressions (no kernel required) |

## Package imports

The CP daemon core is the `internal/controlplane` orchestrator plus these `controlplane/*` subpackages.

**Uses**: `internal/config`, `internal/consts`, `internal/docker`, `internal/logger`, `controlplane/firewall`, `controlplane/firewall/ebpf`, `api/admin/v1`, `google.golang.org/grpc`, `github.com/cilium/ebpf`, `github.com/moby/moby/api/types/{mount,network}`, `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/log`, `go.opentelemetry.io/otel/sdk/log`, `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc` (the last four via `controlplane/otel`'s `NewOtelLoggerProvider`).

**Used by**: `cmd/clawkercp/` (daemon entrypoint → `internal/controlplane`), `internal/cmd/controlplane/` (break-glass up/down/status → `controlplane/manager`), `internal/cmd/factory/` (`controlplane/adminclient` + ControlPlane Factory closures), `internal/cmd/firewall/` (AdminService consumers via `f.AdminClient`; `controlplane/manager` for stack up/down/status), `internal/cmd/container/*` (BootstrapServicesPostStart → `controlplane/manager`), `controlplane/firewall/ebpf/netlogger` (consumes `controlplane/otel`'s `NewOtelLoggerProvider`), `internal/dnsbpf` (reuses ebpf types), `internal/auth` (cert paths).

No circular dependencies.

## What's deferred

- **Kratos active usage** — running as subprocess placeholder. Lights up with webui.
- **Oathkeeper active routing** — running with empty rules. Lights up with webui HTTP auth.
- **Per-method scopes beyond `admin`** — finer-grained scopes (`webui:read`, etc.) would add entries to `AdminMethodScopes()` in `api/admin/v1/admin.go` (typed `adminv1.AdminScope`). INV-B2-009 mandates a uniform `admin` scope across all AdminService methods except the public bootstrap RPC `GetSystemTime` (`consts.ScopePublic`). The agent listener's scope vocabulary lives in `AgentMethodScopes()` in `api/agent/v1/agent.go` (typed `agentv1.AgentScope`) and currently holds the single `Register` → `ScopeSelfRegister` entry.

## Known limitations (deferred to cp-restart-resilience)

- **CP restart resilience.** When the CP restarts, clawkerd's inbound `:7700` listener stays up and the dialer re-establishes the Session once the new CP boots and reaches the container. The agent registry sqlite DB persists across CP restarts so identity is preserved without re-bootstrapping.
