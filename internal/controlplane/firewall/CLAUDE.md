# Controlplane Firewall Subpackage

Firewall domain under the control plane. Owns the egress enforcement surface: Envoy + CoreDNS config generation, MITM CA + per-domain certs, egress rules store, eBPF manager (under `ebpf/`), CoreDNS binary embed, Docker network discovery, cgroup resolution, and the gRPC firewall-domain handler on `adminv1.AdminServiceServer`.

## Architecture

```
clawker CLI
    │  f.AdminClient(ctx) — mTLS + OAuth2 JWT
    ▼
internal/controlplane/adminServer  (embeds *firewall.Handler)
    │
    ▼
firewall.Handler (13 RPCs)
    ├── Stack         → Envoy + CoreDNS containers (on clawker-net)
    ├── ebpf.Manager  → pinned BPF maps + attached programs
    ├── Store         → egress-rules.yaml (gofrs/flock, atomic rename)
    ├── Resolver      → Docker-backed (cid, cgroupPath, exists, err)
    └── Certs (lazy)  → on-disk CA + per-domain certs
```

- **No host-side daemon**: `internal/firewall/` is gone. Lifecycle authority is the `clawker-controlplane` container (see `../CLAUDE.md` for startup sequencing). First CLI call triggers `controlplane.EnsureRunning` via `adminClientFunc`; when the `AgentWatcher` observes drain-to-zero + grace, the CP self-shuts-down (INV-B2-007).
- **Composite server**: `controlplane.adminServer` embeds `*firewall.Handler`; Go method promotion surfaces all 13 RPCs. Future domain handlers (monitor, hostproxy, clawkerd) embed alongside.
- **Per-container RPCs carry only `container_id`**: path resolution is hidden behind the injected `ContainerResolver`. The wiring in `cmd/clawker-cp/main.go::containerResolverFromDocker` calls `DetectCgroupDriver` once at CP startup and captures the driver string in the resolver closure; every RPC call goes through the resolver, which invokes `ResolveContainerID` + `EBPFCgroupPath(driver, cid)` (INV-B2-016 drift guard). The Handler itself holds no cgroup driver state.

## Files

| File | Purpose |
|------|---------|
| `handler.go` | `Handler` + `HandlerDeps` + `ContainerResolver` + `StackLifecycle` — 13 RPCs, bypass timer management, rules-store mutation helpers, `Proto↔Config` rule conversion |
| `stack.go` | `Stack` — Envoy + CoreDNS container lifecycle via DooD; image build helpers (`drainPullStream`, `ensureEnvoyImage`, `ensureCorednsImage`); health probing; `EnsureRunning`/`Stop`/`Reload`/`WaitForHealthy`/`Status` + IP/CIDR accessors |
| `status.go` | `Status` struct returned by `Stack.Status` (per-container up state, IPs, rule count) |
| `cgroup.go` | `DetectCgroupDriver(ctx, *docker.Client)`, `EBPFCgroupPath(driver, cid)`, `ResolveContainerID(ctx, *docker.Client, ref)`, `IsCanonicalContainerID` |
| `drift.go` | `resolveBypassCgroupID(entry, resolver, log)` — shared INV-B2-016 drift resolver used by direct Enable (`resolveForEnable`) and the bypass dead-man timer |
| `envoy_config.go` | Envoy YAML generation; per-domain filter chains; LOGICAL_DNS clusters; TCP/SSH listeners (`normalizeDomain` lives here — used by certs, coredns_config, rules_store, and shared with `internal/dnsbpf` via `ebpf.DomainHash`) |
| `coredns_config.go` | Corefile generation; per-domain forward zones; `dnsbpf` plugin directive; catch-all NXDOMAIN |
| `certs.go` | CA keypair generation/loading; per-domain cert signing; wildcard SANs; `RotateCA` |
| `rules_store.go` | `EgressRulesFile` schema + `NewRulesStore(cfg)` + `ProjectRules(cfg)` + rule helpers (`ValidateDst`, `NormalizeRule`, `RuleKey`, `NormalizeAndDedup`) |
| `network.go` | `NetworkInfo` + `DiscoverNetwork(ctx, *docker.Client, cfg)` + `ComputeStaticIP(gateway, lastOctet)` |
| `embed_coredns.go` | `//go:embed assets/coredns-clawker` — exported `CoreDNSClawkerBinary` |
| `errors.go` | Sentinels (`ErrEnvoyUnhealthy`, `ErrCoreDNSUnhealthy`, `ErrCPUnhealthy`) + `HealthTimeoutError` |
| `ebpf/` | eBPF subsystem — see `ebpf/CLAUDE.md` |
| `mocks/` | Moq-generated mocks for handler-local interfaces used by handler tests |
| `testdata/` | Golden files (e.g., `corefile_basic.golden`) |
| `assets/` | `coredns-clawker` Linux binary (gitignored; built by `make coredns-binary`) |

