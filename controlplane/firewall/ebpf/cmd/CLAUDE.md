# ebpf-manager Binary

**Break-glass only.** Not a supported interface.

This binary is kept in the `clawker-controlplane` container image as a resilience last-ditch tool for humans debugging a misbehaving control plane. It is **not** the interface between the firewall manager and the CP — that channel is typed gRPC via `AdminService` on the CP's TCP listener (`AdminPort`).

## When to use it

Use `ebpf-manager` via `docker exec clawker-controlplane ebpf-manager <subcommand>` when:
- The CP process is unresponsive but the BPF programs are still pinned in the kernel
- You need to inspect raw `container_map` / `bypass_map` / `dns_cache` state via `dump`
- You need to manually poke pinned maps during an incident response

Do **not** wire new consumers to it. Adding new call sites is a signal that the real interface (`AdminService`) is missing a method — add it there instead.

## Subcommands

| Subcommand | Args | Purpose |
|---|---|---|
| `init` | — | `Manager.Load()` — parse embedded ELF, pin maps + programs to `/sys/fs/bpf/clawker/`, and clean up stale links. Calling it again re-runs `cleanupStaleLinks()` which removes links for dead cgroups while preserving enforcement on live containers. Only run during disaster recovery when the CP is down and you need to re-pin. (In normal operation the CP calls `Manager.Load()` directly; it does not exec this binary.) |
| `enable` | `<cgroupPath> <configJSON>` | Manual container enrollment. Populates `container_map[cgroupID]` and attaches programs. |
| `disable` | `<cgroupPath>` | Detach programs, delete `container_map` entry, clear bypass flag. |
| `bypass` | `<cgroupPath>` | Set `bypass_map[cgroupID] = 1`. |
| `unbypass` | `<cgroupPath>` | Clear the bypass flag. |
| `sync-routes` | `<routesJSON>` | Replace the global `route_map` atomically. |
| `dns-update` | `<ip> <domainHash> <ttl>` | Write a single `dns_cache` entry. Break-glass only; the CoreDNS `dnsbpf` plugin is the real writer. |
| `gc-dns` | — | Iterate `dns_cache`, delete expired entries, print count. |
| `dump` | `<cgroupPath>` | Print the `container_map` entry for one cgroup (envoy/coredns/gateway IPs, net_addr/net_mask, host_proxy, egress_port). |
| `dump-routes` | `[--json]` | Dump the global `route_map` (every `{domain_hash, dst_port, l4_proto}` → `envoy_port`). |
| `dump-containers` | `[--json]` | Dump the full `container_map` (every cgroup → BPF container_config). |
| `dump-bypass` | `[--json]` | Dump the `bypass_map` (every cgroup → bypass flag). |
| `dump-dns` | `[--json]` | Dump the `dns_cache` (every IP → `{domain_hash, expire_ts}`). |
| `resolve` | `<hostname>` | Libc hostname lookup. |

## Why the break-glass tool exists

Every non-`init` subcommand calls `Manager.OpenPinned()` + `defer Close()` — they attach to maps the CP has already pinned, mutate them, and exit. Because the BPF maps are pinned and the programs are loaded into the kernel, the tool works even when the CP process is dead. This gives incident-response operators a way to observe and repair state without restarting the privileged container.

## Why it is NOT the primary interface

Docker-exec-as-an-interface between the firewall manager and a long-running container is a hack: every call pays ~100ms of exec overhead, requires string-encoded JSON marshalling, is untyped, is hard to test, and — load-bearingly — can't carry auth context. Typed gRPC over mTLS gives us structured requests, structured errors, per-method authorization via the CP's authz interceptor, and a path forward for additional clients (clawkerd, webui) that will never use docker exec. The primary interface for machine-to-machine CP calls is `AdminService` on the CP's TCP `AdminPort`.

## Security

Runs inside `clawker-controlplane` with `CAP_BPF + CAP_SYS_ADMIN` and bind mounts of `/sys/fs/bpf` + `/sys/fs/cgroup`. Every subcommand that accepts a `<cgroupPath>` passes it through `ebpf.CgroupID`, which runs `validateCgroupPath` (rejects empty, NUL/CR/LF, `..`, anything outside `/sys/fs/cgroup/`). This is the CodeQL `go/path-injection` sanitizer for the `os.Args[n] → file open` flow.

## Provenance

Binary lives at `internal/controlplane/cpboot/assets/ebpf-manager` after `make ebpf-binary` (plain `CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build` once `make ebpf` has staged the bpf2go bindings). The `clawker-controlplane` image bundles both `clawkercp` (the CMD) and `ebpf-manager` (this binary) via the `COPY` instructions in the Dockerfile recipe built by `cpImageDockerfile` in `internal/controlplane/cpboot/bootstrap.go`. Nothing about this binary is committed — the recipe (Makefile `BPF_APT_DEPS` + `gen.go`) is the build chain and the binary is regenerated on every build. See `../CLAUDE.md` for the BPF toolchain pin table.
