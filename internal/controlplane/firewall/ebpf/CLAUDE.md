# eBPF Subsystem (under the control plane)

BPF loader + manager for clawker's cgroup programs. Lives under `internal/controlplane/` because ebpf is a **feature of the control plane**, not a peer service: once BPF programs are loaded into the kernel they persist independently of any userspace process, so there is no separate "ebpf service" to run. The CP owns `Manager.Load()` lifetime and drives everything through direct Go imports.

The BPF source (`bpf/clawker.c`) and its generated Go bindings (`clawker_*_bpfel.go`) live here. The short-lived `cmd/` CLI stays as a break-glass debug tool for humans (see `cmd/CLAUDE.md`), but the real interface is `ControlPlaneService` gRPC.

## Layout

```
bpf/clawker.c        BPF C source (connect4/6, sendmsg4/6, recvmsg4/6, sock_create)
bpf/common.h         Shared structs: container_config, dns_entry, route_key/val, metric_key
gen.go               //go:generate bpf2go directive
clawker_*_bpfel.go   bpf2go-generated Go bindings (gitignored, produced by `make ebpf`)
clawker_*_bpfel.o    BPF bytecode (gitignored)
manager.go           Go-side Manager: Load/Enable/Disable/SyncRoutes/Bypass/DNS helpers
types.go             Exported types: ContainerConfig, DNSEntry, RouteKey/Val, MetricKey
manager_test.go      Unit tests (no kernel required — exercises non-BPF code paths)
cmd/                 break-glass ebpf-manager binary (see cmd/CLAUDE.md)
```

## Lifetime ownership

The `clawker-controlplane` container runs `clawker-cp` (the daemon binary) as PID 1. That binary imports `internal/controlplane/firewall/ebpf` directly and calls `Manager.Load()` **exactly once** at startup. The resulting `link.Link` handles live in-process for the CP's lifetime; BPF pinning at `/sys/fs/bpf/clawker/` is purely a crash-recovery mechanism, not load-bearing state.

`Load()` runs `cleanupStaleLinks()` which checks each pinned `link_*` file against `container_map` — links to dead cgroups are removed, links to live cgroups are preserved. This ensures enforcement survives CP restarts while cleaning up resource leaks from dead containers. `CleanupAllLinks()` is a separate method that removes ALL pinned links — called ONLY by the daemon on shutdown when no agent containers remain.

Command-mode access to pinned state is done via the `cmd/ebpf-manager` break-glass binary + `OpenPinned()` (which opens handles to already-pinned maps without re-running Load). That binary is packaged in the CP image alongside `clawker-cp` for emergency debugging.

## Pinned Maps

All maps live at `PinPath = /sys/fs/bpf/clawker/`:

| Map | Key | Value | Written by | Read by |
|-----|-----|-------|-----------|---------|
| `container_map` | cgroup ID (u64) | `container_config` | `Install`/`Remove` | BPF fast path |
| `bypass_map` | cgroup ID (u64) | u8 (1 = bypass) | `Disable`/`Enable`, cleared by `Install`/`Remove` | BPF fast path |
| `dns_cache` | IPv4 (u32) | `dns_entry` {domain_hash, expire_ts} | `UpdateDNSCache` **and** `internal/dnsbpf` CoreDNS plugin | BPF fast path |
| `route_map` | `{domain_hash, dst_port}` | `{envoy_port}` | `SyncRoutes` | BPF fast path |
| `metrics_map` | `{cgroup_id, domain_hash, dst_port, action}` | counters | BPF fast path | userspace `dump` (break-glass) |

`route_map` is **global** — container enforcement is gated by presence in `container_map`, so a single `SyncRoutes` call updates routing for every enforced container atomically.

## Key Types and Functions

```go
type Manager struct { /* pin path, logger, loaded objects, per-cgroup links */ }
func NewManager(log *logger.Logger) *Manager

func (m *Manager) Load() error                              // CP startup: parse ELF, pin all
func (m *Manager) OpenPinned() error                        // break-glass: attach to pinned
func (m *Manager) Close() error                             // detach links, close programs/maps
func (m *Manager) Install(cgroupID uint64, cgroupPath string, cfg BPFContainerConfig) error
func (m *Manager) Remove(cgroupID uint64) error
func (m *Manager) SyncRoutes(routes []Route) error          // replace global route_map atomically
func (m *Manager) Disable(cgroupID uint64) error            // set bypass flag (unrestricted egress)
func (m *Manager) Enable(cgroupID uint64) error             // clear bypass flag (restore enforcement)
func (m *Manager) UpdateDNSCache(ip, domainHash, ttl uint32) error
func (m *Manager) GarbageCollectDNS() int                   // returns number cleared
func (m *Manager) LookupContainer(cgroupID uint64) (clawkerContainerConfig, error)

// Startup / shutdown maintenance — not on EBPFManager interface; called
// by cmd/clawker-cp directly so the RPC surface stays pure.
func (m *Manager) CleanupStaleBypass() (int, error)         // INV-B2-013: clear orphan bypass_map entries at startup
func (m *Manager) FlushAll() error                          // INV-B2-007: drain-to-zero — empty container_map + bypass_map, unpin links
```

