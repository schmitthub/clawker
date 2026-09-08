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
  (`NewAgentWatcher`), the executor (`NewExecutor`), the registry subscriptions
  (`agent.Start`). No `*Domain` symbol names. No `startAgentDomain`-style blobs — a
  package is not a startable vehicle.
- The CP wires those concrete constructors inline; it is not "an orchestrator calling
  domain wrappers". Don't oversell DDD/"orchestrator" terminology.
- `run()` reads top-to-bottom through concrete calls. No numbered `// Phase N` comment
  scaffolding — a function that needs a comment table-of-contents is a god-function.

## Control-plane safety

Read this section before changing CP startup, serving, their dependencies, or
the CP-to-agent trust contract. The failure rules apply to every package on the
CP boot or serve path, including packages outside this directory
(`internal/controlplane/`, `cmd/clawkercp/`, `clawkerd/`, `internal/clawkerd/`,
`cmd/clawkerd/`, and anything they import).

### CP and firewall lifecycles

- **CP is unconditional infrastructure.** Auth (Hydra/Kratos/Oathkeeper), AdminService gRPC on `AdminPort`, AgentService gRPC on `AgentPort`, agent registry, mTLS, OAuth2 — all running whenever any clawker container exists. CP boots via `Manager.Start` (`controlplane/manager`). No "disable CP" flag. CP owns the clawker network.
- **Firewall is one optional subsystem CP manages.** Envoy + custom CoreDNS + eBPF egress enforcement. Toggled by `firewall.enable` in `settings.yaml` (NOT `clawker.yaml`). When disabled, CP/mTLS/registry/agent.Dialer/ListAgents continue to operate.

Do **NOT** gate non-firewall behavior on `firewall.enable`.

### CP crashing is a SECURITY incident, not an availability one

This is the single most important invariant in the codebase. Read it before adding any failure path to CP code.

**What happens when CP crashes (panic, log.Fatal, unrecovered goroutine):**

1. PID 1 exits. CP container goes down. `on-failure` restart policy retries 3×; if the bug is deterministic (most are), CP stays dead.
2. **eBPF programs stay attached to cgroups.** They're pinned under `/sys/fs/bpf` and survive the CP container's death. Agent containers' egress traffic continues to be filtered by whatever rule set was loaded at the moment CP died.
3. **The clean drain-to-zero path is skipped.** `firewall.Stack.Stop()` and `ebpfMgr.FlushAll()` only run on intentional shutdown via the orchestrator. A panic skips both. eBPF state is now frozen and unsupervised.
4. **Agent containers keep running.** They have no awareness that their supervisor died. They keep serving their workloads.
5. **The user has no idea.** They see agents running. They assume the firewall is enforcing — and it technically is, against the rules that happened to be loaded. They assume CP is observing — it isn't. They assume CP can dispatch containment — it can't.

**The result:**

- No new firewall rules can be applied (`clawker firewall add` writes to the rules file but Envoy/CoreDNS need CP to reload).
- No bypass can be expired (`clawker firewall bypass <duration>` schedules a CP-side timer; if CP died during a bypass, the bypass is now permanent until the user manually intervenes).
- No CP→clawkerd Session means no command dispatch, no observation of agent behavior, no containment commands available even if compromise is detected.
- Agents are vulnerable to prompt injection, exfiltration, and lateral-movement attempts that CP would otherwise observe and contain. The user's mental model ("CP has them covered") is silently false.

The stack trace from a CP panic lands on `os.Stderr` → `docker logs <cp>`. It is NOT in the rotating `ControlPlaneLogFile` operators are wired to grep. It is NOT surfaced by `clawker controlplane status` (which only knows up/down). The user has to know to dig into raw docker logs to find it.

**Hard rules for code on the CP boot/serve path** (`cmd/clawkercp/`, `internal/controlplane/`, anything imported by them):

