# Outstanding Features / TODOs

Top-level tracker for features and architectural improvements that are known but not yet implemented. Each entry has enough context for a fresh agent to pick it up.

---

## User notes on potential features

- we prob dont need root bypassing anymore btw. that was only for an escape hatch when we were managing routing with iptables. might be safer to keep root's egress locked down too now if it doesn't effect eBPF's routing. now we are doing toggling via containerid mappings right?
- Add a claude hook via managed settings in /etc/claude-code (https://code.claude.com/docs/en/settings#settings-files) to always require approval when editing clawker.yaml files might be a good defensive measure to prevent clawker image polution 

---

## 0. Release pipeline adaptation for Dockerfile.controlplane build chain

**Status:** release pipeline is currently broken on main's build system — **must land before the next tag push**
**Scope:** medium

Background: commits `a50ac9e4` + `5ce36b1c` on branch
`fix/project-egress-priority` replaced the host-native Go build of
`internal/firewall/assets/{ebpf-manager,coredns-clawker}` with a pinned
multi-stage `Dockerfile.controlplane` + `docker buildx build` extraction.
Nothing generated is committed anymore (no `clawker_*_bpfel.{go,o}`, no
firewall asset binaries). `make clawker-build` works end-to-end locally
because Make's dep graph triggers the pinned Docker build, which produces
the firewall stack binaries into `internal/firewall/assets/` before the
host-native `go build ./cmd/clawker` runs with them `go:embed`'d.

The release pipeline does NOT go through `make clawker-build`. It will
fail on the next tag push as-is.

### What's broken in `.github/workflows/release.yml`

| Step | Breakage |
|---|---|
| `Verify build` → `go build ./cmd/clawker` | `internal/firewall/assets/{ebpf-manager,coredns-clawker}` don't exist on the runner; `go:embed` fails at compile time |
| GoReleaser step (invokes goreleaser which runs `go generate ./...` + `go build`) | `go generate` needs pinned `clang` + `llvm` + `libbpf-dev` + `linux-libc-dev`; `go build` needs the embedded assets |

Everything downstream of the Go build is fine:
- `archives` / `homebrew_casks` / `sboms` / `signs` / `changelog` /
  `release` in `.goreleaser.yaml` all operate on already-built binaries
- The `actions/attest-build-provenance` step in release.yml that emits
  SLSA provenance on `dist/checksums.txt` works unchanged — the
  attestation covers whatever GoReleaser put in `dist/` transitively

### What's broken in `.goreleaser.yaml`

| Section | Breakage |
|---|---|
| `before.hooks: go generate ./...` | Runs on the ubuntu-latest runner with no BPF toolchain and no pinned versions. Even if the packages were installed, bypassing `Dockerfile.controlplane` defeats the reproducibility story |
| `builds: - id: clawker … main: ./cmd/clawker` | Missing `go:embed` assets. Also needs to run per-target-arch so each clawker binary embeds a matching-arch ebpf-manager + coredns-clawker (darwin/arm64 clawker embeds linux/arm64 sidecars, darwin/amd64 embeds linux/amd64, etc.) |

### Recommended fix: switch to `builder: prebuilt`

Cleanest path is to stop having GoReleaser build Go at all. Instead, the
Makefile produces all four cross-arch clawker binaries via the pinned
Docker chain, and GoReleaser just packages / signs / publishes them.

1. **Extend Dockerfile.controlplane or add a new stage** to support
   cross-compiling the ebpf-manager + coredns-clawker binaries for both
   `linux/amd64` and `linux/arm64` in a single build (using `TARGETARCH`
   build args). The current build already supports `--build-arg TARGETARCH=<arch>`
   via the Makefile's `BUILDX_TARGETARCH` — adapt it to be controllable
   per-invocation from the release Makefile target.

2. **Update `Makefile`** `clawker-build-linux` / `clawker-build-darwin`
   recipes to:
   - Call `make ebpf-binary BUILDX_TARGETARCH=amd64` for the linux/amd64
     and darwin/amd64 slots
   - Call `make ebpf-binary BUILDX_TARGETARCH=arm64` for the linux/arm64
     and darwin/arm64 slots
   - Same for `coredns-binary`
   - Then `go build` for the matching target OS with `GOOS=<os> GOARCH=<arch>`
   - Keep the existing "run sequentially, not in parallel, because the
     shared asset path gets stomped" structure (the existing comment at
     the top of `clawker-build-all` documents this)
   - Drop the outputs into `dist/<os>-<arch>/clawker` in a layout GoReleaser's
     `prebuilt` builder can pick up

3. **Replace `.goreleaser.yaml`'s `builds`** section with:
   ```yaml
   before:
     hooks: []  # no go generate — the Makefile target ran everything already
   builds:
     - id: clawker
       builder: prebuilt
       goos: [linux, darwin]
       goarch: [amd64, arm64]
       prebuilt:
         path: dist/{{ .Os }}-{{ .Arch }}/clawker
       binary: clawker
   ```

4. **Update `.github/workflows/release.yml`**:
   - Delete the `Verify build` step (or replace it with `make clawker-build-all`)
   - Add a step before GoReleaser: `make clawker-build-all` — this triggers
     the full pinned Docker chain + cross-compile all four binaries into
     `dist/<os>-<arch>/clawker`
   - GoReleaser picks them up via the `prebuilt` builder

