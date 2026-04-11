# eBPF Package

Host-side loader and manager for clawker's eBPF cgroup programs. Replaces iptables DNAT with cgroup-level `connect()`/`sendmsg()` interception for per-container, DNS-aware egress routing. The BPF source (`bpf/clawker.c`) and its generated Go bindings (`clawker_*_bpfel.go`) live here; the runtime binary that drives them from outside the firewall stack is `cmd/` (see `internal/ebpf/cmd/CLAUDE.md`).

## Layout

```
bpf/clawker.c        BPF C source (connect4/6, sendmsg4/6, recvmsg4/6, sock_create)
bpf/common.h         Shared structs: container_config, dns_entry, route_key/val, metric_key
gen.go               //go:generate bpf2go directive — not committed artifacts
clawker_*_bpfel.go   bpf2go-generated Go bindings (gitignored, produced by make ebpf-binary)
clawker_*_bpfel.o    BPF bytecode (gitignored)
manager.go           Go-side Manager: Load/Enable/Disable/SyncRoutes/Bypass/DNS helpers
types.go             Exported types: ContainerConfig, DNSEntry, RouteKey/Val, MetricKey
manager_test.go      Unit tests (no kernel required — exercises non-BPF code paths)
cmd/                 ebpf-manager binary (see cmd/CLAUDE.md)
REPRODUCIBILITY.md   Provenance chain — pin-update procedure for the BPF toolchain
```

## Two-Process Model

