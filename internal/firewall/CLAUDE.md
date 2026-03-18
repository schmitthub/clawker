# Firewall Package

Domain contracts, Docker implementation, daemon, certificates, and network management for the Envoy+CoreDNS firewall stack.

## Contents

| File | Purpose |
|------|---------|
| `firewall.go` | `FirewallManager` interface, `FirewallStatus`, `HealthTimeoutError`, sentinel errors |
| `types.go` | `EgressRulesFile` — top-level document for `storage.Store[T]` |
| `coredns.go` | `GenerateCorefile(rules)` — CoreDNS Corefile from egress rules |
| `envoy.go` | `GenerateEnvoyConfig(rules)` — Envoy bootstrap YAML from egress rules |
| `certs.go` | CA and per-domain TLS certificate management for MITM inspection |
| `daemon.go` | Background daemon process — health probes + container watcher |
| `manager.go` | `Manager` — Docker implementation of `FirewallManager` |
| `network.go` | `NetworkInfo`, Docker network creation, static IP computation |
| `rules.go` | `NewRulesStore(cfg)` — `storage.Store[EgressRulesFile]` factory |
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
    StopBypass(ctx context.Context, containerID string) error
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

Docker implementation of `FirewallManager`. Creates and manages Envoy + CoreDNS containers on an isolated Docker network with static IPs.

```go
func NewManager(client *docker.Client, cfg config.Config, log *logger.Logger) (*Manager, error)
```

`Manager` holds a `*docker.Client`, `config.Config`, `*logger.Logger`, and a `*storage.Store[EgressRulesFile]`. All `FirewallManager` methods are implemented as `*Manager` receivers.

Key internal methods: `ensureNetwork`, `ensureContainer`, `ensureConfigs`, `regenerateAndRestart`, `syncProjectRules`.

## Certificate Management (`certs.go`)

Self-signed CA and per-domain TLS certificates for Envoy MITM inspection of path-rule traffic.

```go
func EnsureCA(dataDir string) (*x509.Certificate, *ecdsa.PrivateKey, error)
func GenerateDomainCert(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, domain string) (certPEM []byte, keyPEM []byte, err error)
func RegenerateDomainCerts(rules []config.EgressRule, dataDir string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) error
func RotateCA(dataDir string, rules []config.EgressRule) error
```

- `EnsureCA` — creates or loads CA keypair (`ca-cert.pem`, `ca-key.pem`) in `dataDir`
- `GenerateDomainCert` — signs a per-domain certificate with the CA; returns PEM bytes
- `RegenerateDomainCerts` — generates certs for all rules with `PathRules` (MITM); skips SNI-passthrough rules
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
func GenerateCorefile(rules []config.EgressRule) ([]byte, error)
```

- Only "allow" rules with domain destinations produce forward zones
- Each domain gets `forward . 1.1.1.2 1.0.0.2` (Cloudflare malware-blocking)
- Catch-all `.` zone: `template IN ANY . { rcode NXDOMAIN }` + `health :8080` + `reload`
- IP/CIDR destinations and deny rules are excluded

### Envoy (`envoy.go`)

```go
const EnvoyTLSPort     = 10000
const EnvoyTCPPortBase = 10001

func GenerateEnvoyConfig(rules []config.EgressRule) ([]byte, []string, error)
```

- Returns YAML bytes + warnings (non-fatal issues like skipped IP/CIDR rules)
- TLS listener on `:10000` with TLS Inspector
- Filter chain ordering: MITM (path rules) -> SNI passthrough -> default deny
- MITM chains: TLS termination with per-domain cert, HTTP route matching, dynamic forward proxy
- Passthrough chains: `sni_dynamic_forward_proxy` network filter
- Default deny: `tcp_proxy` -> `deny_cluster` (static, no endpoints = connection reset)
- TCP/SSH listeners on `:10001+` (sequential ports)

## Relationships

- **`internal/config`** — `EgressRule` and `PathRule` types come from the config schema. The firewall package imports config; config does NOT import firewall.
- **`internal/storage`** — `EgressRulesFile` is the document type passed to `storage.Store[EgressRulesFile]` for persisting active rules.
- **`internal/docker`** — imports firewall for container creation; `Manager` receives `*docker.Client` in its constructor.
- **`internal/cmd/factory`** — Factory exposes `f.Firewall()` as a lazy noun returning `FirewallManager`.
- **`internal/logger`** — `Manager` and `Daemon` accept `*logger.Logger` in constructors.

## Test Double (`mocks/`)

`FirewallManagerMock` is moq-generated from the `FirewallManager` interface. Regenerate after interface changes:

```bash
cd internal/firewall && go generate ./...
```

All methods have corresponding `*Func` fields for injection and `*Calls()` methods for call recording.
