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

### `EnsureRunning` ordering (critical)

The startup order is load-bearing because the `dnsbpf` plugin opens the pinned `dns_cache` BPF map on CoreDNS startup — the map must exist first:

1. `ensureNetwork` — create/discover `clawker-net`
2. `discoverNetwork` — compute Envoy/CoreDNS static IPs + CIDR
3. `ensureConfigs` — regenerate Corefile, envoy.yaml, and per-domain certs
4. `ensureEbpfImage` + `ensureCorednsImage` — build locally-tagged images from embedded Linux binaries if missing
5. `ensureContainer(ebpfContainer)` + `ebpfExec("init")` — start the eBPF manager container and load/pin BPF programs and maps (this creates `/sys/fs/bpf/clawker/dns_cache`)
6. `syncRoutes` — populate the global `route_map` from current egress rules (`ebpf-manager sync-routes`)
7. `ensureContainer(envoyContainer)` + `ensureContainer(corednsContainer)` — start Envoy and CoreDNS (the dnsbpf plugin opens the pre-existing pinned map)
8. `WaitForHealthy` — HTTP GET Envoy `:18901/`, HTTP GET CoreDNS `:18902/health`

### `regenerateAndRestart` (used by `Reload`, `AddRules`, `RemoveRules`)

1. `ensureConfigs` — regenerate Corefile, envoy.yaml, certs
2. Early-return if the stack is not running (daemon will start it with fresh configs)
3. `ebpfExec("init")` — ensure BPF maps remain pinned before CoreDNS restarts (the dnsbpf plugin re-opens them)
4. `syncRoutes` — update the global `route_map` so **already-running** containers see new rules immediately without needing to be restarted
5. `restartContainer(envoyContainer)` + `restartContainer(corednsContainer)`
6. `WaitForHealthy`

### `Enable(containerID)` — attach a container to the firewall

1. `ensureEbpfImage` + `ensureContainer(ebpfContainer)` + `ebpfExec("init")` — idempotent
2. `syncRoutes` — idempotent; ensures the global route_map is populated before the container starts enforcing
3. Resolve host-proxy IP (if enabled) via `ebpfExec("resolve", "host.docker.internal")`
4. Build a container-config JSON (IPs, ports — no routes; the route_map is global) and invoke `ebpfExec("enable", cgroupPath, cfgJSON)` — this writes a `container_map` entry (cgroup_id → container_config) and attaches the six cgroup programs
5. `touchSignalFile` — unblock the container's entrypoint

### Global `route_map` architecture

The BPF `route_map` key is a `{domain_hash, dst_port}` pair (see `internal/ebpf/bpf/common.h`), shared across all enforced containers. Presence in `container_map` is what marks a cgroup as "clawker-enforced"; the route_map itself is **global**. This lets `firewall add/remove/reload` propagate new rules to all running containers via a single `sync-routes` call, without restarting any agent containers.

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

The firewall stack does **not** use the stock `coredns/coredns` image. It builds a custom `clawker-coredns:latest` image on-demand from an embedded Linux binary:

- `cmd/coredns-clawker/main.go` — a thin wrapper around `coremain.Run()` that blank-imports the stock plugins we actually use (`forward`, `health`, `log`, `reload`, `template`) plus our own `internal/dnsbpf` plugin, then prepends `"dnsbpf"` to `dnsserver.Directives` so the plugin sees every DNS response before the client does
- `Makefile` target `coredns-binary` cross-compiles this into `internal/firewall/assets/coredns-clawker` (linux/$GOARCH, CGO disabled, static)
- `internal/firewall/coredns_embed.go` embeds the binary via `//go:embed assets/coredns-clawker`
- `ensureCorednsImage` / `ensureEmbeddedImage` (manager.go) lazily build a tiny Alpine-based Docker image with the binary as ENTRYPOINT on first use — same shared pattern as `ensureEbpfImage`
- `corednsContainerConfig` uses this image, mounts the generated Corefile, bind-mounts `/sys/fs/bpf` (so the dnsbpf plugin can open the pinned `dns_cache` map), and adds `CAP_BPF` + `CAP_SYS_ADMIN` capabilities (CAP_BPF alone is insufficient for `BPF_MAP_UPDATE_ELEM` on kernels < 5.19)