## Handler RPCs (B2 scope-corrected surface — 13 methods)

Every RPC requires the uniform `"admin"` scope (INV-B2-009). Per-method scope diversification is intentionally not used; see `.correctless/specs/cp-initiative/branch-2-cp-owns-firewall.md` §8.

| RPC | Scope | Purpose |
|-----|-------|---------|
| `FirewallInit` | global | Idempotent stack-up: `ensureConfigs` → ensure Envoy/CoreDNS images → ensure containers attached to `clawker-net` at static IPs → `WaitForHealthy`. Returns Envoy/CoreDNS IPs + network ID. BPF attach happens at CP startup, not here. |
| `FirewallRemove` | global | Global teardown: `CancelAllBypassTimers` → `Stack.Stop` → `ebpf.Manager.FlushAll` (wipe container_map + bypass_map + unpin links) → wipe rules store. |
| `FirewallEnable(container_id)` | per-container | Idempotent enroll. `resolveForEnable` → Docker lookup → fresh `cgroup_id` via `EBPFCgroupPath`. BPF `container_config` is built CP-side from `Stack.NetworkInfo` (Envoy/CoreDNS/gateway/CIDR) + `cfg.EnvoyEgressPort()` + `resolveHostProxy` (resolves `host.docker.internal` when the project has host proxy enabled). Writes `container_map` + attaches links via `ebpf.Manager.Install` + clears any bypass flag. Drift guard logs stored-vs-fresh cgroup_id delta. Returns `FailedPrecondition` if Docker says the container is gone. Note: the bypass dead-man timer does NOT re-run `Install` — it calls the cheap `ebpf.Manager.Enable` path (clears bypass flag only). Full re-enroll happens only on the explicit `FirewallEnable` RPC. |
| `FirewallDisable(container_id)` | per-container | Set BPF bypass for the container. Falls back to stored `cgroup_id` when Docker reports the container gone; no-op for unknown containers (both paths reach `ebpf.Manager.Disable`). |
| `FirewallBypass(container_id, timeout)` | per-container | `FirewallDisable` + `time.AfterFunc` that calls drift-guarded `Enable` on expiry (`bypassTimerFired` → `resolveBypassCgroupID` → `ebpf.Manager.Enable`). Caps at `maxBypassTimeout = 1h`. Stores `storedCgroupID[cid]` so mid-bypass Disable on a now-gone container can still clear the orphan bypass_map entry. |
| `FirewallAddRules` | global | `addRulesToStore` — all-or-nothing validation via `ValidateDst`, `NormalizeAndDedup`, `store.Set` + `store.Write`; then `Stack.Reload` + `ebpf.Manager.SyncRoutes` to hot-reload Envoy/CoreDNS + BPF route_map. |
| `FirewallRemoveRules` | global | `removeRulesFromStore` — matches by `RuleKey`, reloads. |
| `FirewallListRules` | global | Read-only normalized rule dump from the store. |
| `FirewallStatus` | global | `Stack.Status` — per-container up state, Envoy/CoreDNS IPs, network ID, rule count. Network-discovery errors log at Warn and leave topology empty; per-container `isRunning` is authoritative for "stack down". |
| `FirewallReload` | global | Regenerate configs and restart the stack without rule mutation. |
| `FirewallRotateCA` | global | Regenerate MITM CA + per-domain certs and `Stack.Reload`. |
| `FirewallSyncRoutes` | global | Break-glass route re-sync — rebuilds BPF `route_map` from store. |
| `FirewallResolveHostname` | global | DNS lookup from CP netns (used by container enroll for `host.docker.internal` resolution). |

