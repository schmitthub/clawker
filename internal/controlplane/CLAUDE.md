# Control Plane Package

The clawker control plane. A containerized, privileged, long-lived Go service that owns authoritative state for managed containers. The `clawker-controlplane` container runs `cmd/clawker-cp` as PID 1, owns the firewall stack (Envoy + CoreDNS) and eBPF state, and serves the `AdminService` gRPC surface consumed by the CLI via `f.AdminClient(ctx)`.

## Resilience contract — CP crashing is a security incident, NOT an availability one

This is the most important invariant in this package. Read it before adding any failure path to CP code. See the root `CLAUDE.md` for the canonical statement; this section enforces it for code under `internal/controlplane/`.

### Why CP must not crash

A panic, `log.Fatal`, `os.Exit`, or unrecovered goroutine in CP code:

1. **Kills PID 1.** CP container exits non-zero. `on-failure` restart policy retries `consts.CPMaxRestartRetries` (3) times; deterministic bugs replay each time, then CP stays dead.
2. **Skips the clean drain-to-zero path.** `firewall.Stack.Stop()` and `ebpfMgr.FlushAll()` are only called by the `AgentWatcher`'s drain callback in `cmd/clawker-cp/main.go`. A panic bypasses both.
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

1. **No `panic()`. No `log.Fatal()`. No `os.Exit()`** in any code reachable from `cmd/clawker-cp/main.go` after `SetReady`. The only acceptable hard exits are:
   - Pre-`SetReady` orchestrator startup-gate failures (exit code 1). These exit WITHOUT flushing eBPF, so agents enrolled by a previous CP stay fail-closed rather than fail-open. Current gates: `CleanupStaleBypass` (INV-B2-013) and the `firewall.enable` stack bringup (step 8c).
   - The orchestrator's intentional drain-to-zero clean exit (code 0).
2. **Constructors return `(*T, error)`.** Pattern: `agent.New`, `agent.NewExecutor`. Nil deps, cert load failures, schema errors → return error, never panic. main.go logs structurally and degrades the subsystem (`dialer = nil`, `initExec = nil`).
3. **Long-lived goroutines must recover.** Wrap heartbeats, watchers, dispatch handlers, RPC interceptors with `defer func() { if r := recover(); r != nil { log.Error().Interface("panic", r)... } }()`. The overseer stats heartbeat goroutine in `cmd/clawker-cp/main.go` (search "overseer stats heartbeat") is the template. One bad event must not silently strand eBPF.
4. **Subsystem failures degrade.** Broken Executor → `initExec = nil` → dialer logs `agent_init_executor_unset` per dial → entrypoint fifo timeout is the user-visible failure. CP itself, firewall, registry, AdminService unaffected. Broken dialer → `dialer = nil` → CP→clawkerd dispatch disabled; everything else stays up. Copy `wireInitExecutor` (initExec; emits `event=agent_init_executor_unavailable`) and the `agent.New(...)` block that degrades on error to `event=agent_dialer_unavailable` in `cmd/clawker-cp/main.go` as templates for new subsystems.
5. **Every degraded path emits a structured log line** (`event=<subsystem>_unavailable`) with enough fields for an operator to triage root cause AND blast radius. They will never see panic stacks; the structured log is the only surface.
6. **Treat any urge to panic as a security review trigger.** Ask: "would this leave eBPF programs pinned with no supervisor?" If yes — you are about to silently break the firewall enforcement boundary the user trusts. Return an error.

### Existing escape hatches you'll find in code (and must not add to)

- The `register_panic` recover in `cmd/clawkerd/session.go::handleRegisterRequired` (NOT in CP — clawkerd-side, agent container can crash).
- The overseer stats heartbeat recover in `cmd/clawker-cp/main.go` (search "overseer stats heartbeat") — keep doing this for new long-lived goroutines.
- `firewall/handler.go` returns `status.Error(...)` for handler-level failures; never panics.

### What about `consts.CPMaxRestartRetries`?

It's a safety net, not a recovery strategy. By the time it triggers, eBPF has been pinned with no supervisor for at least the time it took to crash → restart → crash 3× → backoff. The restart policy exists for transient hardware/scheduler hiccups, not for software bugs we should have caught.

## Responsibilities