1. **No `panic()`. No `log.Fatal()`. No `os.Exit()`** outside the orchestrator's intentional shutdown sequence. Constructors return `(nil, error)` (see `agent.NewDialer`, `agent.NewExecutor`); main logs structurally and degrades. The only hard-exits permitted are: drain-to-zero clean exit (code 0), and the orchestrator's pre-`SetReady` startup-gate failures (code 1) — these exit WITHOUT flushing eBPF, so any agents enrolled by a previous CP stay fail-closed (filtered against the old rule set) rather than fail-open.
2. **Every long-lived goroutine recovers.** Heartbeats, watchers, event handlers, RPC handlers — wrap with `defer func() { if r := recover(); r != nil { log.Error().Interface("panic", r)... } }()`. The pub/sub stats heartbeat (`pubsub.NewStatsHeartbeat`, wired in `internal/controlplane/cmd.go`) is the canonical template. One bad event must not take down the daemon and silently strand eBPF.
3. **Subsystem failures degrade, never cascade.** A broken Executor → `executor = nil`; CP never dispatches `AgentReady`, clawkerd-as-PID-1 never spawns the user CMD, and the container exits non-zero on `docker stop`; the firewall, registry, AdminService, dialer all stay up. A broken dialer → `dialer = nil`; CP→clawkerd dispatch disabled; everything else stays up. The patterns in `internal/controlplane/cmd.go` — `wireExecutor` (executor; emits `event=agent_executor_unavailable`) and the `agent.NewDialer(...)` block that degrades on error to `event=agent_dialer_unavailable` — are the templates; copy either for any new subsystem.
4. **Every degraded path emits a structured log line.** `event=<subsystem>_unavailable` with component, error, downstream impact. Operator must be able to determine root cause AND blast radius from the structured log surface alone — they will not see panic stacks.
5. **Treat CP shutdown as a privileged operation.** If you find yourself thinking "this should never happen, just panic," stop. In CP that line of reasoning compromises the security boundary the user trusts to be intact. Return an error and let the orchestrator decide.

If you're tempted to write `panic()` in CP code, ask: "would this leave eBPF programs pinned with no supervisor?" If yes — you've just turned a logic bug into a silent firewall failure. Return an error instead.

### Asymmetric trust: dialer permissive, listener strict

- **clawkerd-side listener (server):** STRICT. `clawkerd/listener.go` enforces CP CN pin + Client-Auth EKU + CA chain at TLS layer.
- **CP-side dialer (client):** PERMISSIVE. `controlplane/agent.Dialer` never aborts on cert/identity grounds. Outcomes emitted as typed fields on `SessionConnected` events. Dial only fails on connectivity.

**Why permissive:** CP must reach clawkerd to issue containment commands even when certs are bad. Subscribers to `SessionConnected` enact policy; the dialer holds none.

**Trust attestation:** The CLI mints the agent certificate. CP writes the registry row through the Register handler. The dialer checks the peer certificate thumbprint against the row and emits the result on the bus. See [the agent package](agent/AGENTS.md) for the identity contract.

