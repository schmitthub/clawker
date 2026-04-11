# Firewall Package

Domain contracts, Docker implementation, daemon, certificates, and network management for the eBPF + custom CoreDNS + Envoy firewall stack.

<critical>
## Envoy References
See `.claude/rules/envoy.md` for all Envoy references, documentation links, and configuration guidelines.
That rule is auto-loaded when touching `envoy.go`, `envoy_test.go`, or `manager.go`.
</critical>

## Contents

| File | Purpose |
|------|---------|
| `firewall.go` | `FirewallManager` interface, `FirewallStatus`, `HealthTimeoutError`, sentinel errors |
| `types.go` | `EgressRulesFile` — top-level document for `storage.Store[T]` |
| `coredns.go` | `GenerateCorefile(rules)` — CoreDNS Corefile from egress rules (adds `dnsbpf` directive + query logging) |
| `envoy.go` | `GenerateEnvoyConfig(rules)` — Envoy bootstrap YAML from egress rules (with access logging) |
| `certs.go` | CA and per-domain TLS certificate management for TLS inspection/termination |
| `daemon.go` | Background daemon process — health probes + container watcher |
| `manager.go` | `Manager` — Docker implementation of `FirewallManager`; also builds the custom CoreDNS + eBPF-manager images from embedded binaries |
| `network.go` | `NetworkInfo`, Docker network creation, static IP computation |
| `rules.go` | `NewRulesStore(cfg)` — `storage.Store[EgressRulesFile]` factory |
| `ebpf_embed.go` | `//go:embed assets/ebpf-manager` — Linux-static eBPF manager binary |
| `coredns_embed.go` | `//go:embed assets/coredns-clawker` — Linux-static custom CoreDNS binary with dnsbpf plugin |
| `mocks/manager_mock.go` | `FirewallManagerMock` — moq-generated test double |

## Interface

```go
type FirewallManager interface {
    EnsureRunning(ctx context.Context) error
    Stop(ctx context.Context) error
    IsRunning(ctx context.Context) bool
    WaitForHealthy(ctx context.Context) error
    AddRules(ctx context.Context, rules []config.EgressRule) error
    RemoveRules(ctx context.Context, rules []config.EgressRule) error
    Reload(ctx context.Context) error
    List(ctx context.Context) ([]config.EgressRule, error)
    Disable(ctx context.Context, containerID string) error
    Enable(ctx context.Context, containerID string) error
    Bypass(ctx context.Context, containerID string, timeout time.Duration) error
    Status(ctx context.Context) (*FirewallStatus, error)
    EnvoyIP() string
    CoreDNSIP() string
    NetCIDR() string
}
```

## Types

