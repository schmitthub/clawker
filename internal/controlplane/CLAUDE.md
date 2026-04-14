# Control Plane Package

The clawker control plane. A containerized, privileged, long-lived Go service that owns authoritative state for managed containers. Runs as `clawker-controlplane` in the firewall stack, replacing the historical `clawker-ebpf` container.

## Responsibilities (v1)

1. **Authoritative eBPF management** — the CP owns `ebpf.Manager.Load()` lifetime for its process lifetime. BPF programs are loaded once at boot and stay live.
2. **AdminService gRPC surface** — the host CLI calls firewall/eBPF operations as typed gRPC over mTLS TCP with OAuth2 JWT authorization.
3. **Ory auth stack** — Hydra (OAuth2), Oathkeeper (reverse proxy), Kratos (identity, placeholder for webui).
4. **Aggregate health reporting** — `/healthz` actively probes all 7 service ports before returning 200.

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
| `server.go` | `Server` struct, `ControlPlaneService` interface, `Registry`, `AgentReportingService` handler |
| `registry.go` | Thread-safe agent registry keyed by container ID |
| `embed_cp.go` / `embed_ebpf.go` | `//go:embed assets/clawker-cp` + `assets/ebpf-manager` — CP daemon + break-glass eBPF CLI binaries embedded into the clawker release |
| `authz.go` | `AuthInterceptor` — validates OAuth2 bearer tokens via Hydra introspection, enforces per-method scopes |
| `hydra_client.go` | `RegisterCLIClient` — registers clawker-cli OAuth2 client with Hydra at startup. `AdminMethodScopes` lives in `api/admin/v1/admin.go` so a new RPC fails closed (covered by `TestAdminMethodScopes_CoversAllRPCs`). |
| `startup.go` | `CPStartupOrchestrator` — startup sequencing + aggregate `/healthz` endpoint (probes all 7 service ports) |
| `bootstrap.go` | Host-side `EnsureRunning` + `Stop` — manage the CP container lifecycle via `*docker.Client` |
| `watcher.go` | `AgentWatcher` — polls Docker for `purpose=agent` containers; invokes drain-to-zero callback past grace/threshold (INV-B2-007) |
| `cp_container.go` | `BuildCPContainerConfig(cfg)` → `CPContainerConfig` struct for Docker container creation |
| `ory_configs.go` | `WriteOryConfigs(cp)` — generates Hydra/Kratos/Oathkeeper YAML config files |
| `subprocess.go` | `SubprocessManager` — manages Ory subprocess lifecycle (start, health, crash detection, shutdown) |
| `mocks/mock_server.go` | `MockServer` — hand-written test double for `ControlPlaneService` |
| `mocks/` | moq-generated mocks: `ControlPlaneServiceMock`, `IntrospectorMock`, `EBPFManagerMock` |

## AdminService composition

`server.go` exposes the unexported `adminServer` type that embeds `*fwhandler.Handler` (and, in future branches, additional domain handlers). Method promotion produces the AdminServiceServer surface; `NewAdminServer(fw)` is the wiring point used by `cmd/clawker-cp/main.go`.

The 13 firewall RPCs live in `internal/controlplane/firewall/handler.go` — see `internal/controlplane/firewall/CLAUDE.md` for the per-RPC table. Future domains (Monitor, Hostproxy, Clawkerd) embed alongside; the `<Domain><Action>[<Object>]` proto naming convention prevents method-name collisions.

All RPCs require the uniform `admin` scope (INV-B2-009). Per-method diversification is intentionally not used — see Spec §8.

## Startup Sequence (`cmd/clawker-cp/main.go`)

1. Write Ory config files (`WriteOryConfigs(cp)`)
2. Start Kratos + Hydra subprocesses, wait healthy
3. Wait for Hydra admin port healthy, configure service probes (`orchestrator.SetServiceProbes(cp, tlsCfg)`)
4. Read CLI public JWK from bind-mount
5. Register CLI client with Hydra (`RegisterCLIClient`)
6. Start Oathkeeper subprocess; build `*docker.Client`; build `firewall.Stack` (via `fwhandler.NewRulesStore`)
7. Load eBPF programs (`ebpfMgr.Load()`); run defensive startup cleanup (`ebpfMgr.CleanupStaleBypass()` — INV-B2-013)
8. Start gRPC AdminService with mTLS (`RequireAndVerifyClientCert` + CA pool) + AuthInterceptor
9. Mark ready (`orchestrator.SetReady()`), serve `/healthz` on HealthPort
9b. Start `controlplane.AgentWatcher` goroutine — polls Docker for agents with `purpose=agent`; on drain-to-zero invokes callback that cancels bypass timers → `grpcServer.GracefulStop()` → `firewall.Stack.Stop()` → `ebpfMgr.FlushAll()` (INV-B2-007), then the outer shutdown path tears the CP container down (exit code 0 — the `on-failure` restart policy does NOT retrigger)

