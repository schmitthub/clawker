# ebpf-manager Binary

Host-side command-mode entrypoint for the clawker eBPF subsystem. Compiled for Linux, embedded as a go-embed blob into the clawker CLI (`internal/firewall/assets/ebpf-manager` → `internal/firewall/ebpf_embed.go`), and dropped into the on-demand `clawker-ebpf:latest` Docker image. The firewall manager invokes it via `docker exec` for every per-container mutation.

## Subcommands

| Subcommand | Args | Purpose |
|---|---|---|
| `init` | — | `Manager.Load()` — parse embedded ELF, pin maps + programs to `/sys/fs/bpf/clawker/`. Daemon startup only. |
| `enable` | `<cgroupPath> <configJSON>` | Populate `container_map[cgroupID]` from `enableArgs` (Envoy/CoreDNS/gateway IPs, CIDR, host proxy) and attach all seven cgroup programs. Also clears any stale bypass flag. |
| `disable` | `<cgroupPath>` | Detach links, delete `container_map` entry, clear bypass flag. |
| `bypass` | `<cgroupPath>` | Set `bypass_map[cgroupID] = 1` — unrestricted egress for the container. |
| `unbypass` | `<cgroupPath>` | Clear the bypass flag — container falls back to enforcement. |
| `sync-routes` | `<routesJSON>` | Replace the global `route_map` atomically with `[]Route{{DomainHash, DstPort, EnvoyPort}, ...}`. Called by the firewall manager on `EnsureRunning`, `regenerateAndRestart`, and per-container `Enable`. |
| `dns-update` | `<ip> <domainHash> <ttl>` | Write a single `dns_cache[ip] = {domain_hash, now+ttl}` entry. Historical — the `internal/dnsbpf` CoreDNS plugin now writes the map in real time; this path is retained for diagnostics. |
| `gc-dns` | — | `Manager.GarbageCollectDNS()` — iterate `dns_cache`, delete entries whose `expire_ts < now`. Returns count to stdout. |
| `dump` | `<cgroupPath>` | Read `container_map`, `bypass_map`, and per-cgroup metrics for diagnostics. |
| `resolve` | `<hostname>` | Look up a hostname via the libc resolver and print the IPv4/IPv6 result. Used to debug DNS seeding vs CoreDNS routing during incident response. |

Anything other than the above prints usage to stderr and exits 1.

## Flow

```
main() → logger.Nop() → switch on os.Args[1]
         │
         ├── runInit:    NewManager().Load()              (daemon boot)
         ├── runEnable:  NewManager().OpenPinned() → Enable(cgroupID, path, cfg)
         ├── runDisable: NewManager().OpenPinned() → Disable(cgroupID)
         ├── runSyncRoutes: NewManager().OpenPinned() → SyncRoutes(routes)
         ├── ... (other subcommands similarly open pinned maps and mutate)
```

Every non-`init` path uses `NewManager().OpenPinned()` + `defer Close()` — maps stay pinned across process exit; only the per-process handles are closed.

## `enableArgs` JSON Schema

```go
type enableArgs struct {
    EnvoyIP       string `json:"envoy_ip"`
    CoreDNSIP     string `json:"coredns_ip"`
    GatewayIP     string `json:"gateway_ip"`
    CIDR          string `json:"cidr"`           // clawker-net subnet, passed to CIDRToAddrMask
    HostProxyIP   string `json:"host_proxy_ip"`  // optional (empty → 0)
    HostProxyPort uint16 `json:"host_proxy_port"`
    EgressPort    uint16 `json:"egress_port"`    // Envoy main listener
}
```

The firewall manager (`internal/firewall`) builds this payload from project/container config, JSON-encodes it, and passes it as `os.Args[3]` to `ebpf-manager enable`.

## Error Handling

`fatal(cmd, err)` prints `"<cmd>: <err>"` to stderr and calls `os.Exit(1)`. No partial success reporting — command-mode mutations are all-or-nothing from the caller's perspective, and the underlying `Manager` methods already use `errors.Join` where appropriate (e.g. `SyncRoutes`).

## Security

Runs inside the `clawker-ebpf` container with `CAP_BPF + CAP_SYS_ADMIN` and a bind mount of `/sys/fs/bpf` and `/sys/fs/cgroup`. Every subcommand that accepts a `<cgroupPath>` passes it through `clawkerebpf.CgroupID` — which runs `validateCgroupPath` (rejects empty, NUL/CR/LF, `..`, anything outside `/sys/fs/cgroup/`). This is the CodeQL `go/path-injection` sanitizer for the `os.Args[n] → file open` flow.

## Imports

Leaf binary: imports `internal/ebpf` (everything BPF-side), `internal/logger`, and stdlib only. No docker, no firewall, no cobra — keep it small and boot-fast.

## Provenance

Binary lives at `internal/firewall/assets/ebpf-manager` after `make ebpf-binary`. The Makefile target delegates to `docker buildx build` against the pinned multi-stage `Dockerfile.firewall` at the repo root. Nothing about this binary is committed — the Dockerfile is the build recipe and the binary is regenerated on every build. See `internal/ebpf/REPRODUCIBILITY.md`.