```go
// HealthTimeoutError wraps sentinel errors (ErrEnvoyUnhealthy, ErrCoreDNSUnhealthy)
// when health probes exceed the caller's context deadline.
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

Sentinel errors: `ErrEnvoyUnhealthy`, `ErrCoreDNSUnhealthy`.

## Manager (`manager.go`)

Docker implementation of `FirewallManager`. Creates and manages the clawker firewall stack — three shared-infrastructure containers on an isolated Docker network with static IPs: the Envoy egress proxy, the custom CoreDNS resolver (`clawker-coredns:latest`), and the eBPF control-plane agent (`clawker-ebpf:latest`). These are **not** sidecars — they do not share network/PID/mount namespaces with user containers, and one firewall stack is shared by all clawker-managed containers on the host (1:N). The eBPF programs are attached to user container cgroups via `bpf_prog_attach` from inside `clawker-ebpf`; once pinned at `/sys/fs/bpf/clawker/` they persist in kernel state independently of the container that loaded them.

```go
func NewManager(client client.APIClient, cfg config.Config, log *logger.Logger) (*Manager, error)
```

`Manager` holds a `client.APIClient` (raw moby — **not** `docker.Client`/whail, to avoid jail label filtering), `config.Config`, `*logger.Logger`, and a `*storage.Store[EgressRulesFile]`. All `FirewallManager` methods are implemented as `*Manager` receivers.

Container name arguments use the `firewallContainer` typed constant (`envoyContainer`, `corednsContainer`, `ebpfContainer`) for type safety.

### Lifecycle ordering (critical invariants)

The `dnsbpf` plugin opens the pinned `dns_cache` BPF map on CoreDNS startup — the map **must** exist first. That drives the ordering for all three lifecycle paths:

- **`EnsureRunning`**: `ensureNetwork` → `discoverNetwork` (IPs/CIDR) → `ensureConfigs` (Corefile, envoy.yaml, certs) → `ensureEbpfImage` + `ensureCorednsImage` → `ensureContainer(ebpfContainer)` + `ebpfExec("init")` (creates the pinned map) → `syncRoutes` → `ensureContainer(envoyContainer)` + `ensureContainer(corednsContainer)` → `WaitForHealthy` (Envoy `:18901/`, CoreDNS `:18902/health`).
- **`regenerateAndRestart`** (used by `Reload`/`AddRules`/`RemoveRules`): `ensureConfigs` → early-return if stack is not running → `ebpfExec("init")` (idempotent re-pin so CoreDNS reload doesn't find a dangling map) → `syncRoutes` (live update of already-running containers) → `restartContainer(envoy)` + `restartContainer(coredns)` → `WaitForHealthy`.
- **`Enable(containerID)`**: `ensureEbpfImage` + `ensureContainer(ebpfContainer)` + `ebpfExec("init")` → `syncRoutes` (defense-in-depth after daemon restart) → `ebpfExec("resolve", "host.docker.internal")` if host-proxy is enabled → `ebpfExec("enable", cgroupPath, cfgJSON)` (writes `container_map[cgroup_id]` and attaches the six cgroup programs) → `touchSignalFile` to unblock the container's entrypoint.

### Global `route_map`

The BPF `route_map` key is `{domain_hash, dst_port}` — **global**, not per-container. Presence in `container_map` is what marks a cgroup as "clawker-enforced". This is why `firewall add/remove/reload` can propagate new rules to every running container via one `sync-routes` call, with no agent-container restarts.

Key internal methods: `ensureNetwork`, `ensureContainer`, `ensureConfigs`, `regenerateAndRestart`, `ensureEbpfImage`, `ensureCorednsImage`, `ensureEmbeddedImage`, `syncRoutes`, `ebpfExec`, `ebpfExecOutput`.

## Certificate Management (`certs.go`)

Self-signed CA and per-domain TLS certificates for Envoy TLS termination and inspection.

```go
func EnsureCA(certDir string) (*x509.Certificate, *ecdsa.PrivateKey, error)
func GenerateDomainCert(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, domain string) (certPEM []byte, keyPEM []byte, err error)
func RegenerateDomainCerts(rules []config.EgressRule, certDir string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) error
func RotateCA(certDir string, rules []config.EgressRule) error
```

- `EnsureCA` — creates or loads CA keypair (`ca-cert.pem`, `ca-key.pem`) in `dataDir`
- `GenerateDomainCert` — signs a per-domain certificate with the CA; returns PEM bytes. Wildcard domains (leading-dot convention) get both apex and `*.domain` SANs
- `RegenerateDomainCerts` — generates certs for all TLS egress rules; skips TCP/SSH/HTTP and IP/CIDR rules. Deduplicates by normalized domain, preserving wildcard SANs. Cleans stale cert files from previous runs
- `RotateCA` — regenerates the CA keypair and all domain certificates

## Daemon (`daemon.go`)

Background process with dual-loop architecture: health check loop (default 5s) + container watcher loop (default 30s). Stops the firewall stack when no clawker containers remain after a grace period.

```go
type DaemonOption func(*Daemon)

func WithHealthCheckInterval(d time.Duration) DaemonOption
func WithDaemonPollInterval(d time.Duration) DaemonOption
func WithDaemonGracePeriod(d time.Duration) DaemonOption

