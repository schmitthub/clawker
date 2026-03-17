# Firewall Package

Domain contracts for the Envoy+CoreDNS firewall stack.

## Contents

| File | Purpose |
|------|---------|
| `firewall.go` | `FirewallManager` interface + `FirewallStatus` type |
| `types.go` | `EgressRulesFile` — top-level document for `storage.Store[T]` |
| `coredns.go` | `GenerateCorefile(rules)` — CoreDNS Corefile from egress rules |
| `envoy.go` | `GenerateEnvoyConfig(rules)` — Envoy bootstrap YAML from egress rules |
| `firewalltest/mock_manager.go` | `MockManager` test double (no containers, no network) |

## Interface

```go
type FirewallManager interface {
    EnsureRunning(ctx context.Context) error
    Stop(ctx context.Context) error
    IsRunning(ctx context.Context) bool
    Update(ctx context.Context, rules []config.EgressRule) error
    Remove(ctx context.Context, rules []config.EgressRule) error
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
- Config built as `map[string]any`, marshalled with `gopkg.in/yaml.v3`

## Relationships

- **`internal/config`** — `EgressRule` and `PathRule` types come from the config schema. The firewall package imports config; config does NOT import firewall.
- **`internal/storage`** — `EgressRulesFile` is the document type passed to `storage.Store[EgressRulesFile]` for persisting active rules.
- **`internal/docker`** (future) — `DockerFirewallManager` will live in `internal/docker` and implement `FirewallManager` using `whail.Engine`.
- **`internal/cmd/factory`** — Factory will expose `f.Firewall()` as a lazy noun returning `FirewallManager`.

## Test Double (`firewalltest/`)

```go
mock := firewalltest.NewMockManager()          // Starts not running; EnsureRunning transitions to running
mock := firewalltest.NewRunningMockManager()   // Already running
mock := firewalltest.NewFailingMockManager(err) // EnsureRunning returns error
```

All methods have corresponding `*Fn` function fields for fine-grained injection:

```go
mock.UpdateFn = func(ctx context.Context, rules []config.EgressRule) error {
    // assert rules or return a specific error
    return someErr
}
```

State mutations from default method implementations (e.g. `EnsureRunning` sets `Running = true`) are visible via the `Running` field for assertions.