1. **Daemon mode** (inside the firewall stack's `clawker-ebpf` container): calls `Load()` once to parse the embedded ELF, pin all maps and programs to `/sys/fs/bpf/clawker/`, and bring the programs into the kernel. Maps live on the pinned filesystem for the lifetime of the kernel.
2. **Command mode** (`ebpf-manager <subcommand>`, invoked via `docker exec`): calls `OpenPinned()` to attach to the already-loaded maps and programs. Every CLI subcommand (`enable`, `disable`, `sync-routes`, `bypass`, `unbypass`, `dns-update`, `gc-dns`) is a short-lived process that mutates pinned maps and exits.

This split matters: the firewall manager (in `internal/firewall`) never imports this package at runtime — it invokes the embedded `ebpf-manager` binary inside the container via `docker exec`. Only the daemon container calls `Load()`.

## Pinned Maps

All maps live at `PinPath = /sys/fs/bpf/clawker/`:

| Map | Key | Value | Written by | Read by |
|-----|-----|-------|-----------|---------|
| `container_map` | cgroup ID (u64) | `container_config` | `Enable`/`Disable` | BPF fast path |
| `bypass_map` | cgroup ID (u64) | u8 (1 = bypass) | `Bypass`/`Unbypass`, cleared by `Enable`/`Disable` | BPF fast path |
| `dns_cache` | IPv4 (u32) | `dns_entry` {domain_hash, expire_ts} | `UpdateDNSCache` **and** `internal/dnsbpf` CoreDNS plugin | BPF fast path |
| `route_map` | `{domain_hash, dst_port}` | `{envoy_port}` | `SyncRoutes` | BPF fast path |
| `metrics_map` | `{cgroup_id, domain_hash, dst_port, action}` | counters | BPF fast path | userspace `dump` |

`route_map` is **global** — container enforcement is gated by presence in `container_map`, so a single `SyncRoutes` call updates routing for every enforced container atomically. The BPF programs look up `{domain_hash, dst_port}` from `dns_cache[resolved_ip]` on connect().

## Key Types and Functions

```go
type Manager struct { /* pin path, logger, loaded objects, per-cgroup links */ }
func NewManager(log *logger.Logger) *Manager

func (m *Manager) Load() error                              // daemon mode: parse ELF, pin all
func (m *Manager) OpenPinned() error                        // command mode: attach to pinned
func (m *Manager) Close() error                             // detach links, close programs/maps
func (m *Manager) Enable(cgroupID uint64, cgroupPath string, cfg clawkerContainerConfig) error
func (m *Manager) Disable(cgroupID uint64) error
func (m *Manager) SyncRoutes(routes []Route) error          // replace global route_map atomically
func (m *Manager) Bypass(cgroupID uint64) error             // set bypass flag (unrestricted egress)
func (m *Manager) Unbypass(cgroupID uint64) error
func (m *Manager) UpdateDNSCache(ip, domainHash, ttl uint32) error
func (m *Manager) GarbageCollectDNS() int                   // returns number cleared
func (m *Manager) LookupContainer(cgroupID uint64) (clawkerContainerConfig, error)
```

Helpers in `types.go`:

```go
const PinPath = "/sys/fs/bpf/clawker"

type Route struct { DomainHash uint32; DstPort, EnvoyPort uint16 }
type ContainerConfig struct { /* mirrors bpf/common.h — Envoy/CoreDNS/gateway IPs, CIDR, host proxy */ }

func IPToUint32(net.IP) uint32                              // network byte order (matches ctx->user_ip4)
func Uint32ToIP(uint32) net.IP
func CIDRToAddrMask(cidr string) (addr, mask uint32, err error)
func DomainHash(domain string) uint32                       // FNV-1a of lowercased domain — MUST match dnsbpf plugin
func NewContainerConfig(envoyIP, corednsIP, gatewayIP, cidr, hostProxyIP string, hostProxyPort, egressPort uint16) (clawkerContainerConfig, error)
func CgroupPath(containerID string) string                  // /sys/fs/cgroup/system.slice/docker-<id>.scope
func CgroupID(cgroupPath string) (uint64, error)            // validated against path-injection, returns inode
func Supported() error                                       // checks cgroup v2 available
```

## Invariants

- `DomainHash` is the shared contract between this package, `internal/dnsbpf`, and `internal/firewall/manager.go`. All three **must** use the same normalization (lowercase fold, no trailing dot, no leading `*.`). Changing the hash function requires changing all three call sites and clearing the pinned `route_map`.
- `Enable` is idempotent and clears stale links + bypass flags before attaching, so it is also the canonical "re-enforce after bypass" entry point.
- `SyncRoutes` collects per-entry errors into `errors.Join` instead of returning on the first failure — a partial sync returns non-nil and callers can decide what to do. Prior to the current behavior, partial syncs silently returned success.
- `CgroupID(path)` validates `path` against `/sys/fs/cgroup/` + `..` + control-char sanitization (defense in depth for the privileged `ebpf-manager` entry points). Treat this as the CodeQL `go/path-injection` sanitizer for any caller that reaches it from `os.Args`.
- Stale pinned maps with mismatched key/value sizes are detected in `Load()` (via `info.KeySize != mapSpec.KeySize`) and removed before loading — schema bumps of the route_key struct during development required this.
- `connect6` / `sendmsg6` are installed even when the firewall only cares about IPv4 — dual-stack sockets can be opened as AF_INET6 and would otherwise bypass enforcement via the IPv6 path.

## Build and Provenance

The BPF toolchain pins (clang, libbpf-dev, linux-libc-dev, bpf2go version, Go toolchain digest) are all captured in `Dockerfile.firewall` at the repo root. `make ebpf-binary` runs `docker buildx build` against that pinned recipe, produces `internal/firewall/assets/ebpf-manager` (the Linux binary `go:embed`'d into the clawker CLI), and does **not** commit any generated artifacts. See `REPRODUCIBILITY.md` for the full chain.

## Imports

This package is a leaf for the firewall stack: imports `github.com/cilium/ebpf`, `github.com/cilium/ebpf/link`, and `internal/logger`. It is imported by `internal/ebpf/cmd` (the binary) and `internal/dnsbpf` (which reuses `DomainHash`/`IPToUint32`/`Uint32ToIP` to stay in sync). `internal/firewall` does **not** import this package at runtime — only at build time via `go:embed` of the `ebpf-manager` binary.