## Types

### `Handler` + `HandlerDeps`

```go
type HandlerDeps struct {
    EBPF       ebpf.EBPFManager       // required — every RPC hits it
    Stack      StackLifecycle         // optional — stack-up/down RPCs no-op if nil
    Store      *storage.Store[EgressRulesFile] // optional — rules RPCs no-op if nil
    Cfg        config.Config          // optional — read for rule defaults, CPIPLastOctet, etc.
    Resolver   ContainerResolver      // required — per-container RPCs
    Log        *logger.Logger         // optional — defaults to Nop
    CertDirFn  func() (string, error) // optional — certs path for RotateCA
}

func NewHandler(deps HandlerDeps) *Handler  // panics on missing EBPF or Resolver
```

### `Stack`

```go
type Stack struct { /* docker.Client, config.Config, logger, Store */ }

func NewStack(dc *docker.Client, cfg config.Config, log *logger.Logger, store *storage.Store[EgressRulesFile]) *Stack
func (s *Stack) EnsureRunning(ctx) error
func (s *Stack) Stop(ctx) error
func (s *Stack) Reload(ctx) error
func (s *Stack) WaitForHealthy(ctx) error
func (s *Stack) Status(ctx) (*Status, error)
func (s *Stack) EnvoyIP() string
func (s *Stack) CoreDNSIP() string
func (s *Stack) NetworkID() string
func (s *Stack) CIDR() string
```

`StackLifecycle` is the Handler-facing interface — `*Stack` satisfies it. Keep Handler unit-testable by passing a Stack fake.

### `ContainerResolver`

```go
type ContainerResolver func(ctx context.Context, ref string) (id, cgroupPath string, exists bool, err error)
```

- `exists=false` + `err=nil` is the "container gone" signal — drives `FirewallEnable`'s `FailedPrecondition` and `FirewallDisable`'s stored-cgroup fallback.
- Production wiring: `cmd/clawker-cp/main.go::containerResolverFromDocker` uses `*docker.Client` + `IsCanonicalContainerID` so short-ref NotFound doesn't silently drop enforcement state.

### `EgressRulesFile` + rule helpers

`EgressRulesFile` is the on-disk schema (`egress-rules.yaml`) — it implements `storage.Schema` via `Fields()` so the store engine can read field metadata. `ProjectRules(cfg)` builds the ruleset from `cfg.Project().Security.Firewall` + `cfg.RequiredFirewallDomains()` — consumed by `BootstrapServicesPostStart` before issuing `FirewallAddRules`.

Rule helpers are exported for reuse by `BootstrapServicesPostStart` and E2E tests:

- `ValidateDst(dst string) error` — domain syntax + wildcard rules + length
- `NormalizeRule(r)` — lowercase dst, trim leading `*.`, etc.
- `RuleKey(r) string` — dedup key (`dst:proto:port`)
- `NormalizeAndDedup(rules) ([]EgressRule, []string)` — canonical form + dropped-duplicate notes
- `ProtoRulesToConfig(in)` / `ConfigRulesToProto(in)` — wire ↔ config translation for the `BootstrapServicesPostStart` flow

## Invariants

- **INV-B2-007 drain ordering**: `CancelAllBypassTimers` → `Stack.Stop` → `ebpf.Manager.FlushAll`. See `../CLAUDE.md` for drain callback composition in `cmd/clawker-cp/main.go`.
- **INV-B2-009 uniform scope**: every RPC in `AdminMethodScopes` maps to `"admin"`. `TestAdminMethodScopes_CoversAllRPCs` reflects over `AdminService_ServiceDesc` so a new RPC without a scope entry fails the build.
- **INV-B2-013 defensive startup cleanup**: `ebpf.Manager.CleanupStaleBypass` runs before `orchestrator.SetReady()`. Any error here fails startup (by design — a broken drain should not silently bless stale BPF state).
- **INV-B2-016 drift guard**: `FirewallEnable` always resolves `container_id → cgroup_path` via Docker, logs warning on stored-vs-fresh `cgroup_id` delta, returns `FailedPrecondition` if Docker says the container is gone. Bypass dead-man timer goes through the same `resolveBypassCgroupID` helper.
- **Domain hash contract is shared across three packages**: this package's `normalizeDomain` (string normalization — lowercase, strip trailing dot, strip leading `*.`) feeds `internal/controlplane/firewall/ebpf.DomainHash` (FNV-1a hash), which is also called from `internal/dnsbpf` so CoreDNS writes into the same `dns_cache` / `route_map` keyspace. Changing either the normalization or the hash requires all three call sites + clearing the pinned `route_map`.
- **Static IPs**: Envoy/CoreDNS/CP use `ComputeStaticIP(gateway, cfg.EnvoyIPLastOctet()/CoreDNSIPLastOctet()/CPIPLastOctet())`. Static-IP assignment cannot go through whail's `EnsureNetwork` helper — use `dc.EnsureNetwork` first, then explicit `NetworkingConfig.IPAMConfig.IPv4Address` in `ContainerCreate`.