1. **Authoritative eBPF management** — the CP owns `ebpf.Manager.Load()` lifetime for its process lifetime. BPF programs are loaded once at boot and stay live. The manager exposes read-only accessors for the netlogger pipeline: `EventsRingbuf()`, `EventsDrops()`, `RatelimitDrops()`, `DNSCache()` (all nil before `Load`; callers MUST nil-check).
2. **AdminService gRPC surface** — the host CLI calls firewall/eBPF operations as typed gRPC over mTLS TCP with OAuth2 JWT authorization.
3. **Ory auth stack** — Hydra (OAuth2), Oathkeeper (reverse proxy), Kratos (identity, placeholder for webui).
4. **Aggregate health reporting** — `/healthz` actively probes all 7 service ports before returning 200.
5. **Per-decision eBPF egress event emission (netlogger)** — drains BPF `events_ringbuf`, enriches by `cgroup_id` via overseer enrollment events, ships OTLP log records (`service.name=ebpf-egress`) to the trusted-infra OTLP receiver via the `controlplane.NewOtelLoggerProvider` factory. Degraded paths emit `event=netlogger_unavailable` and leave firewall enforcement untouched.

## Auth (Hydra introspection + mTLS + JWT)

The auth stack uses Ory Hydra as the OAuth2 provider (replaces the earlier custom OIDC provider):

| Layer | Purpose | Implementation |
|-------|---------|----------------|
| mTLS over TCP | Authenticate the channel; server cert + CLI client cert both signed by CLI CA | Server: `RequireAndVerifyClientCert` + `ClientCAs` from bind-mounted CA. Client: `LoadClientCert()` in `cp_dial.go` |
| Hydra OAuth2 | Issue JWTs via `client_credentials` + `private_key_jwt` (ES256) | Hydra subprocess with in-memory DSN |
| gRPC AuthInterceptor | Validate bearer tokens via Hydra introspection (RFC 7662); enforce per-method scopes | `authz.go` — `HydraIntrospector` calls POST `/admin/oauth2/introspect` |

**CLI auth flow**: CLI presents mTLS client cert (signed by CLI CA) during TLS handshake → server verifies against CA → CLI signs a JWT assertion with its ES256 private key → POST to Hydra `/oauth2/token` (plain TLS, separate config) with `client_credentials` grant + `client_assertion` → Hydra validates signature against registered JWKS → returns access token (JWT) → CLI sends bearer token on gRPC calls → CP's AuthInterceptor introspects token via Hydra admin API → grants/denies based on scope.

**Two TLS configs on CLI side** (`cp_dial.go`): `tokenTLSCfg` (plain TLS for Hydra token endpoint) and `grpcTLSCfg` (mTLS with client cert for AdminService). This pattern scales to future agent clients that will have their own CA-signed certs.

**Failure mode**: Fail-closed. Any error (network, introspection failure, unmapped method) returns `codes.Unauthenticated`.

## Files