Package-specific behavior below must preserve those requirements.

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
| `agent/` | Agent bounded context — sqlite registry, in-memory worldview repository, CP→clawkerd dialer (`agent.New`), `NewAgentWatcher`, `NewExecutor`, `IdentityInterceptor`. See `controlplane/agent/AGENTS.md`. |
| `server/` | gRPC composition: `NewAdminServer(fw, agents, log) (adminv1.AdminServiceServer, error)` (`server.go`) + `NewGRPCStack(GRPCDeps) (*GRPCStack, error)` (`grpc_stack.go`) — builds both listeners (admin + agent), wires interceptors, registers services. |
| `auth/` | Ory auth stack: `AuthInterceptor`/`HydraIntrospector` (`authz.go`), `RegisterCLIClient`/`RegisterAgentClient` (`hydra_client.go`), `WriteOryConfigs` (`ory_configs.go`), Ory subprocess bringup (`ory_stack.go`). Mocks in `auth/mocks/`. |
| `subprocess/` | `SubprocessManager` + `NewSubprocessManager` — Ory subprocess lifecycle (start, health, crash detection, reverse-order shutdown). |
| `otel/` | `NewOtelLoggerProvider(OtelClientOptions) (*sdklog.LoggerProvider, error)` (`otelclient.go`) — generic per-subsystem OTel log-provider factory pushing OTLP/gRPC over mTLS to the trusted-infra receiver. |
| `firewall/` | Envoy + CoreDNS + eBPF egress enforcement; `firewall.Handler` (the 13 firewall RPCs), `firewall.Stack`, Envoy/CoreDNS config generation, and the `ebpf/` subtree (loader + netlogger). See `controlplane/firewall/AGENTS.md`. |
| `manager/` | **Host-side CP lifecycle.** `ensureRunning`/`Stop`/`CPRunning` (`bootstrap.go`), `BuildCPContainerConfig` (`cp_container.go`), `Manager` interface (`Start`/`Stop`/`IsRunning`/`ProbeHealthz`) + `NewManager` (`manager.go`) — `Start` is the idempotent bringup; a boot the CP cannot finish alone surfaces as `*CPSOSError` for the CLI bootstrap verbs to assist (`internal/cmd/controlplane/shared`). Also the `//go:embed` of `clawkercp` + `ebpf-manager` + the host-side `bpffs-delegate` (`embed_cp.go`/`embed_ebpf.go`/`embed_bpffs.go`). Replaces the former `cpboot/`. See `controlplane/manager/AGENTS.md`. |
| `adminclient/` | CLI-side AdminService dialer (`dial.go`): `Dial`, `ProbeCPTime`, `LoadClientCert`, the two TLS configs (token-endpoint plain TLS vs gRPC mTLS), token source. |
| `infracerts/` | Trusted-infra (OTLP/monitoring) mTLS cert material. See `controlplane/infracerts/AGENTS.md`. |
| `otelcerts/` | OTel client cert provisioning. See `controlplane/otelcerts/AGENTS.md`. |
| `sdscerts/` | Dedicated Envoy→CP SDS client identity (`envoy-sds-client` leaf, own material dir + readiness gate — deliberately NOT the telemetry lane's certs). See `controlplane/sdscerts/AGENTS.md`. |

## AdminService composition

`controlplane/server/server.go` exposes the unexported `adminServer` type that embeds `*firewall.Handler` (and, in future branches, additional RPC handlers). Method promotion produces the AdminServiceServer surface. `server.NewAdminServer(fw, agents, recovery, log) (adminv1.AdminServiceServer, error)` is the composition constructor — it returns an error (e.g. `ErrNilRegistry`, `ErrNilRecovery`) rather than panicking, per the CP no-crash contract. It is composed into the gRPC stack by `server.NewGRPCStack` (`controlplane/server/grpc_stack.go`), which `buildGRPCStack` in `internal/controlplane/cmd.go` calls to build and serve both listeners.

The 13 firewall RPCs live in `controlplane/firewall/handler.go` — see `controlplane/firewall/AGENTS.md` for the per-RPC table. Future handlers (Monitor, Hostproxy, Clawkerd) embed alongside; the `<Subsystem><Action>[<Object>]` proto naming convention prevents method-name collisions.

All RPCs require the uniform `admin` scope (INV-B2-009) with one deliberate exception: `GetSystemTime` is mapped to the public scope (`consts.ScopePublic`) in `AdminMethodScopes()` — no bearer token, mTLS client cert still required at the listener — because it bootstraps the token exchange itself. `WatchSOS` is admin-scoped like the rest (recoverable failures only happen after the Ory stack is up — anything earlier exits 1 — so the CLI can always mint a token by the time there is something to watch): it is a server stream the CLI holds open while the CP boots, carrying an error or nothing — a delivered `SOS` is a startup failure the CLI can assist with while the CP stays alive waiting, a clean end-of-stream means resolved or nothing to report; the `SOS.kind` enum (`SOSKind`) is the CLI's dispatch discriminator — it switches on kind and never parses `message`, and an unknown kind (older CLI, newer CP) surfaces `message` as the error rather than hanging; unrecoverable failures never appear — the CP exits non-zero as usual (see the recovery queue in `internal/controlplane/cmd.go`). The ready-gate exemption list (`readyGateExemptMethods` in `controlplane/server/grpc_stack.go`) is the public set plus `WatchSOS`: the admin listener serves from construction, and the ready-gate interceptor rejects every non-exempt RPC with `codes.FailedPrecondition` until `SetReady`. An empty or unmapped scope fails closed (deny) — public is the explicit `ScopePublic` sentinel, never the zero value. Per-method scope diversification beyond this is intentionally not used — see Spec §8.

## Startup Sequence (`run()` in `internal/controlplane/cmd.go`)

`run()` reads top-to-bottom in two halves — CONSTRUCTION (objects only, no side effects) then the STARTUP FLOW (ordered method calls that create the side effects). BPF pins, subprocesses, and firewall state are flow steps, never construction side effects.

1. `bootLogging` — trusted-lane OTel certs + the file/OTEL logger (degraded `os.Stderr` fallback when `New` fails).
2. Config + signal-aware contexts — `config.NewConfig`, `signalCtx` (SIGTERM/SIGINT), `watcherCtx` (long-lived workers), `subprocess.NewSubprocessManager`, `NewControlPlane`.
3. `auth.NewOryStack` — CONSTRUCTION only: builds the single CA pool + CA TLS surface from `caCertPath` up front. No subprocess starts here — Ory bringup is flow step 9.
4. `buildEnforcement` — CONSTRUCTION only: Docker client + `firewall.Stack` + the `firewall.EgressRulesStore` + the shared `firewall.CAStore` + the `RouteIdentityStore`-backed `IdentityAllocator` + `ebpf.NewManager`. No BPF state is created here; `ebpfLoadFlow` (flow step 9) is the step that loads. Returns the joined cleanup, safe on an unloaded manager.
5. `buildTopics` — the typed pub/sub topics (`dockerTopic`, `agentTopic`, `enrolledTopic`); one topic per payload type, the generic audit hook self-attaches in `NewTopic`.
6. `buildAgentInfra` — agent sqlite registry + `MobyPeerLookup` + `ContainerLister` + the in-memory `agent.Repository` (worldview) with its agent-event and docker-event subscriptions wired.
7. `buildGRPCStack` — firewall `ActionQueue` + `fwhandler.Handler` (holds publish-only `enrolledTopic`) + the admin (`cp.AdminPort`, mTLS + ready gate + CLI-scope AuthInterceptor) and agent (`cp.AgentPort`, clawker-net only, agent-scope AuthInterceptor chained ahead of `agent.IdentityInterceptor`) gRPC listeners; both listeners are BOUND here and the agent listener starts serving (`ServeAgent`) — boot-time clawkerd dial-back/registration needs it. The admin surface hosts the 13 firewall RPCs + `ListAgents` + the bootstrap RPCs (the public `GetSystemTime`, the admin-scoped ready-gate-exempt `WatchSOS`). `IdentityInterceptor` runs a universal three-stage gate (CN pin to `consts.ContainerClawkerd` → peer-IP→`purpose=agent` container resolution reading `dev.clawker.{project,agent}` labels → constant-time `AgentFullName` vs `urn:clawker:agent:` URI SAN compare). CP→clawkerd dispatch is the OUTBOUND dialer (step 12), not this listener — see `controlplane/agent/AGENTS.md` and [asymmetric trust](#asymmetric-trust-dialer-permissive-listener-strict).
8. Serve the boot surfaces — `SetAdminServingCheck`, then `grpcStack.ServeAdmin(serveFailed)` MID-BOOT: the ready-gate-exempt bootstrap RPCs answer while the flow below runs (`WatchSOS` is the CLI's window into a boot waiting for assistance), and the ready-gate interceptor rejects every other admin RPC with `codes.FailedPrecondition` until `SetReady` — no rule mutation is accepted mid-boot. Then `startHealthz`, which answers 503 `not_ready` until the flow completes.
9. STARTUP FLOW — ordered side-effect steps, each a pre-`SetReady` gate (exit 1, no eBPF flush, enrolled agents stay fail-closed):
   - `oryStack.Start` — writes Ory configs, starts Kratos + Hydra + Oathkeeper subprocesses, waits healthy, registers the CLI + agent Hydra clients; then `SetServiceProbes` installs the aggregate `/healthz` probes.
   - `ebpfLoadFlow` (a `ControlPlane` method — the recovery protocol is orchestrator state management) — `ebpfMgr.Load()` + `CleanupStaleBypass` (INV-B2-013). On the default deployment this is the whole story. The permission-denied load is the ONE recoverable failure (rootless Docker) and the only thing that engages any delegation machinery (`delegateBPFFSFlow`): kernel gate (`ebpf.CheckKernelSupport`, 6.9 floor — too old is terminal, no SOS), `ebpf.OpenForDelegation` (fsopen; the superblock's owning userns is stamped from this process), publish ONCE on the orchestrator's recovery queue (`PublishRecovery` — delivered to every connected `WatchSOS` stream, and to any that connects later), then serve the filesystem context on the handoff socket bounded by `consts.CPSOSIdleTTL` (30s — a CP nobody is listening to shuts down rather than waiting forever). Once the helper acks, `ClearRecovery` ends every watcher stream cleanly and the flow returns `errBPFFSDelegated` — `Main` maps it to a CLEAN exit (code 0, so the on-failure restart policy does not resurrect a process whose mount namespace predates the delegated filesystem), and the CLI's `Manager.Start` retry recreates the CP container onto the delegated path when the source changed (the bpffs-source drift gate) or simply restarts it when the helper replaced a stale mount at the same path — Docker re-establishes bind mounts at every container start. No in-process retry exists by design: cilium/ebpf caches its token decision on the first BPF syscall. Every other load failure is unrecoverable and exits 1 as before.
   - `startSDSServer` — the firewall on-demand certificate SDS listener (Envoy ↔ CP, clawker-net only, `ControlPlaneSettings.SDSPort`). Serves per-SNI MITM leaves to Envoy's wildcard-chain certificate selector. mTLS narrowed by a `VerifyConnection` SAN pin to the dedicated `consts.EnvoySDSClientName` leaf (provisioned by `controlplane/sdscerts`, constructed in `run()` and threaded through `buildEnforcement` into the stack; the telemetry lane's `envoy-otel-client` leaf is refused). NOT a gate: any failure degrades with `event=sds_unavailable` / `event=sds_certs_unavailable` (wildcard multi-label handshakes fall back to static certs; everything else stays up). Runs before the firewall gate so Envoy can fetch secrets from its first boot. The deferred stop waits up to `sdsStopTimeout` (`defaultShutdownWait`) for streams to close, then logs `event=sds_graceful_stop_timeout` and calls `grpc.Server.Stop`.
   - `firewallBringupGate` — when `firewall.enable` (settings.yaml) is true, runs `FirewallInit` synchronously BEFORE `SetReady` so a green `/healthz` means "everything the settings enable is enforcing". A failure FAILS startup (logged `event=firewall_bringup_failed`, bounded by `consts.FirewallStackBringupTimeout`). Caveat: re-enrollment events published by this gate precede netlogger construction (step 11), so netlogger's label cache stays cold for agents that outlived the previous CP until the next FirewallInit/FirewallEnable — telemetry enrichment only, enforcement unaffected.
   - `orchestrator.SetReady()` — the ready gate flips; the already-serving admin surface accepts everything from here; `/healthz` can now go green.
10. `startFeeder` — the `dockerevents` feeder, sole producer of `DockerEvent` onto its typed topic.
11. `startWorkers` — the long-lived observability workers: the `pubsub.NewStatsHeartbeat`, the `netlogger.Service` (subscribes `enrolledTopic` to hydrate its label cache; degrades to `netloggerSvc=nil` with `event=netlogger_unavailable` on any chain failure), and the `dns_cache` GC goroutine (`event=dns_gc_*`, escalates `dns_gc_degraded` after `dnsGCDegradedThreshold` consecutive reclaim-failures). All run on `watcherCtx`.
12. Agent watcher + `startAgentDialer` — `agent.NewAgentWatcher` (drain-to-zero trigger; its goroutine recovers panics into a terminal shutdown error, `event=agent_watcher_panic`) plus the executor, CP→clawkerd dialer, and agent-axis subscriptions (§3.4 degrade contract).
13. Serve + drain — the select waits on signal / drain-to-zero / subprocess crash / serve failure, then runs the drain callback (`actionQueue.Close()` → `grpcStack.GracefulStop()` → `handler.CancelAllBypassTimers()` → `firewall.Stack.Stop()` → `netloggerSvc.Stop` → `stopDNSGC()` → `ebpfMgr.FlushAll()`, INV-B2-007) exactly once (sync.Once). EVERY arm drains — the subprocess-crash and serve-failure arms (`event=cp_subprocess_crashed` / `event=cp_serve_failed`) run the callback before returning their error, since a bare return would exit PID 1 with eBPF pinned and unsupervised. Drain-to-zero and signal exit 0 (the `on-failure` restart policy does NOT retrigger); a crash or serve failure exits 1 after draining.

## Aggregate Health (`internal/controlplane/cmd.go`)

The `ControlPlane` orchestrator type (`NewControlPlane`, `SetReady`, `HealthzHandler`, `SetServiceProbes`) manages the `/healthz` endpoint:

- **Before ready**: returns 503
- **After ready**: actively probes all 7 service ports on every request:
  - Hydra public (TLS), Hydra admin (TLS)
  - Kratos public (TLS), Kratos admin (TLS)
  - Oathkeeper proxy (TLS), Oathkeeper API (TLS)
  - gRPC admin (raw TCP **plus** a serving check — `GRPCStack.AdminServing`, installed via `SetAdminServingCheck`)
- Returns 200 only when ALL probes succeed

The admin listener socket is bound at gRPC stack construction, so a bare TCP
dial completes out of the accept backlog whether or not anything is in
`Serve`. The grpc-admin probe therefore gates its dial on the stack's serving
signal (`GRPCStack.AdminServing`): the dial proves reachability, the signal
proves liveness. The check fails closed until it is installed (right after
`buildGRPCStack`, before `ServeAdmin`). `ServeAdmin` runs mid-boot — the ready
gate in the interceptor chain, not late serving, is what keeps non-public RPCs
out until `SetReady`.

## Container Config (`controlplane/manager/cp_container.go`)

```go
func BuildCPContainerConfig(cfg config.Config, opts CPContainerOpts) (*CPContainerConfig, error)
```

All ports from `cfg.ControlPlaneSettings()` (defaults via struct tags). Published to `127.0.0.1` only:

| Published Port | Purpose |
|----------------|---------|
| AdminPort (7443) | gRPC AdminService |
| HydraPublicPort (4444) | OAuth2 token endpoint |
| OathkeeperPort (4456) | HTTP reverse proxy (future webui) |
| HealthPort (7080) | /healthz endpoint |

**Not published**: Hydra admin (4445), Kratos ports, Oathkeeper API — internal-only (`127.0.0.1` bind inside container).

**Key mounts**: config dir (RO), CA cert (RO), CLI public JWK (RO), server TLS cert+key (RO), `FirewallDataSubdir` → `/var/lib/clawker/firewall` (RW — egress rules, Envoy/CoreDNS configs, MITM CA), `/sys/fs/cgroup` (RO), the BPF filesystem → `/sys/fs/bpf` (RW, no propagation options; source chosen by `resolveBPFFSSource` — the host's own `/sys/fs/bpf` by default, clawker's delegated bpffs at `BPFFSSubdir` when one exists after a rootless heal; the choice is stamped as `consts.LabelCPBPFFSSource` for the recreate drift gate and injected as `consts.EnvHostBPFFSSource` for the CoreDNS sibling), logs dir.

**Restart policy**: `on-failure` with `MaximumRetryCount=3`. A clean drain-to-zero exit (code 0 from `AgentWatcher`) does NOT retrigger the policy.

**Invariant INV-B1-006**: CLI private signing key is NEVER mounted into the container.

**Capabilities**: `BPF`, `SYS_ADMIN` (for eBPF program attachment).

## eBPF Subsystem

The eBPF subsystem lives at `firewall/ebpf/` — see `firewall/ebpf/CLAUDE.md` for full reference. Key surface consumed by CP core:

- `ebpf.Manager` — concrete loader. `Load()` runs once at CP startup; `CleanupStaleBypass` runs before `SetReady` (INV-B2-013); `FlushAll` runs during drain-to-zero (INV-B2-007).
- `ebpf.EBPFManager` interface — consumed by `firewall.Handler`. Methods: `Install`, `Remove`, `Enable`, `Disable`, `SyncRoutes`, `FlushAll`.
- `ebpf.Route` + `ebpf.BPFContainerConfig` — shared types used by `internal/dnsbpf` and `controlplane/firewall`. Route identities are allocated by `firewall.IdentityAllocator` (sticky, persisted), never derived by hashing.
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
- `cpmanager.Manager` interface — `cpmanagermocks.ManagerMock` (`controlplane/manager/mocks`) for break-glass `controlplane up/down/status` CLI tests.
- `adminv1.AdminServiceClient` — `api/admin/v1/mocks.AdminServiceClientMock` for CLI tests that speak to the AdminService.
- `firewall.ContainerResolver` — handler-side injectable Docker lookup (see `controlplane/firewall/AGENTS.md`).
- `firewall.EgressRulesStore` / `firewall.RouteIdentityStore` — the two store-backed firewall domain facades; moq mocks in `controlplane/firewall/mocks/`. CP wiring (`buildEnforcement`) threads the interfaces, never a `storage.Store`. Firewall's own tests use real stores instead.
- `agent.Registry` — moq-generated `RegistryMock` (in `controlplane/agent/mocks/registry_mock.go`) for `IdentityInterceptor`, `ListAgents`, and the dialer-side classification tests that need a deterministic snapshot independent of dockerevents wiring.

## Test coverage

Tests for CP entrypoint helpers belong in `internal/controlplane/cmd_helpers_test.go`, including SDS listener shutdown and client SAN checks. Tests for the SDS service belong in `controlplane/firewall/`.

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
