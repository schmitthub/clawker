# Firewall Package

> **Transitional state (CP Initiative Branch 2).** Task 1 relocated envoy/coredns/certs/rules/network/coredns-embed + admin handler + eBPF subsystem into `internal/controlplane/firewall/` and moved the CP + ebpf-manager embeds + their binaries into `internal/controlplane/`. This package now only holds `firewall.go` (interface + `FirewallStatus`), `manager.go`, `daemon.go`, `manager_network.go` (temporary raw-moby Network helpers), and the legacy `mocks/`. The package is scheduled for deletion in Task 6/8.

Domain contracts, Docker implementation, daemon, certificates, and network management for the clawker firewall stack: Envoy (egress proxy) + custom CoreDNS (DNS resolver with BPF plugin) + clawker-controlplane (control plane with eBPF + Ory auth).

## Control plane integration

The firewall manager communicates with the CP via typed gRPC over mTLS TCP with OAuth2 JWT authorization. The old `docker exec ebpf-manager <subcommand>` pattern is gone — the CP owns `ebpf.Manager.Load()` in-process and serves `AdminService` RPCs.

- `Manager` holds a lazy `adminv1.AdminServiceClient` built on first use via `internal/auth.DialCPAdmin()` (mTLS + JWT token exchange). The client is cached and reused.
- `waitForCPReady()` polls the CP's `/healthz` endpoint (HTTP on `127.0.0.1:<HealthPort>`). The CP's healthz is an aggregate probe — it actively checks all 7 service ports (Hydra public/admin, Kratos public/admin, Oathkeeper proxy/API, gRPC admin) before returning 200.
- CP container config is delegated to `controlplane.BuildCPContainerConfig(cfg)` — the firewall manager maps it to an internal `containerSpec` and adds network attachment + static IP.

See `internal/controlplane/CLAUDE.md` for the full auth architecture.

<critical>
## Envoy References
See `.claude/rules/envoy.md` for all Envoy references, documentation links, and configuration guidelines.
That rule is auto-loaded when touching `envoy.go`, `envoy_test.go`, or `manager.go`.
</critical>

## Contents (post-Task-1)

| File | Purpose |
|------|---------|
| `firewall.go` | `FirewallManager` interface + `FirewallStatus` (sentinels + `HealthTimeoutError` moved to `internal/controlplane/firewall/errors.go`) |
| `manager.go` | `Manager` — Docker implementation of `FirewallManager`; manages 3-container stack; owns lazy gRPC client to CP. Moved helpers (envoy/coredns/certs/rules/network types) are imported via alias `fwcp "github.com/schmitthub/clawker/internal/controlplane/firewall"` |
| `manager_network.go` | Temporary raw-moby `(m *Manager).discoverNetwork` / `ensureNetwork` returning `fwcp.NetworkInfo` — removed with the Manager in Task 6/8 |
| `daemon.go` | Background daemon — health check loop (5s) + container watcher loop (30s); restart logic; BPF cleanup on shutdown |
| `mocks/manager_mock.go` | `FirewallManagerMock` — moq-generated test double |

### Relocated (see `internal/controlplane/firewall/`)

| Was here | Now at |
|----------|--------|
| `envoy.go` | `internal/controlplane/firewall/envoy_config.go` |
| `coredns.go` | `internal/controlplane/firewall/coredns_config.go` |
| `certs.go` | `internal/controlplane/firewall/certs.go` |
| `rules.go` + `types.go` | `internal/controlplane/firewall/rules_store.go` |
| `network.go` (pure package fns only — raw-moby Manager methods stayed behind as `manager_network.go`) | `internal/controlplane/firewall/network.go` |
| `coredns_embed.go` | `internal/controlplane/firewall/embed_coredns.go` (binary at `internal/controlplane/firewall/assets/coredns-clawker`) |
| `cp_embed.go` | `internal/controlplane/embed_cp.go` (binary at `internal/controlplane/assets/clawker-cp`) |
| `ebpf_embed.go` | `internal/controlplane/embed_ebpf.go` (binary at `internal/controlplane/assets/ebpf-manager`) |

## Interface

```go
type FirewallManager interface {
    // Stack lifecycle
    EnsureRunning(ctx context.Context) error
    Stop(ctx context.Context) error
    IsRunning(ctx context.Context) bool
    WaitForHealthy(ctx context.Context) error

    // Rule management
    AddRules(ctx context.Context, rules []config.EgressRule) error
    RemoveRules(ctx context.Context, rules []config.EgressRule) error
    Reload(ctx context.Context) error
    List(ctx context.Context) ([]config.EgressRule, error)

    // Per-container enforcement
    InstallFirewall(ctx context.Context, containerID string) error
    Enable(ctx context.Context, containerID string) error
    Disable(ctx context.Context, containerID string) error
    Bypass(ctx context.Context, containerID string, timeout time.Duration) error

    // Status and introspection
    Status(ctx context.Context) (*FirewallStatus, error)
    EnvoyIP() string
    CoreDNSIP() string
    NetCIDR() string
}
```