Helpers in `types.go`:

```go
const PinPath = "/sys/fs/bpf/clawker"

type Route struct { DomainHash uint32; DstPort, EnvoyPort uint16 }
type ContainerConfig struct { /* mirrors bpf/common.h — Envoy/CoreDNS/gateway IPs, CIDR, host proxy */ }

func IPToUint32(net.IP) uint32                              // network byte order (matches ctx->user_ip4)
func Uint32ToIP(uint32) net.IP
func CIDRToAddrMask(cidr string) (addr, mask uint32, err error)
func DomainHash(domain string) uint32                       // FNV-1a of lowercased domain
func NewContainerConfig(envoyIP, corednsIP, gatewayIP, cidr, hostProxyIP string, hostProxyPort, egressPort uint16) (clawkerContainerConfig, error)
func CgroupPath(containerID string) string                  // /sys/fs/cgroup/system.slice/docker-<id>.scope
func CgroupID(cgroupPath string) (uint64, error)            // validated against path-injection, returns inode
func Supported() error                                       // checks cgroup v2 available
```

## Invariants

- `DomainHash` is the shared contract between this package, `internal/dnsbpf`, and the CP firewall domain (`internal/controlplane/firewall`, specifically `normalizeDomain` + `Handler.FirewallSyncRoutes`). All three **must** use the same normalization (lowercase fold, no trailing dot, no leading `*.`). Changing the hash function requires changing all three call sites and clearing the pinned `route_map`.
- `Install` is idempotent and clears stale links + bypass flags before attaching, so it is also the canonical "re-enforce after bypass" entry point.
- `SyncRoutes` collects per-entry errors into `errors.Join` instead of returning on the first failure — a partial sync returns non-nil and callers can decide what to do.
- `CgroupID(path)` validates `path` against `/sys/fs/cgroup/` + `..` + control-char sanitization (defense in depth for the privileged `ebpf-manager` break-glass paths).
- Stale pinned maps with mismatched key/value sizes are detected in `Load()` and removed before loading.
- `connect6` / `sendmsg6` are installed even when the firewall only cares about IPv4 — dual-stack sockets can be opened as AF_INET6 and would otherwise bypass enforcement.

## Build and Provenance

The BPF toolchain pins (clang, libbpf-dev, linux-libc-dev) live in the Makefile's `BPF_APT_DEPS` variable; the bpf2go version + clang flags live in `gen.go`. CI runs `sudo make bpf-deps` on `ubuntu-latest` to apt-install the pinned toolchain, then `make ebpf` (native `go generate`) produces `clawker_*_bpfel.{go,o}` on the host tree. macOS dev routes through `Dockerfile.controlplane`, which `COPY`s the same Makefile and runs `make bpf-deps` inside a pinned `debian:bookworm-slim@sha256:...` image. Either path produces identical bpf2go bindings; nothing generated is committed.

To bump pins: resolve fresh apt versions against the pinned `debian:bookworm-slim@sha256:...` with `docker run --rm debian:bookworm-slim@sha256:<digest> bash -c 'apt-get update >/dev/null && apt-cache policy clang llvm libbpf-dev linux-libc-dev | grep Candidate'`, paste each `Candidate:` into `BPF_APT_DEPS` in the Makefile. The Dockerfile picks up the new values automatically.

`make ebpf-binary` / `make cp-binary` / `make coredns-binary` / `make clawkerd-binary` are plain `CGO_ENABLED=0 GOOS=linux GOARCH=$(BUILDX_TARGETARCH) go build` targets. The `//go:embed` of the BPF `.o` bytecode is pulled in by `clawker_*_bpfel.go`, so the binary build itself never touches clang or Docker.

## Imports

- **Imported by**: `internal/controlplane` (the CP binary — imports `Manager`, `Route`, types), `internal/controlplane/firewall` (the firewall domain handler — `Manager` interface satisfies `EBPFManager`), `internal/dnsbpf` (reuses `DomainHash`/`IPToUint32`/`Uint32ToIP` to stay in sync), `internal/controlplane/firewall/ebpf/cmd` (the break-glass CLI).
- **Imports**: `github.com/cilium/ebpf`, `github.com/cilium/ebpf/link`, `internal/logger`.
