# dnsbpf Package

CoreDNS plugin that populates the clawker BPF `dns_cache` map in real time. Installed as a `dnsbpf` directive in the custom CoreDNS build (`cmd/coredns-clawker`), registered second (after `otel`) in every server block so it wraps the downstream resolver (typically `forward`) and intercepts the response.

Runtime owner: `internal/controlplane/firewall.Stack` builds the `clawker-coredns:latest` image on demand (embeds `cmd/coredns-clawker` via `//go:embed`), manages its container lifecycle, and provides the pinned `dns_cache` map at `/sys/fs/bpf/clawker/dns_cache`.

Purpose: let the BPF `connect4` program route per-domain TCP traffic (e.g. `ssh github.com` vs `ssh gitlab.com`, both on port 22) to the correct Envoy listener. CoreDNS resolves → the plugin writes `dns_cache[resolved_ip] = {identity, expire_ts, source}` → the BPF fast path looks it up on the next `connect()`.

## Files

| File | Purpose |
|------|---------|
| `dnsbpf.go` | `Handler` — implements `plugin.Handler`. Captures responses via `nonwriter`, extracts A records, clamps TTLs to `minTTLSeconds` (60s — cilium tofqdns-min-ttl analog), writes to the BPF map under the zone's CP-allocated identity, forwards the original response upstream. |
| `setup.go` | `setup` (Caddy controller callback) — parses the `dnsbpf <identity>` directive (exactly one required non-zero u32 argument), opens the shared BPF map once via `sync.Once`, registers the handler. Runs on every server-block init (including CoreDNS reloads). |
| `bpfmap.go` | `BPFMap` — thin `cilium/ebpf` wrapper around the pinned `dns_cache` map, matching `struct dns_entry` in `bpf/common.h`. `Update(ip, identity, ttl)` refuses to overwrite a `DNSSourceSeed` entry (CP SyncRoutes-owned IP-literal seed — source precedence), writes `DNSSourceDNS` otherwise, and log-and-drops individual write failures (non-fatal — the next DNS answer retries). `dnsCacheMap` is the injectable map seam for unit tests. |
| `log.go` | CoreDNS-style logger (thin wrapper around `coredns/coredns/plugin/pkg/log`). |
| `dnsbpf_test.go` | Unit tests using a `cannedHandler` downstream stub to exercise ServeDNS without a real resolver. |

## Key Types

```go
type MapWriter interface {
    Update(ip, identity, ttlSeconds uint32)
}

type Handler struct {
    Next     plugin.Handler
    Zone     string    // Corefile zone (e.g., "github.com." or ".example.com.") — logging only
    Identity uint32    // CP-allocated route identity, parsed from the directive argument
    Map      MapWriter // Shared BPF map writer; nil skips writes
}

type BPFMap struct { m dnsCacheMap }
func OpenBPFMap(pinPath string) (*BPFMap, error)
func (b *BPFMap) Update(ip, identity, ttlSeconds uint32)
func (b *BPFMap) Close() error

const DefaultPinPath = "/sys/fs/bpf/clawker/dns_cache"
const pluginName    = "dnsbpf"
const minTTLSeconds = 60
```

## Route Identity Contract

The plugin derives NOTHING itself. Each zone's route identity is allocated CP-side by `firewall.IdentityAllocator` and delivered as the directive's argument by the Corefile generator (`coredns_config.go` writes `dnsbpf 261` per zone; a destination with no identity gets no directive — fail closed). Keying `dns_cache` writes by the **zone's** identity (not the query name) is what makes wildcard zones work: all subdomains of `.example.com` map to the one identity `route_map` is keyed on. Because the zone set and its identity arguments are regenerated together and applied atomically through `Stack.Reload`, the `dns_cache` and `route_map` keyspaces can never drift.

Write precedence (cilium ipcache source-precedence analog): `Update` first looks the key up and leaves any `clawkerebpf.DNSSourceSeed` entry untouched — those are written by CP `SyncRoutes` for IP-literal rules and owned by its reconcile lifecycle. DNS-derived writes carry `clawkerebpf.DNSSourceDNS`.

TTLs below `minTTLSeconds` (60s) are clamped up — very short CDN TTLs would otherwise let the userspace GC sweep an entry between the DNS answer and the app's `connect()` (cilium's tofqdns-min-ttl analog).

## Shared Map Lifecycle

`sharedMap` + `sharedMapOnce` + `sharedMapErr` implement a process-wide singleton:

- First zone to call `setup` opens the pinned map at `DefaultPinPath`.
- Subsequent zones (one per forwarded domain in the Corefile) reuse the same file descriptor.
- **No `OnShutdown` close handler.** CoreDNS's `reload` plugin tears down and rebuilds all server blocks without restarting the process. Closing the FD on shutdown would invalidate it permanently (the `sync.Once` won't re-execute), so the plugin deliberately leaks the FD for the lifetime of the process. The map is pinned — the kernel-side map survives regardless.
- If the map can't be opened, `setup` returns an error and the CoreDNS process fails to start. This is intentional: running CoreDNS without the BPF map defeats the plugin's entire purpose.

## Runtime Requirements

The custom CoreDNS container (`clawker-coredns:latest`, built on demand by the CP's `firewall.Stack`) runs with `CAP_BPF + CAP_SYS_ADMIN` and a bind mount of `/sys/fs/bpf`. `CAP_BPF` alone is insufficient on kernels < 5.19 for `BPF_MAP_UPDATE_ELEM`, which is why `CAP_SYS_ADMIN` is added — this was observed during the CoreDNS plugin initiative. The CP's `ebpf.Manager.Load()` must run **before** CoreDNS starts so the pinned map exists when `OpenBPFMap` runs.

## Test Seam

`Handler.Map` is the `MapWriter` interface — tests construct a `Handler` with a fake map writer (e.g., a mutex-guarded slice recorder) and drive it via a `cannedHandler` stub as `Next`. This exercises the full A-record extraction path without a live BPF map or kernel.

## Imports

Imports: `github.com/coredns/coredns/plugin` + `plugin/pkg/nonwriter` + `core/dnsserver`, `github.com/coredns/caddy`, `github.com/miekg/dns`, `github.com/cilium/ebpf`, and `controlplane/firewall/ebpf` (for `IPToUint32`, `Uint32ToIP`, and the `DNSSource*` precedence constants). Imported by `cmd/coredns-clawker` (the binary) and nothing else. `internal/controlplane/firewall` embeds the built binary but does not import this package.