## dnsbpf plugin (`internal/dnsbpf`)

The `dnsbpf` CoreDNS plugin is a first-party plugin that bridges DNS resolution into eBPF. It replaces an earlier startup-time "seed" approach that resolved allowed domains once and wrote stale mappings into `dns_cache` — seeded entries drifted from live round-robin/CDN responses, breaking per-domain TCP routing (most visibly for SSH).

| File | Purpose |
|------|---------|
| `setup.go` | Registers the `dnsbpf` plugin with CoreDNS/Caddy, parses the empty `dnsbpf {}` block, opens the pinned `dns_cache` map **once** (guarded by `sync.Once` so reloads don't close the FD), and attaches a `Handler` to each server block capturing its Zone |
| `dnsbpf.go` | `Handler.ServeDNS` wraps the next plugin with `nonwriter.New(w)`, inspects the returned `*dns.Msg` for `dns.A` records, writes each resolved IP to the BPF map, then forwards the original response to the client. `zoneToDomain` trims the trailing `.` so the zone hash matches `route_map`'s keys |
| `bpfmap.go` | `OpenBPFMap` / `BPFMap.Update` wrap `cilium/ebpf.LoadPinnedMap` for `/sys/fs/bpf/clawker/dns_cache`; `Update` encodes `dnsEntry{DomainHash, ExpireTs = now + ttl}` (min 60 s floor). `Update` logs on failure and never crashes CoreDNS |
| `log.go` | Trivial `clog` shim |

Design notes:

- **Zone hash, not query hash**: `writeARecords` hashes `Handler.Zone` (e.g., `.example.com` for a wildcard zone), not the queried name. This matches how the manager populates `route_map` from egress-rule `Dst` values, so a lookup for `sub.example.com` finds the same wildcard route entry.
- **No `OnShutdown` close**: CoreDNS's `reload` plugin recreates server blocks without restarting the process. A `sync.Once`-guarded open would never re-execute after close, so the handler intentionally leaves the pinned map FD open for the process lifetime.
- **Imports `internal/ebpf` directly**: since the plugin lives in the same Go module, there is no duplication of `DomainHash` (FNV-1a) or `IPToUint32` — these come from `internal/ebpf/types.go`.
- Tests in `internal/dnsbpf/dnsbpf_test.go` exercise `ServeDNS`, `writeARecords`, and zone parsing with an in-memory `MapWriter` fake.

## sync-routes flow (global `route_map`)

The eBPF manager binary exposes a `sync-routes <jsonArray>` subcommand. The manager calls it in three places, each via `m.syncRoutes(ctx)`:

- `EnsureRunning` — initial population after `ebpfExec("init")` creates the pinned maps
- `regenerateAndRestart` (Reload/AddRules/RemoveRules) — propagates rule edits to every running container live, because the map is global and `container_map` presence is what gates enforcement
- `Enable(containerID)` — idempotent re-sync before enabling a new container (defense-in-depth for first-container-after-daemon-restart)

`syncRoutes` calls `TCPMappings(rules, envoyPorts)` (from `envoy.go`) to compute one `ebpfRoute{DomainHash, DstPort, EnvoyPort}` per TCP/SSH mapping, JSON-encodes them via `marshalRoutes`, and passes the result through `ebpfExec`. On the eBPF side (`internal/ebpf/manager.go`), `SyncRoutes` first iterates and deletes the entire current `route_map`, then writes the new set — this is why a single `firewall add` call takes effect everywhere without restarting anything.

The `ebpfRoute` / `ebpf.Route` structs contain only `{DomainHash, DstPort, EnvoyPort}` — there is no `Domain` field (the seed-time design fed domain strings back into the manager; post-dnsbpf the domain string is not needed at all because hashes are authoritative).

## Stale pinned map cleanup

`ebpf.Manager.Load()` runs before program load and compares each pinned map on disk (`/sys/fs/bpf/clawker/<name>`) against the spec's `KeySize` / `ValueSize`. If either dimension changed, the stale pin is removed so the loader can create a fresh map — this is how the `route_key` size change (dropping `cgroup_id`, going from per-container to global) survives rolling upgrades without forcing users to manually unpin anything.

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