### Enable / Disable / InstallFirewall split

Three distinct per-container operations:

- **`InstallFirewall(containerID)`** — full enrollment. Called at container start. Ensures auth material + CP image, discovers network, ensures CP running + ready, syncs global `route_map`, resolves host.docker.internal, calls CP `Install` RPC (attaches BPF programs to container's cgroup), touches signal file to unblock entrypoint.
- **`Enable(containerID)`** — clears the eBPF bypass flag. Lightweight. Calls CP `Enable` RPC.
- **`Disable(containerID)`** — sets the eBPF bypass flag. Lightweight. Calls CP `Disable` RPC.
- **`Bypass(containerID, timeout)`** — sets bypass flag with server-side dead-man timer. Calls CP `Bypass` RPC. The CP auto-restores enforcement after the timeout even if the CLI crashes.

## Types

```go
type HealthTimeoutError struct {
    Timeout time.Duration
    Err     error
}

type FirewallStatus struct {
    Running       bool
    EnvoyHealth   bool
    CoreDNSHealth bool
    RuleCount     int
    EnvoyIP       string
    CoreDNSIP     string
    NetworkID     string
}
```

Sentinel errors: `ErrEnvoyUnhealthy`, `ErrCoreDNSUnhealthy`, `ErrCPUnhealthy`.

## Manager (`manager.go`)

Docker implementation of `FirewallManager`. Creates and manages three shared-infrastructure containers on an isolated Docker network with static IPs: Envoy (`clawker-envoy`, .200), CoreDNS (`clawker-coredns`, .201), and CP (`clawker-controlplane`, .202). These are **not** sidecars — one firewall stack is shared by all clawker-managed containers on the host (1:N).

```go
func NewManager(client client.APIClient, cfg config.Config, log *logger.Logger) (*Manager, error)
```

`Manager` holds a `client.APIClient` (raw moby — **not** whail, to avoid label filtering), `config.Config`, `*logger.Logger`, and a `*storage.Store[EgressRulesFile]`.

### Test seams

- `cgroupDriverFn` — override cgroup driver detection
- `touchSignalFileFn` — override signal file creation
- `waitForCPReadyFn` — override CP readiness polling
- `adminClientFn` — override gRPC client (inject mock)

### Lifecycle ordering (critical invariants)

The `dnsbpf` plugin opens the pinned `dns_cache` BPF map on CoreDNS startup — the map **must** exist first. That drives the ordering:

**`EnsureRunning`**: network → discover IPs → configs (Corefile, envoy.yaml, certs) → auth material → build images → start CP → wait for CP ready (polls `/healthz`) → sync routes → start Envoy → start CoreDNS → WaitForHealthy.

**`regenerateAndRestart`** (Reload/AddRules/RemoveRules): configs → early-return if not running → sync routes (CP gRPC `SyncRoutes` RPC) → restart Envoy + CoreDNS → WaitForHealthy.

**`InstallFirewall(containerID)`**: ensure auth + CP image → discover network → ensure CP running + ready → sync routes → resolve host.docker.internal → CP `Install` RPC → touch signal file.

### Global `route_map`

The BPF `route_map` key is `{domain_hash, dst_port}` — **global**, not per-container. Presence in `container_map` is what marks a cgroup as "clawker-enforced". `firewall add/remove/reload` propagates to all containers via one `SyncRoutes` RPC.

### WaitForHealthy

Polls three endpoints every 500ms using `127.0.0.1` (not `localhost` — avoids IPv6 resolution):
- Envoy: `http://127.0.0.1:<EnvoyHealthHostPort>/`
- CoreDNS: `http://127.0.0.1:<CoreDNSHealthHostPort>/health`
- CP: `http://127.0.0.1:<CPHealthPort>/healthz`

Returns `HealthTimeoutError` with joined errors if any probe fails.

## Daemon (`daemon.go`)

Background process with dual-loop architecture.

```go
func NewDaemon(cfg config.Config, log *logger.Logger, opts ...DaemonOption) (*Daemon, error)
func EnsureDaemon(cfg config.Config, log *logger.Logger) error
```

### Health check loop (5s interval)

- Probes: Envoy (TCP 127.0.0.1:18901), CoreDNS (HTTP 127.0.0.1:18902/health), CP (HTTP 127.0.0.1:<HealthPort>/healthz)
- `maxHealthCheckFailures = 3` — consecutive failures before restart attempt
- `maxHealthRestartAttempts = 3` — restart attempts via `EnsureRunning` before exit
- On restart failure: exits with error (does NOT cleanup BPF state)

### Container watcher loop (30s interval, 60s grace period)

- Counts running agent containers (filter: `managed=true`, `purpose=agent`)
- `missedCheckThreshold = 2` — exit after 2 consecutive "no containers" polls
- When exiting due to no containers: sets `noAgentContainers = true`

### BPF cleanup on shutdown

**Only** runs when `noAgentContainers == true` (watcher exit path). Calls `ebpf.Manager.CleanupAllLinks()` to remove all pinned `/sys/fs/bpf/clawker/link_*` files. In all other shutdown paths (signal, health failure, context cancel), BPF state persists for the next daemon startup.

### EnsureDaemon

1. Check PID file for live process
2. If alive + healthy: no-op
3. If alive + unhealthy: kill, then spawn fresh daemon
4. If not alive: spawn new daemon
5. Returns immediately (does not wait for health)

## Certificate Management (`certs.go`)

Self-signed CA and per-domain TLS certificates for Envoy MITM inspection.

```go
func EnsureCA(certDir string) (*x509.Certificate, *ecdsa.PrivateKey, error)
func GenerateDomainCert(caCert, caKey, domain) (certPEM, keyPEM []byte, err error)
func RegenerateDomainCerts(rules, certDir, caCert, caKey) error
func RotateCA(certDir string, rules []config.EgressRule) error
```

- CA: ECDSA P-256, 10yr validity, "Clawker Firewall CA"
- Domain certs: ECDSA P-256, 1yr validity, wildcard domains get apex + `*.domain` SANs
- Only TLS rules generate certs (TCP/SSH/HTTP/IP/CIDR skipped)

## Rules Store (`rules.go`)

```go
func NewRulesStore(cfg config.Config) (*storage.Store[EgressRulesFile], error)
```

Cross-process-safe store backed by `egress-rules.yaml` with flock. AddRules validates all destinations (all-or-nothing), normalizes, deduplicates by `(dst, proto, port, action)`, then hot-reloads via `regenerateAndRestart`.

## Config Generators

### CoreDNS (`coredns.go`)

```go
func GenerateCorefile(rules []config.EgressRule, healthPort int) ([]byte, error)
```

- Per-domain forward zones with `dnsbpf` plugin directive (before `forward`)
- Docker internal forward zones (docker.internal, otel-collector, etc.) delegate to `127.0.0.11`
- Every zone includes `template IN AAAA . { rcode NOERROR }` (NODATA) before `forward` so AAAA queries return empty. The eBPF `connect6` hook blocks non-IPv4-mapped IPv6, and some clients (npm/node) treat `EPERM` from `connect6()` as permanent and don't fall back to A. Returning NODATA steers them to A upfront. The catch-all `.` zone already NXDOMAINs AAAA via `template IN ANY`.
- Catch-all `.` zone: `template IN ANY . { rcode NXDOMAIN }` + health endpoint + reload

### Envoy (`envoy.go`)

```go
func GenerateEnvoyConfig(rules []config.EgressRule, ports EnvoyPorts) ([]byte, []string, error)
```

- Single egress listener with `tls_inspector`: TLS filter chains (SNI match → per-domain LOGICAL_DNS cluster with upstream re-encryption) + HTTP filter chain (Host header routing) + deny catch-all
- LOGICAL_DNS cluster architecture prevents confused deputy attacks
- WebSocket support via ALPN override for TLS, simple upgrade_configs for HTTP
- TCP/SSH listeners on sequential ports from `TCPPortBase`
- JSON access logging to stdout for Promtail/Loki pipeline

## Custom images

All three images are built on-demand from `//go:embed`'d Linux-static binaries:

- **clawker-controlplane**: Multi-stage Dockerfile — Ory binaries (Hydra, Oathkeeper, Kratos) + clawker-cp + ebpf-manager on distroless base
- **clawker-coredns**: Alpine + custom CoreDNS binary with dnsbpf plugin, `CAP_BPF + CAP_SYS_ADMIN`, `/sys/fs/bpf` mount
- **ebpf-manager**: Break-glass only (bundled in CP image)

## Relationships

- **`internal/config`** — `EgressRule` types. Firewall imports config; config does NOT import firewall.
- **`internal/storage`** — `Store[EgressRulesFile]` for rule persistence.
- **`internal/auth`** — `DialCPAdmin()` for mTLS + JWT gRPC client construction.
- **`internal/controlplane`** — `BuildCPContainerConfig()` for CP container config. `controlplane/ebpf.DomainHash` for route_map keys.
- **`api/admin/v1`** — protobuf types for CP gRPC (Install, Remove, Enable, Disable, Bypass, SyncRoutes, ResolveHostname).
- **`github.com/moby/moby/client`** — raw moby `APIClient` (not whail, avoids label filtering).
- **`internal/cmd/factory`** — Factory exposes `f.Firewall()` as a lazy noun returning `FirewallManager`.
- **`internal/logger`** — `Manager` and `Daemon` accept `*logger.Logger` in constructors.

## Test Double (`mocks/`)

`FirewallManagerMock` is moq-generated. Regenerate after interface changes:

```bash
cd internal/firewall && go generate ./...
```
