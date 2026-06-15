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
| `internal/bundler/` | Dockerfile generation, flavor selection, npm version resolution (leaf — no docker import) |
| `internal/semver/` | General semver utilities — parse/compare/sort/match plus v-tolerant string compare; leaf, stdlib only, safe across the DAG (bundler, changelog, update) |
| `internal/project/` | Project registration in user registry |
| `internal/containerfs/` | Host Claude config preparation — tar archives for config volume (leaf — config types only, no docker runtime) |
| `internal/docker/` | Container naming, image resolution, image building (`Builder`, `BuildDefaultImage`), Docker middleware |
| `internal/controlplane/` | CP daemon core: startup orchestrator, Ory auth stack, AdminService composition, agent watcher |
| `internal/controlplane/cpboot/` | Host-side CP lifecycle: `EnsureRunning`/`Stop`/`CPRunning`, `BuildCPContainerConfig`, `Manager` interface + `NewManager`, embedded clawker-cp + ebpf-manager binaries. Split from `internal/controlplane/` so `cmd/clawker-cp` can import the parent daemon package without dragging in `go:embed` directives for its own binary |
| `internal/controlplane/firewall/` | Firewall `Handler` (13 RPCs), `Stack` (Envoy+CoreDNS lifecycle), Envoy+CoreDNS config generators, certificate management, rules store, network discovery, cgroup helpers |
| `internal/controlplane/firewall/ebpf/` | eBPF loader + `Manager` (cgroup programs, pinned maps); break-glass `ebpf-manager` CLI under `internal/controlplane/firewall/ebpf/cmd/` |
