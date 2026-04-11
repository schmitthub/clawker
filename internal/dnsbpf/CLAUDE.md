# dnsbpf Package

CoreDNS plugin that populates the clawker BPF `dns_cache` map in real time. Installed as a `dnsbpf` directive in the custom CoreDNS build (`cmd/coredns-clawker`), registered first in every server block so it wraps the downstream resolver (typically `forward`) and intercepts the response.

Purpose: let the BPF `connect4` program route per-domain TCP traffic (e.g. `ssh github.com` vs `ssh gitlab.com`, both on port 22) to the correct Envoy listener. CoreDNS resolves → the plugin writes `dns_cache[resolved_ip] = {domain_hash, expire_ts}` → the BPF fast path looks it up on the next `connect()`.

## Files

| File | Purpose |
|------|---------|
| `dnsbpf.go` | `Handler` — implements `plugin.Handler`. Captures responses via `nonwriter`, extracts A records, writes to the BPF map, forwards the original response upstream. |
| `setup.go` | `setup` (Caddy controller callback) — parses the `dnsbpf` block, opens the shared BPF map once via `sync.Once`, captures the zone name, registers the handler. Runs on every server-block init (including CoreDNS reloads). |
| `bpfmap.go` | `BPFMap` — thin `cilium/ebpf` wrapper around the pinned `dns_cache` map, matching `struct dns_entry` in `bpf/common.h`. `Update(ip, hash, ttl)` log-and-drops individual write failures (non-fatal — the next DNS answer retries). |
| `log.go` | CoreDNS-style logger (thin wrapper around `coredns/coredns/plugin/pkg/log`). |
| `dnsbpf_test.go` | Unit tests using a `cannedHandler` downstream stub to exercise ServeDNS without a real resolver. |

## Key Types

```go
type MapWriter interface {
    Update(ip, domainHash, ttlSeconds uint32)
}

type Handler struct {
    Next plugin.Handler
    Zone string    // Corefile zone (e.g., "github.com." or ".example.com.")
    Map  MapWriter // Shared BPF map writer; nil in tests
}

type BPFMap struct { m *ebpf.Map }
func OpenBPFMap(pinPath string) (*BPFMap, error)
func (b *BPFMap) Update(ip, domainHash, ttlSeconds uint32)
func (b *BPFMap) Close() error

const DefaultPinPath = "/sys/fs/bpf/clawker/dns_cache"
const pluginName    = "dnsbpf"
```

## Domain Hash Contract

The plugin computes the domain hash **from the Corefile zone name, not the query name**. This is load-bearing for wildcard zones: a `.example.com { dnsbpf; forward … }` block hashes `.example.com` once so every subdomain answered through that zone maps to the same hash that `internal/firewall/manager.go` wrote into `route_map`.

The hash function is `clawkerebpf.DomainHash` (FNV-1a of `strings.ToLower(domain)`) — the exact same call `internal/ebpf` and `internal/firewall/manager.go` use. Do not inline a second hash implementation here; reuse the one from `internal/ebpf` so the three call sites stay synchronized.

`zoneToDomain(zone)` strips the trailing dot (`"github.com." → "github.com"`) before hashing, matching how the firewall manager normalizes destination strings before writing to the route map.

## Shared Map Lifecycle

`sharedMap` + `sharedMapOnce` + `sharedMapErr` implement a process-wide singleton:

- First zone to call `setup` opens the pinned map at `DefaultPinPath`.
- Subsequent zones (one per forwarded domain in the Corefile) reuse the same file descriptor.
- **No `OnShutdown` close handler.** CoreDNS's `reload` plugin tears down and rebuilds all server blocks without restarting the process. Closing the FD on shutdown would invalidate it permanently (the `sync.Once` won't re-execute), so the plugin deliberately leaks the FD for the lifetime of the process. The map is pinned — the kernel-side map survives regardless.
- If the map can't be opened, `setup` returns an error and the CoreDNS process fails to start. This is intentional: running CoreDNS without the BPF map defeats the plugin's entire purpose.

## Runtime Requirements

The custom CoreDNS container (`clawker-coredns:latest`, built on demand by `internal/firewall`) runs with `CAP_BPF + CAP_SYS_ADMIN` and a bind mount of `/sys/fs/bpf`. `CAP_BPF` alone is insufficient on kernels < 5.19 for `BPF_MAP_UPDATE_ELEM`, which is why `CAP_SYS_ADMIN` is added — this was observed during the CoreDNS plugin initiative. The eBPF manager must run its `init` pass **before** CoreDNS starts so the pinned map exists when `OpenBPFMap` runs.

## Test Seam

`Handler.Map` is the `MapWriter` interface — tests construct a `Handler` with a fake map writer (e.g., a channel-backed recorder) and drive it via a `cannedHandler` stub as `Next`. This exercises the full A-record extraction path without a live BPF map or kernel.

## Imports

Imports: `github.com/coredns/coredns/plugin` + `plugin/pkg/nonwriter` + `core/dnsserver`, `github.com/coredns/caddy`, `github.com/miekg/dns`, `github.com/cilium/ebpf`, and `internal/ebpf` (for `DomainHash`, `IPToUint32`, `Uint32ToIP`). Imported by `cmd/coredns-clawker` (the binary) and nothing else. `internal/firewall` embeds the built binary but does not import this package.