| File | Purpose |
|------|---------|
| `server.go` | `adminServer` composition + `NewAdminServer(fw, agents, log) adminv1.AdminServiceServer` — embeds the firewall handler so AdminService method promotion is satisfied for `cmd/clawker-cp` to register on its gRPC server. Holds `agents` for `ListAgents` (nil-tolerant; empty result). |
| `authz.go` | `AuthInterceptor` — validates OAuth2 bearer tokens via Hydra introspection, enforces per-method scopes |
| `hydra_client.go` | `RegisterCLIClient` + `RegisterAgentClient` — register the `clawker-cli` and `clawker-agent` OAuth2 clients with Hydra at startup via the shared `registerHydraClient` helper. Both clients use the same JWK (the CLI's signing key); only `client_id` and `scope` differ — the granted scope is the per-service distinct type (`adminv1.ScopeAdmin` / `agentv1.ScopeSelfRegister`), so a cross-service grant fails to compile. `AdminMethodScopes`/`AdminScope` live in `api/admin/v1/admin.go` and `AgentMethodScopes`/`AgentScope` in `api/agent/v1/agent.go`, each beside its proto bindings so a new RPC fails closed (admin covered by `TestAdminMethodScopes_CoversAllRPCs`). |
| `startup.go` | `CPStartupOrchestrator` — startup sequencing + aggregate `/healthz` endpoint (probes all 7 service ports) |
| `watcher.go` | `AgentWatcher` — polls Docker for `purpose=agent` containers; invokes drain-to-zero callback past grace/threshold (INV-B2-007) |
| `ory_configs.go` | `WriteOryConfigs(cp)` — generates Hydra/Kratos/Oathkeeper YAML config files |
| `subprocess.go` | `SubprocessManager` — manages Ory subprocess lifecycle (start, health, crash detection, shutdown) |
| `otelclient.go` | `NewOtelLoggerProvider(OtelClientOptions) (*sdklog.LoggerProvider, error)` — generic constructor for per-subsystem OTel log providers pushing OTLP/gRPC over mTLS to the trusted-infra receiver. Owns: preflight TLS dial (fails fast when monitoring stack is down, no background reconnect), `otel.SetErrorHandler` routing to file logger, `otlploggrpc` retry (default 10s vs SDK default 1min), optional `ExporterWrap` hook for caller-supplied decorators (circuit breaker, counters). `internal/controlplane/firewall/ebpf/netlogger` is the first consumer; future emitters (sysexec events, etc.) wire here too. |
| `cpboot/` | **Host-side CP bootstrap subpackage.** Contains `embed_cp.go` / `embed_ebpf.go` (`//go:embed assets/clawker-cp` + `assets/ebpf-manager`), `bootstrap.go` (`EnsureRunning` / `Stop` / `CPRunning` package-level funcs), `cp_container.go` (`BuildCPContainerConfig` → `CPContainerConfig`), `manager.go` (`Manager` interface (`EnsureRunning` / `Stop` / `IsRunning` / `ProbeHealthz`) + `NewManager`). Split out so `cmd/clawker-cp` can import `internal/controlplane` for `SubprocessManager` / `AdminServer` / `AgentWatcher` without dragging in the `go:embed` directives that would otherwise require the daemon to embed itself during its own build. |
| `mocks/` | moq-generated mocks: `IntrospectorMock`, `AdminServiceClientMock` |
| `cpboot/mocks/` | moq-generated `ManagerMock` for the host-side CP lifecycle noun |

## AdminService composition

`server.go` exposes the unexported `adminServer` type that embeds `*fwhandler.Handler` (and, in future branches, additional domain handlers). Method promotion produces the AdminServiceServer surface; `NewAdminServer(fw, agents, log)` is the wiring point used by `cmd/clawker-cp/main.go`.

The 13 firewall RPCs live in `internal/controlplane/firewall/handler.go` — see `internal/controlplane/firewall/CLAUDE.md` for the per-RPC table. Future domains (Monitor, Hostproxy, Clawkerd) embed alongside; the `<Domain><Action>[<Object>]` proto naming convention prevents method-name collisions.

All RPCs require the uniform `admin` scope (INV-B2-009) with one deliberate exception: `GetSystemTime` is mapped to the public scope (`consts.ScopePublic`) in `AdminMethodScopes()`, making it PUBLIC so the CLI can call it during token-exchange bootstrap before it holds a bearer token (the mTLS client cert is still required at the listener). An empty or unmapped scope fails closed (deny) — public is the explicit `ScopePublic` sentinel, never the zero value. Per-method scope diversification beyond this is intentionally not used — see Spec §8.

## Startup Sequence (`cmd/clawker-cp/main.go`)

1. Write Ory config files (`WriteOryConfigs(cp)`)
2. Start Kratos + Hydra subprocesses, wait healthy
3. Wait for Hydra admin port healthy, configure service probes (`orchestrator.SetServiceProbes(cp, tlsCfg)`)
4. Read CLI public JWK from bind-mount
5. Register CLI + agent clients with Hydra (`RegisterCLIClient` + `RegisterAgentClient` — both idempotent on 409)
6. Start Oathkeeper subprocess; build `*docker.Client`; build `firewall.Stack` (via `fwhandler.NewRulesStore`)
7. Load eBPF programs (`ebpfMgr.Load()`); run defensive startup cleanup (`ebpfMgr.CleanupStaleBypass()` — INV-B2-013)
8. Start gRPC AdminService on `cp.AdminPort` with mTLS (`RequireAndVerifyClientCert` + CA pool) + AuthInterceptor (CLI-scope vocabulary). The gRPC server hosts the 13 firewall RPCs + `ListAgents` + `GetSystemTime` (15 methods total; `GetSystemTime` is the lone public-scope RPC, for clock-sync bootstrap). Start a second gRPC server on `cp.AgentPort` (clawker-net only, NOT host-bound) using the same TLS material with two chained interceptors: `AuthInterceptor` (agent-scope vocabulary, `agentv1.AgentMethodScopes()`) runs first, then `agent.IdentityInterceptor` runs a universal three-stage identity gate on every AgentService RPC (no opt-outs, Register included): (a) CN pin to `consts.ContainerClawkerd`, (b) resolve the kernel-attested peer IP to a `purpose=agent` container via the injected `ContainerByPeerIP` dep (production: `agent.NewMobyPeerLookup`), reading `dev.clawker.{project,agent}` labels as the authoritative identity source, (c) constant-time compare the label-derived `AgentFullName` against the cert's `urn:clawker:agent:` URI SAN. On success the resolved `(containerID, project, agentName)` triple is attached to ctx via `WithResolvedContainer` for downstream handlers. The interceptor takes only `(peerLookup, log)` — no Registry or method-scopes map. AgentService currently exposes one inbound RPC (`Register`); the listener stays bound for any future inbound `clawkerd→CP` RPC. CP→clawkerd command dispatch is the OUTBOUND `agent.Dialer` path (now part of `internal/controlplane/agent/`) — see `internal/controlplane/agent/CLAUDE.md` and the asymmetric-trust clarification in the root `CLAUDE.md`. Both servers join the graceful-shutdown WaitGroup.
8c. Settings-driven firewall bringup (startup gate). When `firewall.enable` (settings.yaml) is true, run the in-process `FirewallInit` synchronously BEFORE `SetReady` — a green `/healthz` must mean "everything the settings enable is enforcing". Covers CP boots no CLI observes (restart policy resurrections). A bringup failure FAILS CP startup (pre-`SetReady` startup-gate exit, code 1, same doctrine as the `CleanupStaleBypass` gate) — degrading instead would leave agents either unusable (eBPF redirecting at a dead Envoy) or silently unenforced while the user believes the firewall is on. The exit does NOT flush eBPF, so already-enrolled agents stay fail-closed. Host side, `cpboot.waitForCPHealthz` extends its wait budget by `consts.FirewallStackBringupRPCTimeout` when the firewall is enabled, and fail-fasts with a diagnostic error when the CP container terminally exits mid-wait. Caveat: re-enrollment events published by this gate precede netlogger construction (step 9c), so netlogger's LabelCache stays cold for agents that outlived the previous CP until the next FirewallInit/FirewallEnable — telemetry enrichment only, enforcement unaffected
9. Mark ready (`orchestrator.SetReady()`), serve `/healthz` on HealthPort
9a. Wire the `dockerevents` feeder onto the bus + overseer stats heartbeat
9c. Construct `netlogger.Service` — builds a `*sdklog.LoggerProvider` via `NewOtelLoggerProvider` (mTLS leaf via `otelcerts.LoadTLSConfig("netlogger")`, ExporterWrap = `netlogger.NewCircuitExporter` with `FailureThreshold=3`, `service.name=ebpf-egress` so OS routes records to a separate stream from the CP zerolog bridge), wires the provider into `netlogger.New`, calls `Service.Start(watcherCtx)`. Every failure path on the chain (no otelcerts, no `OTEL_EXPORTER_OTLP_ENDPOINT`, plaintext-only endpoint, preflight dial timeout, constructor or Start error) emits `event=netlogger_unavailable` with the `step` field and leaves `netloggerSvc=nil`. The provider is `Shutdown()` immediately on degraded paths so the BatchProcessor goroutine doesn't leak. CP, firewall, AdminService, AgentService, registry all stay up.
9d. Start the periodic `dns_cache` GC goroutine — every `dnsGCInterval` (60s) calls `ebpfMgr.GarbageCollectDNS()` so the CoreDNS-populated dns_cache doesn't grow unbounded and stale orphaned hashes (since-removed zones) don't accumulate. GC skips the IP-literal seeds in `m.seededIPs` (SyncRoutes owns those). Recovers per CP no-panic discipline — the recover is per-sweep, emitting `event=dns_gc_panic`. A sweep counts as failed only if it panicked OR `GarbageCollectDNS` returned an error (the map could not be enumerated — wedged iterator — or an expired entry could not be deleted, logged `event=dns_gc_error`). A clean sweep that simply had nothing to reclaim (`cleared==0`, `err==nil`) is success, not failure, and does not count toward escalation — the silent-growth failure the detector exists to catch is a sweep that *cannot* reclaim (a non-nil error), not one that had nothing expired to reclaim. `dnsGCDegradedThreshold` (5) consecutive failed sweeps escalate once (per crossing, via the testable `dnsGCEscalator`) to `event=dns_gc_degraded` so an operator can tell a wedged GC (map no longer reclaimed) from one transient failure. Stopped via a `sync.Once`-guarded `stopDNSGC` (cancel + `WaitGroup.Wait`) both in the drain callback before `FlushAll` and deferred before `ebpfMgr.Close()`, so a sweep can't iterate/delete a dns_cache fd that's being torn down (same stop-before-teardown discipline as `netloggerSvc.Stop`).

9b. Start `controlplane.AgentWatcher` goroutine — polls Docker for agents with `purpose=agent`; on drain-to-zero invokes callback: `actionQueue.Close()` (drain queued work, reject new Submits with `ErrClosed`) → `grpcServer.GracefulStop()` (let in-flight RPCs return — any blocked on a Submit now observes `ErrClosed`) → `handler.CancelAllBypassTimers()` → `firewall.Stack.Stop()` → `netloggerSvc.Stop(stopCtx)` (5s bounded; drains ringbuf + flushes BatchProcessor BEFORE BPF maps go away — skipped when degraded) → `stopDNSGC()` → `ebpfMgr.FlushAll()` (INV-B2-007). Stack stop + netlogger stop + eBPF flush run post-Close directly from the drain callback because the queue is dead; aggregated errors propagate back to `Run` for non-zero exit. Then the outer shutdown path tears the CP container down (exit code 0 — the `on-failure` restart policy does NOT retrigger)

## Aggregate Health (`startup.go`)

`CPStartupOrchestrator` manages the `/healthz` endpoint:

- **Before ready**: returns 503
- **After ready**: actively probes all 7 service ports on every request:
  - Hydra public (TLS), Hydra admin (TLS)
  - Kratos public (TLS), Kratos admin (TLS)
  - Oathkeeper proxy (TLS), Oathkeeper API (TLS)
  - gRPC admin (raw TCP)
- Returns 200 only when ALL probes succeed

## Container Config (`cpboot/cp_container.go`)

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
- `ebpf.Route` + `ebpf.BPFContainerConfig` + `ebpf.DomainHash` — shared types / hash function used by `internal/dnsbpf` and `internal/controlplane/firewall` (`normalizeDomain`).
- `ebpf.Manager.EventsRingbuf()` / `EventsDrops()` / `RatelimitDrops()` / `DNSCache()` — read-only map accessors consumed by `netlogger.Service` for the per-decision egress event pipeline. All return nil before `Load()` — callers MUST nil-check.

## Ory Config Generation (`ory_configs.go`)

```go
func WriteOryConfigs(cp config.ControlPlaneSettings, hydraSecret string) error
```

Generates `/etc/clawker/{hydra,kratos,oathkeeper}.yaml`:

- **Hydra**: in-memory DSN, JWT access tokens, admin at `127.0.0.1:4445` (internal-only), public at `0.0.0.0:4444`, 1h access token TTL
- **Kratos**: in-memory DSN, `127.0.0.1:4480` (placeholder for future webui identity)
- **Oathkeeper**: HTTP reverse proxy at `0.0.0.0:4455` (placeholder for future webui auth), API at `127.0.0.1:4456`

## Subprocess Management (`subprocess.go`)

```go
type SubprocessManager struct { ... }
func (sm *SubprocessManager) Start(name string, cmd *exec.Cmd) error
func (sm *SubprocessManager) WaitHealthy(ctx context.Context, name string, check HealthCheck) error
func (sm *SubprocessManager) CrashChan() <-chan error
func (sm *SubprocessManager) Shutdown(timeout time.Duration)
```

Manages Ory service lifecycle. Crash reporting via channel. Shutdown sends SIGTERM then SIGKILL, reverse start order.

## Test seam overview

- `EBPFManager` interface — `firewall/ebpf/mocks/EBPFManagerMock` for firewall handler tests.
- `Introspector` interface — `mocks/IntrospectorMock` for authz tests (no real Hydra).
- `cpboot.Manager` interface — `cpboot/mocks/ManagerMock` for break-glass `controlplane up/down/status` CLI tests.
- `adminv1.AdminServiceClient` — `mocks/AdminServiceClientMock` for CLI tests that speak to the AdminService.
- `firewall.ContainerResolver` — handler-side injectable Docker lookup (see `firewall/CLAUDE.md`).
- `agent.Registry` — moq-generated `RegistryMock` (test-only file at `internal/controlplane/agent/registry_mock_test.go`) for `IdentityInterceptor`, `ListAgents`, and the dialer-side classification tests that need a deterministic snapshot independent of dockerevents wiring.

## Test coverage

| File | Invariants | What |
|------|------------|------|
| `authz_test.go` | INV-B1-011 | Token validation, scope enforcement, unmapped method denial, Hydra introspection mock |
| `grpc_mtls_test.go` | — | mTLS connection acceptance (valid cert), rejection (no cert), rejection (no TLS) |
| `container_config_test.go` | INV-B1-005, 006, 008, 009, 015, 017, 018, 020 | Port bindings, mounts, labels, private key exclusion (signing + client), config-driven ports |
| `lifecycle_test.go` | INV-B1-010, 013 | /healthz 503→200 transition, eBPF lifecycle gating, aggregate probes |
| `ebpf/manager_test.go` | — | Link cleanup, map schema detection, Install/Remove/Enable/Disable, SyncRoutes, DNS cache GC |
| `subprocess_test.go` | — | Start/WaitHealthy, crash detection, SIGTERM/SIGKILL shutdown |
| `ebpf_regression_test.go` | — | eBPF package regressions (no kernel required) |

## Package imports

**Uses**: `internal/config`, `internal/consts`, `internal/docker`, `internal/logger`, `internal/controlplane/firewall`, `internal/controlplane/firewall/ebpf`, `api/admin/v1`, `google.golang.org/grpc`, `github.com/cilium/ebpf`, `github.com/moby/moby/api/types/{mount,network}`, `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/log`, `go.opentelemetry.io/otel/sdk/log`, `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc` (the last four via `otelclient.go`'s `NewOtelLoggerProvider`).

**Used by**: `cmd/clawker-cp/` (startup sequence), `internal/cmd/controlplane/` (break-glass up/down/status), `internal/cmd/factory/` (AdminClient + ControlPlane Factory closures), `internal/cmd/firewall/` (AdminService consumers via `f.AdminClient`), `internal/cmd/container/shared/` (BootstrapServicesPostStart), `internal/controlplane/firewall/ebpf/netlogger` (consumes `NewOtelLoggerProvider`), `internal/dnsbpf` (reuses ebpf types), `internal/auth` (cert paths).

No circular dependencies.

## What's deferred

- **Kratos active usage** — running as subprocess placeholder. Lights up with webui.
- **Oathkeeper active routing** — running with empty rules. Lights up with webui HTTP auth.
- **Per-method scopes beyond `admin`** — finer-grained scopes (`webui:read`, etc.) would add entries to `AdminMethodScopes()` in `api/admin/v1/admin.go` (typed `adminv1.AdminScope`). INV-B2-009 mandates a uniform `admin` scope across all AdminService methods except the public bootstrap RPC `GetSystemTime` (`consts.ScopePublic`). The agent listener's scope vocabulary lives in `AgentMethodScopes()` in `api/agent/v1/agent.go` (typed `agentv1.AgentScope`) and currently holds the single `Register` → `ScopeSelfRegister` entry.

## Known limitations (deferred to cp-restart-resilience)

- **CP restart resilience.** When the CP restarts, clawkerd's inbound `:7700` listener stays up and the dialer re-establishes the Session once the new CP boots and reaches the container. The agent registry sqlite DB persists across CP restarts so identity is preserved without re-bootstrapping.
