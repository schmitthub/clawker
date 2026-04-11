# Outstanding Features / TODOs

Top-level tracker for features and architectural improvements that are known but not yet implemented. Each entry has enough context for a fresh agent to pick it up.

---

## User notes on potential features

- we prob dont need root bypassing anymore btw. that was only for an escape hatch when we were managing routing with iptables. might be safer to keep root's egress locked down too now if it doesn't effect eBPF's routing. now we are doing toggling via containerid mappings right?
- Add a claude hook via managed settings in /etc/claude-code (https://code.claude.com/docs/en/settings#settings-files) to always require approval when editing clawker.yaml files might be a good defensive measure to prevent clawker image polution 

---

## 0. Finish the BPF reproducibility chain (pin digest + SLSA attestation)

**Status:** infrastructure in place, two gaps remain
**Scope:** small (pin) + medium (SLSA)

The DRY refactor of `internal/ebpf/bpf/clawker.c` + `common.h` also dropped the committed `vmlinux.h` (clawker is non-CO-RE, UAPI types are sufficient) and introduced `Dockerfile.bpf-builder` + `make bpf-regenerate` / `make bpf-verify` + a CI reproducibility gate in `.github/workflows/test.yml`. See `internal/ebpf/REPRODUCIBILITY.md` for the full chain.

Two loose ends from that work:

1. **Pin the base image digest.** `Dockerfile.bpf-builder` currently has `FROM debian:bookworm-slim@sha256:PIN_ME_BEFORE_MERGE`. The CI `bpf-reproducibility` job detects the placeholder and emits a warning instead of running the verification, so the rest of the refactor could land without waiting on the pin. Before the next BPF change lands on trunk, fill in the real digest using the recipe in `internal/ebpf/REPRODUCIBILITY.md` ("Updating pinned inputs" → step 1). Also re-verify the `CLANG_VERSION`, `LIBBPF_DEV_VERSION`, `LINUX_LIBC_DEV_VERSION` ARGs still resolve in the chosen digest (step 2 of the same section).

2. **SLSA provenance attestation on release binaries.** The BPF bytecode is now reproducible from pinned sources, but the released `clawker` binary itself should carry a SLSA attestation so end users can verify the chain. Check `.github/workflows/release.yml` — if it doesn't already emit SLSA attestations via the SLSA GitHub generator action, add it. The BPF `.o` is `go:embed`'d, so the binary attestation transitively covers the BPF bytecode. This is the last mile to an end-to-end SLSA L3 story for clawker's kernel-level surface.

Files touched by the refactor (for context when picking this up):
- `internal/ebpf/bpf/clawker.c`, `internal/ebpf/bpf/common.h` (refactor)
- `internal/ebpf/clawker_*_bpfel.{go,o}` (regenerated)
- `internal/ebpf/gen.go` (new cflags for UAPI headers)
- `Dockerfile.bpf-builder` (new pinned builder)
- `Makefile` (new `bpf-builder-image`, `bpf-regenerate`, `bpf-verify` targets)
- `internal/ebpf/REPRODUCIBILITY.md` (provenance doc)
- `.github/workflows/test.yml` (`bpf-reproducibility` job)
- `CLAUDE.md` (version pinning table entry)

---

## 1. `firewall enable --agent` — wrong cgroup path

**Status:** broken (bug)
**Scope:** small

**Problem:** `clawker firewall enable --agent <name>` passes the container NAME to `(*Manager).Enable`, which passes it verbatim to `ebpfCgroupPath` → `/sys/fs/cgroup/docker/<container_name>`. The real cgroup path uses container ID, not name:

```
ebpf-manager enable: getting cgroup ID for /sys/fs/cgroup/docker/clawker.clawker.ebpf:
  opening cgroup: open /sys/fs/cgroup/docker/clawker.clawker.ebpf: no such file or directory
```

**Files:**
- `internal/cmd/firewall/enable.go` — calls `fwMgr.Enable(ctx, containerName)` at line ~78
- `internal/firewall/manager.go` — `(*Manager).Enable(ctx, containerID string)` passes containerID to `ebpfCgroupPath`
- `internal/firewall/manager.go` — `ebpfCgroupPath(driver, containerID)` builds `/sys/fs/cgroup/docker/<id>`

**Fix sketch:**
- In `enableRun` (and the similar `disableRun`, `bypassRun`), resolve the container name to its Docker container ID via `docker.Client.ContainerInspect` before calling `fwMgr.Enable/Disable/Bypass`.
- Or change the `Enable/Disable/Bypass` signatures to accept a name AND do the lookup internally via the firewall manager's raw moby client.
- The normal container start path already uses container ID (from `docker create`), so it works correctly — only the CLI re-enable path is broken.

**Where the working path does it right:** `internal/cmd/container/shared/` — container start has the real container ID from `ContainerCreate` and passes it directly.

---

## 2. Native IPv6 support

**Status:** not supported (documented limitation)
**Scope:** large

**Current behavior:**
- `cgroup/connect4` handles AF_INET sockets (full enforcement).
- `cgroup/connect6` handles AF_INET6 sockets: IPv4-mapped (`::ffff:x.x.x.x`) gets full IPv4 routing; **native IPv6 is denied** (`return 0`).
- `cgroup/sendmsg6` + `cgroup/recvmsg6`: same split — IPv4-mapped UDP works, native IPv6 UDP denied.

**Why it works in practice:** Most programs use dual-stack sockets + A records, so they hit the IPv4-mapped path. But `curl -6 https://github.com` or `curl https://[2606:4700::1]` is blocked entirely.

**What full support needs:**
1. **IPv6 Envoy listeners** — Envoy needs to listen on IPv6 on the egress port + per-domain TCP ports.
2. **IPv6-keyed dns_cache** — current map uses `__u32` (IPv4) as key. Need a separate `dns_cache6` with 16-byte IPv6 key, or a union type.
3. **IPv6 Envoy cluster endpoints** — LOGICAL_DNS clusters need to support IPv6 upstream resolution.
4. **IPv6 config in container_config** — add `envoy_ip6`, `coredns_ip6`, `gateway_ip6`, `net_addr6`/`net_mask6`. Current struct is IPv4-only.
5. **IPv6 subnet/loopback/gateway checks in connect6** — native IPv6 path needs the same allow-through logic as connect4 (loopback, clawker-net subnet, gateway lockdown).
6. **IPv6 dnsbpf plugin handling** — the `dnsbpf` plugin currently only writes A records (`dns.A`). Needs to also handle `dns.AAAA` and write to the IPv6 dns_cache.
7. **Config schema** — `firewall` network creation needs to allocate IPv6 subnets + assign IPv6 static IPs to Envoy/CoreDNS/eBPF containers.

**Complexity:** Touches almost every part of the firewall stack. Probably a multi-task initiative.

**Files to start:**
- `internal/ebpf/bpf/common.h` — map definitions, container_config struct
- `internal/ebpf/bpf/clawker.c` — connect6/sendmsg6/recvmsg6
- `internal/ebpf/types.go` + manager.go — IPv6 helpers + sync
- `internal/firewall/manager.go` — network setup, container config
- `internal/firewall/envoy.go` — IPv6 listener + cluster config
- `internal/dnsbpf/dnsbpf.go` — AAAA record handling

---

## 3. eBPF container should be a proper service (not `sleep infinity`)

**Status:** works but opaque
**Scope:** medium

**Current behavior:**
- The eBPF manager container runs `CMD ["sleep", "infinity"]` (see `internal/firewall/manager.go` `ebpfImageSpec` inline Dockerfile).
- All BPF operations happen via `docker exec clawker-ebpf ebpf-manager <subcommand>` — ebpf-manager is a short-lived per-command binary.
- **No logs.** `docker logs clawker-ebpf` returns empty. Debugging BPF problems requires manual exec + bpftool or adding print statements.
- All logging from per-command invocations goes to that invocation's stderr which gets captured in the parent clawker.log, NOT in the container's own logs.

**What we want:**
- The entrypoint should be a long-running `ebpf-manager serve` (or similar) process that:
  - Runs `Load()` once at startup (pin programs + maps)
  - Keeps the Go process alive to serve RPC-style commands from the firewall manager (init, enable, disable, sync-routes, bypass, dns-update, etc.)
  - Logs every operation with structured zerolog to stdout/stderr (visible via `docker logs`)
  - Optionally exposes a health endpoint (HTTP or Unix socket) so the firewall daemon can probe it instead of relying on container state
- The firewall manager should talk to the daemon via a stable transport (HTTP over Unix socket? named pipe? gRPC over exec?) instead of `docker exec` for every operation.

**Why it matters:**
- **Debuggability:** `docker logs clawker-ebpf` should show attach/detach events, errors, metric updates, dns-update calls.
- **Link lifetime:** Currently, `link.AttachCgroup` returns a Go link object. We pin it to the filesystem (`link_<prog>_<cgroup>`) so it survives process exit — but the pin is the only thing keeping the attachment alive, and pin files are fragile (the hot-reload bug we hit on 2026-04-10 was caused by `cleanupAllLinks()` wiping pins from a second `init` call). With a long-running daemon, the Go link objects would stay in-memory for the process lifetime, and we could use pinning purely as a crash-recovery mechanism.
- **Performance:** Every `docker exec` is ~100ms of overhead. A single Unix-socket RPC would be ~1ms.
- **Atomic operations:** Multi-step updates (e.g., "disable container X and remove its route") could be transactional.

**Files to touch:**
- `internal/ebpf/cmd/main.go` — add a `serve` subcommand that runs the long-lived loop
- `internal/ebpf/manager.go` — may need to expose method signatures for the RPC layer
- `internal/firewall/manager.go` — replace `ebpfExec(ctx, ...)` with a client call to the daemon
- `internal/firewall/manager.go` `ebpfImageSpec` — change the inline Dockerfile CMD from `sleep infinity` to `ebpf-manager serve`
- New: RPC server/client — decide on transport (suggest: HTTP over Unix domain socket bind-mounted from host, keeps it simple and debuggable with `curl`)

---

## 4. OTel monitoring from eBPF (container traffic + eBPF logs)

**Status:** not started
**Scope:** medium

**What we want:**
1. **Per-container traffic metrics** sourced from the BPF `metrics_map`:
   - Bytes/packets per `{cgroup_id, domain_hash, dst_port, action}`
   - Export as OTel metrics with labels: `container`, `agent`, `project`, `domain`, `port`, `action` (allow/deny/bypass)
   - Cardinality bounded by active rules × running containers
2. **eBPF program logs** — events from the BPF programs themselves (verifier output, `bpf_printk` events via tracefs, attach/detach events) forwarded to the clawker logging stack (file + OTel bridge).

**Existing infrastructure:**
- `internal/ebpf/bpf/common.h` already defines a `metrics_map` with `MetricKey {cgroup_id, domain_hash, dst_port, action}` → counter struct. The BPF programs already call `metric_inc(...)` at every decision point (connect4, sendmsg4, etc.).
- `internal/monitor/` has Grafana + Prometheus + Loki templates — the monitoring stack exists.
- `internal/logger` has an OTel bridge already wired (see `logger.Logger` Factory noun) that forwards clawker's structured logs.
- The firewall daemon already publishes CoreDNS query logs + Envoy access logs to Loki via Promtail (see `.claude/rules/envoy.md` and `internal/firewall/CLAUDE.md`).

**What's missing:**
- Go-side reader that scrapes `metrics_map` on an interval (e.g., 15s), computes deltas, and exports as OTel gauges/counters.
- A new BPF map or ring buffer for structured event logs (attach/detach/deny events) that userspace can consume.
- Wiring into the Grafana dashboard (see `internal/monitor/grafana/dashboards/`) — new panels for per-container traffic.

**Design questions (to resolve during implementation):**
- Push vs pull: should the eBPF daemon push metrics to an OTel collector, or expose a `/metrics` Prometheus endpoint? The existing Envoy + CoreDNS both expose Prometheus endpoints, so Prometheus scrape is probably more consistent.
- Cardinality control: do we collapse per-domain metrics above N distinct domains per container?
- Log transport: BPF ring buffer (`BPF_MAP_TYPE_RINGBUF`) for event stream, or just poll the metrics_map and synthesize events from deltas?

**Files to touch:**
- `internal/ebpf/bpf/common.h` — maybe add a ring buffer for structured events
- `internal/ebpf/bpf/clawker.c` — optionally emit ring buffer events on key actions
- `internal/ebpf/manager.go` — new `ScrapeMetrics()` method reading `metrics_map`
- `internal/ebpf/cmd/main.go` — new `metrics` subcommand (or part of the `serve` daemon from #3)
- `internal/monitor/` — dashboard updates
- Tie into the #3 long-running daemon naturally — scraping loop lives there

---

## Cross-cutting notes

**Related:** Items 3 and 4 are natural companions. The long-running eBPF service (#3) is the obvious home for the metrics scraper and event stream (#4). Both should be tackled together, or #3 should land first.

**Not in scope here:** Any firewall changes tied to specific egress rule features (path rules, wildcards, IP ranges, etc.) — those are tracked separately via the regular feature pipeline.

**Source of truth for architecture:** `.claude/docs/ARCHITECTURE.md` (§ `internal/firewall`, `internal/dnsbpf`, `internal/ebpf`) and `.claude/docs/DESIGN.md` §7.2 were refreshed in commit `24090e17` (2026-04-10) to reflect the current state.