## Imports

- **Uses**: `internal/config`, `internal/consts`, `internal/docker`, `internal/logger`, `internal/storage`, `internal/controlplane/firewall/ebpf`, `api/admin/v1`, `pkg/whail` (labels only), `github.com/moby/moby/api/types/*`.
- **Used by**: `internal/controlplane` (composite server embeds `*Handler`; startup wires `Stack`); `cmd/clawker-cp/main.go` (Handler ctor + container resolver); `internal/cmd/container/shared/container_start.go` (`BootstrapServicesPostStart` calls `ProjectRules` + `ProtoRulesToConfig`/`ConfigRulesToProto`).
- **Not imported by**: CLI commands — those go through `f.AdminClient(ctx)` which speaks gRPC to the running CP. No direct Go calls into `firewall.Handler` from CLI code.

## Test Patterns

- **Unit tests (`handler_test.go`, `stack_test.go`, `cgroup_test.go`)** — use `docker/mocks.FakeClient` + `controlplane/firewall/ebpf/mocks.EBPFManagerMock`. Handler fakes satisfy `StackLifecycle`; test-only `ContainerResolver` closures drive drift + not-found branches.
- **FakeClient managed-label jail**: `whail.ContainerInspect` re-invokes `ContainerInspectFn` inside `IsContainerManaged` — test fakes must return `Config.Labels[managedKey]=ManagedLabelValue` in inspect responses, otherwise real callers see `ErrContainerNotFound`.
- **Stop/Reload no-op tests** need affirmative assertions (`NotContains(fake.Calls, "ContainerStop")`, `FileExists(envoy.yaml)`) or they pass trivially without exercising the short-circuit.
- **Golden files**: `testdata/corefile_basic.golden` is hand-edited to update (no `GOLDEN_UPDATE=1` hook).
- **E2E tests**: `test/e2e/firewall_test.go` (composite flows through the CLI — blocked domain, allowed domain, add/remove rules, status, path rules, bypass end-to-end including natural-expiry + gone-container error paths) and `test/e2e/controlplane_cli_test.go` (break-glass `controlplane up/status/down` verbs). E2E means through `harness.Run(...)` — no direct `Stack`/`Handler` construction belongs under `test/e2e/`.

## Gotchas

- `APIClient.ImagePull` / `ImageBuild` only return a top-level error on initial HTTP failure; auth/manifest/layer errors stream as JSON frames with an `error` field. Always drain via `drainPullStream`/`drainBuildStream` and surface `msg.Error`.
- `cerrdefs.IsNotFound` does NOT match whail's `*DockerError{Op: "network_find"}` wrapping. Substring-match on `"not found"` false-positives (`"image not found"`, `"endpoint not found"`). In Status, log network-discovery errors at Warn and leave topology fields empty — per-container `isRunning` distinguishes "stack down" from "Docker unreachable".
- `HandlerDeps.Stack` being nil silently turns stack-up/down RPCs into no-ops. Intentional for unit tests, but a production wiring bug would hide here — `cmd/clawker-cp/main.go` must always wire a real `*Stack`.

## See Also

- `../CLAUDE.md` — CP core (Ory auth, startup sequencing, container config, drain callback composition)
- `ebpf/CLAUDE.md` — eBPF subsystem details + pinned map contract
- `.claude/rules/envoy.md` — Envoy config rules + verification workflow
- `.correctless/specs/cp-initiative/branch-2-cp-owns-firewall.md` — migration spec + invariants