func NewDaemon(cfg config.Config, log *logger.Logger, opts ...DaemonOption) (*Daemon, error)
func EnsureDaemon(cfg config.Config, log *logger.Logger) error
func IsDaemonRunning(pidFile string) bool
func GetDaemonPID(pidFile string) int
func StopDaemon(pidFile string) error
```

- `NewDaemon` — creates daemon instance; `(*Daemon).Run(ctx)` starts both loops
- `EnsureDaemon` — checks if daemon is running, spawns if not; returns immediately
- `IsDaemonRunning` / `GetDaemonPID` / `StopDaemon` — PID file-based process management

## Rules Store (`rules.go`)

```go
func NewRulesStore(cfg config.Config) (*storage.Store[EgressRulesFile], error)
```

Creates a `storage.Store[EgressRulesFile]` backed by `egress-rules.yaml` in the firewall data subdirectory. Used by `Manager` for persistent rule state.

## Network (`network.go`)

```go
type NetworkInfo struct {
    NetworkID string
    EnvoyIP   string
    CoreDNSIP string
    CIDR      string
}
```

- `(*Manager).ensureNetwork` — creates the isolated Docker bridge network or discovers an existing one
- `(*Manager).discoverNetwork` — finds the existing firewall network by label
- `computeStaticIP(gateway, lastOctet)` — replaces the last octet of a gateway IP (e.g., `.1` -> `.2` for Envoy, `.3` for CoreDNS)

## Config Generators

### CoreDNS (`coredns.go`)

```go
func GenerateCorefile(rules []config.EgressRule, healthPort int) ([]byte, error)
```

- Only "allow" rules with domain destinations produce forward zones
- Each domain gets `forward . 1.1.1.2 1.0.0.2` (Cloudflare malware-blocking)
- Docker internal forward zones (`docker.internal`, `otel-collector`, `jaeger`, `prometheus`, `loki`, `grafana`) delegate to `127.0.0.11` (Docker's embedded DNS). CoreDNS is on `clawker-net` so its 127.0.0.11 resolves container names and `host.docker.internal`
- Catch-all `.` zone: `template IN ANY . { rcode NXDOMAIN }` + `health :<healthPort>` + `reload 2s` (the catch-all does NOT include `dnsbpf` — NXDOMAIN responses have no A records)
- IP/CIDR destinations and deny rules are excluded
- **`dnsbpf` plugin directive**: Both per-domain forward zones and internal host zones include the custom `dnsbpf` directive (before `forward`). This plugin is only available in the custom `clawker-coredns:latest` image — it intercepts every resolved A record and writes `IP → {domain_hash, expire_ts}` into the pinned `dns_cache` BPF map at `/sys/fs/bpf/clawker/dns_cache`. The hash is derived from the **zone name** (not the query name), so wildcard zones like `.example.com` write the wildcard's hash for every resolved subdomain, matching how `route_map` is keyed. The `host.docker.internal` path is intentionally populated too, so host-proxy traffic benefits from the same per-domain BPF routing
- **Query logging**: All zones include a `log` plugin with a format compatible with the Promtail regex pipeline (`source=coredns client_ip={remote} domain={name} qtype={type} rcode={rcode} duration={duration}`). Promtail parses these via a `regex` pipeline stage (not `logfmt`, because CoreDNS `{remote}` emits `IP:port` and lines have an `[INFO]` prefix) and ships to Loki for the Grafana egress dashboard

### Envoy (`envoy.go`)

```go
type EnvoyPorts struct {
    EgressPort, TCPPortBase, HealthPort int
}

func GenerateEnvoyConfig(rules []config.EgressRule, ports EnvoyPorts) ([]byte, []string, error)
```

- Returns YAML bytes + warnings (non-fatal issues like skipped IP/CIDR rules)
- `EnvoyPorts.Validate()` checks port ranges and detects collisions at entry
- **Egress listener** (single port, `tls_inspector`): handles both TLS and plaintext HTTP via filter chain matching:
  - TLS filter chains: matched by SNI (`server_names`), per-domain TLS termination + HTTP inspection + per-domain LOGICAL_DNS cluster with upstream re-encryption
  - HTTP filter chain: matched by `transport_protocol: "raw_buffer"`, Host header routing via virtual hosts, per-domain LOGICAL_DNS clusters (plaintext)
  - Deny filter chain: catch-all (`filter_chain_match: {}`), `tcp_proxy` → `deny_cluster` (connection reset)
- **LOGICAL_DNS cluster architecture**: Each domain gets its own LOGICAL_DNS cluster with the domain as the endpoint address. Upstream destination is determined by the cluster endpoint — NOT by the HTTP Host header. This prevents confused deputy attacks where a malicious client manipulates Host to redirect traffic. Port is hardcoded in the cluster endpoint from the rule's configured port
- **WebSocket support**: TLS chains use `upgrade_configs` with a custom filter chain: `set_filter_state` overrides `envoy.network.application_protocols` to `http/1.1`, forcing HTTP/1.1 upstream for WebSocket upgrades (HTTP/1.1 Upgrade mechanism doesn't exist in H2). HTTP chains use simple `upgrade_configs: [{upgrade_type: websocket}]` (no ALPN override needed for plaintext). Route-level `upgrade_configs` enable per-route control
- Cluster types: per-domain `tls_<domain>` (LOGICAL_DNS with upstream TLS re-encryption, auto_config H2+H1.1), per-domain `http_<domain>` (LOGICAL_DNS, plaintext), deny_cluster (STATIC, no endpoints)
- TCP/SSH listeners on sequential ports from `TCPPortBase`
- Wildcard domain support: `serverNames()` produces SNI suffix matches (`.domain`), `httpDomains()` produces Envoy Host wildcard patterns (`*.domain`). Per-listener `exactDomains` maps prevent cross-listener interference
- `virtualHostName()` prefixes wildcard virtual hosts with `wildcard_` to avoid name collisions with exact rules
- **Access logging**: All filter chains emit JSON access logs to stdout. TLS and HTTP filter chains use `buildHTTPAccessLog(proto)` (includes `method`, `path`, `response_code`), TCP/SSH and deny chains use `buildTCPAccessLog(proto)`. The deny chain logs with `proto="deny"`. Common fields: `timestamp`, `domain` (SNI), `upstream_host`, `client_ip`, `response_flags`, `bytes_sent`, `bytes_received`, `duration_ms`, `proto`, `source`. Promtail parses these via the `json` pipeline stage and ships to Loki for the Grafana egress dashboard

## Custom CoreDNS image (`clawker-coredns:latest`)

The firewall does NOT use the stock `coredns/coredns` image. `cmd/coredns-clawker/main.go` wraps `coremain.Run()` with blank-imports of the stock plugins we use (`forward`, `health`, `log`, `reload`, `template`) plus `internal/dnsbpf`, and prepends `"dnsbpf"` to `dnsserver.Directives`. The binary is cross-compiled to `internal/firewall/assets/coredns-clawker`, embedded via `//go:embed`, and baked into a tiny Alpine image on first use by `ensureCorednsImage` / `ensureEmbeddedImage` — same pattern as `ensureEbpfImage`. `corednsContainerConfig` adds `CAP_BPF + CAP_SYS_ADMIN` (CAP_BPF alone is insufficient for `BPF_MAP_UPDATE_ELEM` on kernels < 5.19) and bind-mounts `/sys/fs/bpf` so the plugin can open the pinned `dns_cache`.

