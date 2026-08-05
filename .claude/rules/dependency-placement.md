---
paths:
  - "internal/**"
  - "cmd/**"
---

# Dependency Placement Decision Tree

When adding a new heavy dependency or command helper, use this decision tree:

```
"Where does my heavy dependency go?"
              │
              ▼
Can it be constructed at startup,
before any command runs?
              │
       ┌──────┴──────┐
       YES            NO (needs CLI args, runtime context)
       │              │
       ▼              ▼
  3+ commands?    Lives in: internal/<package>/
       │          Constructed in: run function
  ┌────┴────┐     Tested via: inject mock on Options
  YES       NO
  │         │
  ▼         ▼
FACTORY   OPTIONS STRUCT
FIELD     (command imports package directly)
```

## Rules

- Implementation always lives in `internal/<package>/` — never in `cmdutil/`
- The only question is **who constructs it**: `factory.New()` at startup, or each command's run function
- `cmdutil/` contains only: Factory struct (DI container), output utilities, arg validators
- Heavy command helpers (resolution, building, registration) live in their own `internal/` packages

## Current Package Layout

| Package | Contains |
|---------|----------|
| `internal/cmdutil/` | Factory struct, output utilities, arg validators (imports `internal/docker` only for the `*docker.Client` closure type on `Factory`) |
| `internal/bundler/` | Dockerfile generation, harness version resolution (leaf — no docker import) |
| `internal/project/` | Project registration in user registry |
| `internal/containerfs/` | Host Claude config preparation — tar archives for config volume (leaf — config types only, no docker runtime) |
| `internal/docker/` | Container naming, image resolution, image building (`Builder`, `Build`), Docker middleware |
| `internal/controlplane/` | CP daemon core: startup orchestrator, Ory auth stack, AdminService composition, agent watcher |
| `controlplane/manager/` | Host-side CP lifecycle: the narrow `Manager` interface (`Start` = idempotent bringup, surfacing `*CPSOSError` for callers to assist) + `NewManager`, `Stop`/`CPRunning`, `BuildCPContainerConfig`, embedded clawkercp + ebpf-manager + bpffs-delegate binaries. Split from `internal/controlplane/` so `cmd/clawkercp` can import the parent daemon package without dragging in `go:embed` directives for its own binary |
| `controlplane/firewall/` | Firewall `Handler` (13 RPCs), `Stack` (Envoy+CoreDNS lifecycle), Envoy+CoreDNS config generators, certificate management, `EgressRulesStore`/`RouteIdentityStore` facades, network discovery, cgroup helpers |
| `controlplane/firewall/ebpf/` | eBPF loader + `Manager` (cgroup programs, pinned maps); break-glass `ebpf-manager` CLI under `controlplane/firewall/ebpf/cmd/` |