### Things that keep working unchanged

- `attest-build-provenance` step (SLSA provenance on `checksums.txt`) —
  already wired in release.yml:66-69, operates on GoReleaser's output,
  unaffected by how the binaries got built. The existing `actions/attest-build-provenance@v4`
  emits a SLSA v1.0 predicate that transitively covers every embedded
  binary / BPF bytecode / everything `go:embed`'d into the clawker CLI
- `cosign sign-blob` of the checksum file — works unchanged
- SBOM generation (`sboms:` in goreleaser config) — works unchanged
- Homebrew tap push — works unchanged

### Test plan for the follow-up PR

- Local dry-run: `goreleaser release --clean --snapshot --skip=publish` against
  a Makefile-populated `dist/` tree — verify it produces archives for all
  four target platforms and doesn't try to `go generate`
- Push a test tag (e.g. `v0.0.0-rcN`) to a fork and watch the full workflow
  run through to `attest-build-provenance`
- Verify the SLSA attestation covers the final clawker binary and its
  transitively-embedded sidecars (can be inspected with
  `gh attestation verify` or `slsa-verifier`)

### SLSA attestation status (was originally this memory's contents)

Release provenance attestation is **already wired** via
`actions/attest-build-provenance@v4` in `release.yml:66-69`, covering
`dist/checksums.txt`. Once the Makefile-driven prebuilt flow above lands,
the attestation transitively covers the entire stack including BPF
bytecode, via `go:embed`. No additional SLSA work should be needed beyond
the release pipeline adaptation described above — unless we later want to
produce per-binary attestations instead of the current checksums-file
attestation, which is a nice-to-have but not required for SLSA L3.

---

## 0b. Clawker control plane / eliminate the long-running clawker-ebpf container

**Status:** planned (control plane work upcoming)
**Scope:** large

The `clawker-ebpf` container currently runs `sleep infinity` as a resident
RPC endpoint so the firewall manager can `docker exec` subcommands into it
(`init`, `enable`, `disable`, `sync-routes`, `bypass`, `unbypass`,
`resolve`). This is **not** a sidecar — the BPF programs themselves live
in kernel state (pinned under `/sys/fs/bpf/clawker/`) and would continue
enforcing even if `clawker-ebpf` were stopped.

Why it's currently a container: historical + macOS Docker Desktop quirks.
Running BPF operations from the host on macOS requires going through the
Docker Desktop Linux VM; having a container with the right capabilities
(`CAP_BPF` + `CAP_SYS_ADMIN`) + `/sys/fs/cgroup` bind-mount is the
cheapest way to get a privileged code-execution surface that works
identically across macOS and native Linux.

Why it's worth revisiting: a whole container existing purely to sleep and
serve exec calls is wasteful. Once the dedicated clawker control plane
daemon lands (separate from the CLI, which is a short-lived process), it
can own the privileged BPF surface directly — either running on the host
(native Linux) or inside a Docker Desktop VM-level helper (macOS). At
that point `clawker-ebpf` can be retired entirely and `init` / `enable` /
`disable` / `sync-routes` / `bypass` / `resolve` become direct calls from
the control plane to the kernel.

Dependencies: needs the control plane architecture to land first.

---

## ~~1. `firewall enable --agent` — wrong cgroup path~~ [RESOLVED]

Already fixed in commit `6a00a212` ("fix: firewall bypass/enable/disable name→ID resolution + bypass-stop re-enforce + ebpf shutdown speed + label-based test cleanup"). `(*Manager).Enable/Disable/Bypass` all call `resolveContainerID(ctx, ref)` at the top, which runs `ContainerInspect` on a name or short ID and returns the canonical long ID before `ebpfCgroupPath` builds the filesystem path. The memo entry predated that fix.

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

## ~~3. eBPF container should be a proper service (not `sleep infinity`)~~ [RESOLVED]

Delivered by the `feat/control-plane` branch. The `clawker-ebpf` container is replaced by `clawker-cp` (the clawker control plane), which:
- Runs `cmd/clawker-cp` as PID 1 — a long-lived Go daemon, no more `sleep infinity`.
- Calls `internal/controlplane/ebpf.Manager.Load()` once at startup and keeps link handles alive for the process lifetime. Pinning becomes pure crash-recovery insurance.
- Hot-reload pinning bug is fixed by construction: `Load()` runs exactly once, so `cleanupAllLinks()` never strips BPF programs from other running containers.
- `docker logs clawker-cp` shows structured zerolog JSON lines for every CP operation.
- Serves a typed gRPC `ControlPlaneService` over Unix domain socket with mTLS + OIDC + JWT authz. Host-side firewall manager dials it instead of `docker exec`.
- Short-lived `ebpf-manager` CLI stays in the image as a break-glass debug tool (see `internal/controlplane/ebpf/cmd/CLAUDE.md`), but is **not** the primary interface.

The control plane's auth shape (mTLS + OIDC + JWT with per-method scope enforcement) is final — future callers (clawkerd, webui, etc.) plug in as additional `ClientRegistration` entries and `methodScopes` entries without rewiring.

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