## dnsbpf plugin (pointer)

The `dnsbpf` CoreDNS plugin lives in `internal/dnsbpf` (see `internal/dnsbpf/CLAUDE.md`). It replaces an earlier startup-time seeding approach that drifted from live round-robin/CDN responses and broke per-domain SSH routing. The firewall only cares that the plugin exists in the custom `clawker-coredns:latest` image and that the manager ensures BPF maps are pinned **before** CoreDNS starts — the plugin will crash out loud if `dns_cache` isn't pinned yet.

## sync-routes flow

The eBPF manager binary exposes `sync-routes <jsonArray>`. The firewall manager calls it in three places via `m.syncRoutes(ctx)`:

- `EnsureRunning` — initial population after `ebpfExec("init")` creates the pinned maps
- `regenerateAndRestart` (Reload / AddRules / RemoveRules) — propagates rule edits to every running container live, because `route_map` is global and enforcement is gated on `container_map` presence
- `Enable(containerID)` — idempotent re-sync before enabling a new container

`syncRoutes` calls `TCPMappings(rules, envoyPorts)` (from `envoy.go`) to compute one `ebpfRoute{DomainHash, DstPort, EnvoyPort}` per TCP/SSH mapping, JSON-encodes them via `marshalRoutes`, and passes the result through `ebpfExec`. The kernel-side `SyncRoutes` iterates and deletes the current `route_map` before writing the new set, so a single `firewall add` takes effect everywhere without restarting anything. `ebpf.Route` carries no `Domain` field — hashes are authoritative post-dnsbpf.

## Relationships

- **`internal/config`** — `EgressRule` and `PathRule` types come from the config schema. The firewall package imports config; config does NOT import firewall.
- **`internal/storage`** — `EgressRulesFile` is the document type passed to `storage.Store[EgressRulesFile]` for persisting active rules.
- **`internal/ebpf`** — the firewall manager executes `ebpf-manager` subcommands (`init`, `enable`, `disable`, `sync-routes`, `bypass`, `unbypass`, `resolve`) inside the long-running `clawker-ebpf` container via `docker exec`. The container stays resident purely as an RPC endpoint — the BPF programs themselves live in kernel state (pinned under `/sys/fs/bpf/clawker/`) and would continue enforcing even if the container were stopped. `internal/dnsbpf` opens the `dns_cache` pin directly from inside the CoreDNS container.
- **`internal/dnsbpf`** — custom CoreDNS plugin package, linked into `cmd/coredns-clawker` and baked into the `clawker-coredns:latest` image.
- **`github.com/moby/moby/client`** — `Manager` receives `client.APIClient` (raw moby) in its constructor. Does NOT use `internal/docker` or `pkg/whail` — avoids jail label filtering that hides containers from the daemon's watcher.
- **`internal/cmd/factory`** — Factory exposes `f.Firewall()` as a lazy noun returning `FirewallManager`.
- **`internal/logger`** — `Manager` and `Daemon` accept `*logger.Logger` in constructors.

## Test Double (`mocks/`)

`FirewallManagerMock` is moq-generated from the `FirewallManager` interface. Regenerate after interface changes:

```bash
cd internal/firewall && go generate ./...
```

All methods have corresponding `*Func` fields for injection and `*Calls()` methods for call recording.
