# Control Plane Initiative — Sub-Initiative Context

> This file is lazy-loaded by Claude Code when reading files in this directory.
> For the full roadmap, branches, invariants, and merge strategy, see the master spec: `../control-plane-initiative.md`

## Current State

> Last updated: 2026-04-12

- **Branch**: `feat/control-plane` (Branch 1 work in progress)
- **Status**: Initiative spec in review
- **Main branch**: eBPF-based firewall with `clawker-ebpf` sleep-infinity container, PID-file daemons for firewall/hostproxy/socketbridge

## Codebase Snapshot

### The God Object: `internal/firewall/manager.go` (1816 lines, 77 responsibilities)
- Creates CP, Envoy, CoreDNS containers
- Generates Envoy/CoreDNS configs
- Manages rules store, MITM certs, Docker network
- Runs eBPF operations via gRPC (after Branch 1)
- Holds lazy gRPC client to CP
- 15 public interface methods, 31 private methods, 5 test seams

### Four Independent Daemon Processes
1. **Firewall daemon** (`internal/firewall/daemon.go`) — health loop + container watcher, PID file
2. **Hostproxy daemon** (`internal/hostproxy/`) — HTTP server for browser auth + credential forwarding, PID file
3. **Socketbridge daemons** (`internal/socketbridge/`) — one per container, muxrpc over docker exec, PID files
4. **CP container** (`cmd/clawker-cp/`) — long-lived gRPC service (different pattern, Docker container)

### Container Start Flow (current)
```
BootstrapServicesPreStart:
  → firewall.AddRules()
  → firewall.EnsureDaemon()        ← PID-file daemon
  → firewall.WaitForHealthy()      ← polls Envoy + CoreDNS HTTP health
  → hostProxy.EnsureRunning()      ← PID-file daemon
docker.ContainerStart()
BootstrapServicesPostStart:
  → firewall.Enable(containerID)   ← gRPC to CP: attach eBPF
  → socketBridge.EnsureBridge()    ← spawns per-container daemon
```

### Factory Wiring
- `f.Firewall(ctx)` — lazy, uses raw moby client (NOT whail), creates `firewall.NewManager`
- `f.HostProxy()` — lazy singleton
- `f.SocketBridge()` — lazy singleton
- `f.ControlPlane()` — does not exist yet (Branch 2 adds it)

## Reference Pointers

| What | Where |
|------|-------|
| Master initiative spec | `../control-plane-initiative.md` |
| Branch specs (created at kickoff) | `branch-N-*.md` in this directory |
| Architecture docs | `.claude/docs/ARCHITECTURE.md`, `.claude/docs/DESIGN.md` |
| Key concepts index | `.claude/docs/KEY-CONCEPTS.md` |
| CP brainstorm (reference, not authority) | `.serena/memories/brainstorm_the-controlplane-and-clawkerd` |
| Firewall package docs | `internal/firewall/CLAUDE.md` |
| CP package docs | `internal/controlplane/CLAUDE.md` |