## Aggregate Health (`startup.go`)

`CPStartupOrchestrator` manages the `/healthz` endpoint:

- **Before ready**: returns 503
- **After ready**: actively probes all 7 service ports on every request:
  - Hydra public (TLS), Hydra admin (TLS)
  - Kratos public (TLS), Kratos admin (TLS)
  - Oathkeeper proxy (TLS), Oathkeeper API (TLS)
  - gRPC admin (raw TCP)
- Returns 200 only when ALL probes succeed

## Container Config (`cp_container.go`)

```go
func BuildCPContainerConfig(cfg config.Config) (*CPContainerConfig, error)
```

All ports from `cfg.Settings().ControlPlane` (defaults via struct tags). Published to `127.0.0.1` only:

| Published Port | Purpose |
|----------------|---------|
| AdminPort (7443) | gRPC AdminService |
| HydraPublicPort (4444) | OAuth2 token endpoint |
| OathkeeperPort (4456) | HTTP reverse proxy (future webui) |
| HealthPort (7080) | /healthz endpoint |

**Not published**: Hydra admin (4445), Kratos ports, Oathkeeper API — internal-only (`127.0.0.1` bind inside container).

**Key mounts**: config dir (RO), CA cert (RO), CLI public JWK (RO), server TLS cert+key (RO), /sys/fs/cgroup (RO), /sys/fs/bpf (RW), logs dir.

**Invariant INV-B1-006**: CLI private signing key is NEVER mounted into the container.

**Capabilities**: `BPF`, `SYS_ADMIN` (for eBPF program attachment).

## eBPF Subsystem (`ebpf/`)

### Manager lifecycle

```go
func NewManager(log *logger.Logger) *Manager
func (m *Manager) Load() error       // Called once at CP startup
func (m *Manager) OpenPinned() error  // Break-glass mode (reads existing pins)
func (m *Manager) Close() error
```

`Load()` creates pin directory, calls `cleanupStaleLinks()`, loads ELF, pins maps/programs at `/sys/fs/bpf/clawker/`.

### EBPFManager interface

```go
type EBPFManager interface {
    Install(cgroupID uint64, cgroupPath string, cfg BPFContainerConfig) error
    Remove(cgroupID uint64) error
    Enable(cgroupID uint64) error
    Disable(cgroupID uint64) error
    SyncRoutes(routes []Route) error
}
```

### Link cleanup (critical invariant)

- **`cleanupStaleLinks()`** — called during `Load()`. Parses cgroup IDs from link pin filenames (`link_{prog}_{cgroupID}`), checks each against `container_map`. Removes links for dead cgroups, preserves links for live cgroups. This ensures enforcement survives CP restarts.
- **`cleanupLinks(cgroupID)`** — removes stale links for ONE cgroup. Called by `Install` before attaching fresh programs.
- **`CleanupAllLinks()`** — removes ALL pinned links. Called ONLY by the firewall daemon on shutdown when no agent containers remain.

### Shared types (`ebpf/types.go`)

`PinPath`, `ContainerConfig`, `DNSEntry`, `RouteKey/Val`, `MetricKey`, `Action` constants, helper functions (`IPToUint32`, `DomainHash` FNV-1a, `CgroupPath`, `CgroupID` with path injection validation, `Supported`).

## Ory Config Generation (`ory_configs.go`)

```go
func WriteOryConfigs(cp config.ControlPlaneSettings) error
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

- `ControlPlaneService` interface — CLI consumers depend on this. `mocks.MockServer` avoids real gRPC.
- `EBPFManager` interface — `ebpf/mocks/EBPFManagerMock` for admin handler tests.
- `Introspector` interface — `mocks/IntrospectorMock` for authz tests (no real Hydra).
- `AdminHandler.resolveHostFn` — injectable DNS resolver.

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

**Uses**: `internal/config`, `internal/consts`, `internal/logger`, `internal/controlplane/ebpf`, `api/admin/v1`, `internal/clawkerd/protocol/v1`, `google.golang.org/grpc`, `github.com/cilium/ebpf`, `github.com/moby/moby/api/types/{mount,network}`.

**Used by**: `internal/firewall` (BuildCPContainerConfig, ebpf.DomainHash), `cmd/clawker-cp/` (startup sequence), `internal/dnsbpf` (ebpf types), `internal/auth` (cert paths).

No circular dependencies.

## What's deferred

- **TCP listener for agents** — v1 serves gRPC on localhost only. Adding agent TCP listener is pure addition.
- **Kratos active usage** — running as subprocess placeholder. Lights up with webui.
- **Oathkeeper active routing** — running with empty rules. Lights up with webui HTTP auth.
- **Per-method scopes beyond `admin`** — finer-grained scopes (`agent:register`, `webui:read`) would add entries to `AdminMethodScopes()` in `api/admin/v1/admin.go`. INV-B2-009 currently mandates a uniform `admin` scope across all firewall methods.
