# Control Plane Initiative — Sub-Initiative Context

> This file is lazy-loaded by Claude Code when reading files in this directory.
> For the full roadmap, branches, invariants, and merge strategy, see the master spec: `../control-plane-initiative.md`

## Current State

> Last updated: 2026-04-14

- **Branch**: `feat/firewall-cp-migration` (Branch 2 complete; merge pending final host-side review)
- **Status**: Branches 1 + 2 landed. Branch 3 (daemon consolidation) pending.
- **Main branch**: CP-owned firewall. `internal/firewall/` deleted. 13-method scope-corrected AdminService. CLI talks to the CP via `f.AdminClient(ctx)` — mTLS + OAuth2 JWT.

## Codebase Snapshot (post-Branch-2)

### Firewall ownership inverted
- `internal/firewall/` — **deleted**. No host-side daemon, no PID file, no `FirewallManager` interface.
- `internal/controlplane/firewall/` — CP firewall domain. `Handler` serves 13 RPCs; `Stack` owns Envoy + CoreDNS container lifecycle; `ebpf/` holds the loader + pinned maps.
- `internal/controlplane/` — CP core. `bootstrap.go` is the host-side `EnsureRunning`/`Stop`; `watcher.go` is the `AgentWatcher` that drives drain-to-zero self-shutdown (INV-B2-007); `startup.go` is the CP-side orchestrator.

### Daemon inventory (post-B2)
1. **CP container** (`cmd/clawker-cp/`) — long-lived gRPC service, loads eBPF once at boot, owns firewall state, self-shuts-down on drain-to-zero.
2. **Hostproxy daemon** (`internal/hostproxy/`) — HTTP server for browser auth + credential forwarding, PID file. (Branch 3 target.)
3. **Socketbridge daemons** (`internal/socketbridge/`) — one per container, muxrpc over docker exec, PID files. (Branch 3 target.)

### Container Start Flow (post-B2)
```
BootstrapServicesPreStart:
  → hostProxy.EnsureRunning()         ← PID-file daemon (Branch 3 target)
BootstrapServicesPostStart (via f.AdminClient):
  → adminClient.FirewallInit()        ← idempotent stack-up (brings CP up transparently on first call)
  → adminClient.FirewallAddRules()    ← project rules
  → adminClient.FirewallEnable()      ← per-container enroll (drift-guarded, INV-B2-016)
  → socketBridge.EnsureBridge()       ← spawns per-container daemon
docker.ContainerStart()                ← happens between PreStart and PostStart inside shared.ContainerStart()
```

### Factory Wiring (post-B2)
- `f.AdminClient(ctx)` — lazy `adminv1.AdminServiceClient`. First call triggers `controlplane.EnsureRunning` (package-level seam `ensureRunning`), then `auth.DialCPAdmin` with mTLS + OAuth2 + keepalive. Rebuilds `grpc.ClientConn` only on `TransientFailure`/`Shutdown`.
- `f.ControlPlane()` — lazy `controlplane.Manager` (host-side CP lifecycle noun). Used by the break-glass `clawker controlplane up/down/status` verbs.
- `f.HostProxy()` — lazy singleton (unchanged; Branch 3 target).
- `f.SocketBridge()` — lazy singleton (unchanged; Branch 3 target).
- `f.Firewall(ctx)` — **removed**.

## Reference Pointers

| What | Where |
|------|-------|
| Master initiative spec | `../control-plane-initiative.md` |
| Branch specs | `branch-N-*.md` in this directory |
| Architecture docs | `.claude/docs/ARCHITECTURE.md`, `.claude/docs/DESIGN.md` |
| Key concepts index | `.claude/docs/KEY-CONCEPTS.md` |
| CP package docs | `internal/controlplane/CLAUDE.md` |
| CP firewall domain docs | `internal/controlplane/firewall/CLAUDE.md` |
| CP eBPF docs | `internal/controlplane/firewall/ebpf/CLAUDE.md` |
| Branch 2 plan (active until merge) | `.serena/memories/cp-initiative-branch-2-firewall-migration-plan` |
| Initiative status memory | `.serena/memories/cp-initiative-status` |
