# eBPF Firewall — Docker Desktop UAT Progress

## End Goal
Replace iptables with eBPF cgroup programs for per-container egress traffic routing to Envoy/CoreDNS. The feature code was built in a prior session and committed to branch `fix/project-egress-priority`. This session focused on making it actually runnable for UAT (end-user acceptance testing).

## Branch
`fix/project-egress-priority`

## Background Context
- The original iptables approach hit a dead end: port-only matching can't distinguish github.com:22 from gitlab.com:22
- eBPF `cgroup/connect4` fires after DNS resolution, has the resolved IP, and routes per-domain
- Five BPF programs: connect4, sendmsg4, recvmsg4, connect6, sendmsg6, sock_create
- BPF source: `internal/ebpf/bpf/clawker.c`, shared headers: `internal/ebpf/bpf/common.h`
- Go manager: `internal/ebpf/manager.go`, types: `internal/ebpf/types.go`
- CLI entry: `internal/ebpf/cmd/main.go` (subcommands: init, enable, disable, bypass, unbypass, dns-update, gc-dns, dump)
- Firewall manager integration: `internal/firewall/manager.go`

## What Was Done This Session

### 1. eBPF Manager Container Image [DONE]
- Created `Dockerfile.ebpf` at repo root (multi-stage Go build)
- Added `internal/firewall/ebpf_embed.go` — embeds pre-compiled linux binary via `//go:embed assets/ebpf-manager`
- Added `internal/firewall/assets/` directory (gitignored, binary built by Makefile)
- Makefile: `ebpf-binary` target cross-compiles for linux, wired as dependency of `clawker-build`
- Makefile: `clawker-build-linux/darwin/windows` targets build matching arch ebpf binary before clawker
- Firewall manager: `ensureEbpfImage()` builds Docker image on-demand from embedded binary (no registry needed)
- `ebpfBuildContext()` creates tar with inline Dockerfile (`FROM alpine:3.21` + `COPY ebpf-manager`) + embedded binary
- Image tagged `clawker-ebpf:latest`, cached locally

### 2. Firewall Manager Fixes [DONE]
- `IsRunning()` now checks all 3 containers (envoy, coredns, ebpf)
- `Stop()` tears down all 3 containers
- `Status()` includes ebpf container in running check
- `Enable()` now ensures ebpf image + container exist before exec (idempotent)
- `Enable()` calls `init` before `enable` to ensure programs are loaded
- Static IP: ebpf container gets `.202` (was wrongly `.4`, collided with DHCP range)
- Cgroup path: auto-detects `cgroupfs` vs `systemd` driver via `docker info` API
  - cgroupfs (Docker Desktop): `/sys/fs/cgroup/docker/<containerID>`
  - systemd (native Linux): `/sys/fs/cgroup/system.slice/docker-<containerID>.scope`
- `ebpfExec()` now attaches both stdout and stderr

### 3. BPF Program Fixes [DONE]
- Consolidated IPv6 loopback check inline (was helper function — BPF verifier rejected ctx pointer pass)
- Removed stale individual .c files (connect4.c, connect6.c, sendmsg4.c, sendmsg6.c, sock_create.c)
- Added `recvmsg4` program — rewrites source address on UDP DNS responses from CoreDNS back to 127.0.0.11 (paired with sendmsg4)
- IPv4-mapped IPv6 addresses (`::ffff:x.x.x.x`) allowed in connect6/sendmsg6 (dual-stack sockets)

### 4. BPF Pin Lifecycle [DONE]
- `Load()` now removes stale program pins before re-pinning (was skipping with `os.ErrExist`)
- `Enable()` calls `cleanupLinks(cgroupID)` before attaching — removes stale link pins
- `cleanupLinks()` method added — closes in-memory links + removes pinned link files

### 5. Debug Infrastructure [DONE]
- Added `dump` subcommand to ebpf-manager CLI — dumps container_map entry for a cgroup
- Added `LookupContainer()` method to ebpf Manager

## ROOT CAUSE FOUND — Byte Order Bug (NOT ctx writes)

**Resolved 2026-04-09.** The "ctx write blocker" was a byte order bug in Go's `IPToUint32` / `CIDRToAddrMask`.

### The bug
`binary.BigEndian.Uint32(ip)` produces the "logical" big-endian uint32 value (e.g., 0xAC1200C8 for 172.18.0.200). But `ctx->user_ip4` in BPF is network-order bytes read as a NATIVE uint32 — on little-endian ARM64, that's 0xC80012AC. Every IP comparison and every ctx write was using the wrong value.

### The fix
Changed `IPToUint32`, `Uint32ToIP`, and `CIDRToAddrMask` to use `binary.NativeEndian` instead of `binary.BigEndian`. NativeEndian reads IP bytes the same way the CPU loads `ctx->user_ip4` from memory.

### What confused the previous investigation
- Root (uid 0) returns before any map/ctx access → always worked
- Non-root hit map lookups with wrong byte-order values → subnet check failed → all traffic fell through to the catch-all → got redirected to Envoy with garbage IPs → connection timeouts
- This LOOKED like "ctx writes break connections" but was actually "all comparisons fail, all traffic misroutes"

### How it was found
- Used `bpftool map dump` on metrics_map to see that subnet traffic (172.18.0.7:3000) hit the catch-all instead of the subnet check
- Traced back to `is_in_subnet` returning false because map values and ctx values were in different byte orders

## DNS Routing Order
The correct order in connect4 is:
1. DNS (port 53) → redirect to CoreDNS (BEFORE loopback check, because Docker embedded DNS at 127.0.0.11 is loopback)
2. Loopback → allow
3. Subnet → allow
4. Host proxy → allow
5. Gateway lockdown → redirect to Envoy
6. Non-DNS UDP → deny
7. TCP per-domain routing via dns_cache map
8. Catch-all → Envoy egress listener

But this order requires ctx writes (step 1), which triggers the blocker above.

## Files Modified This Session
- `internal/firewall/manager.go` — major changes (ebpf image build, enable flow, cgroup detection, stop/status/isrunning)
- `internal/firewall/ebpf_embed.go` — NEW (go:embed for ebpf-manager binary)
- `internal/ebpf/bpf/clawker.c` — DNS ordering, recvmsg4, IPv6 fixes, loopback inline
- `internal/ebpf/manager.go` — pin lifecycle, cleanupLinks, LookupContainer, recvmsg4 support
- `internal/ebpf/cmd/main.go` — dump subcommand
- `Dockerfile.ebpf` — NEW
- `Makefile` — ebpf-binary target, wired into build
- `.gitignore` — ebpf-manager binary
- `.dockerignore` — already existed, adequate

## Uncommitted State
All changes are uncommitted. The BPF source (`clawker.c`) currently has the DNS-before-loopback order with ctx writes — which causes the blocker. The `.o` files need regeneration after any .c changes (`go generate ./internal/ebpf/...`).

## IMPERATIVE
**Always check with the user before proceeding with the next todo item.** Do not assume which approach they want to take for the ctx write blocker. If all work is complete and the feature is working, ask the user if they want to delete this memory.
