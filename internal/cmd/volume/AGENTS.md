# Volume Command Package

Volume management for persistent workspace data and state.

## Files

| File | Purpose |
|------|---------|
| `volume.go` | `NewCmdVolume(f)` — parent command |

## Subcommands

- `volume create` — create clawker volume
- `volume inspect` — inspect volume details
- `volume list` / `volume ls` — list clawker volumes
- `volume prune` — remove unused agent volumes (default); `-a`/`--all` extends to all clawker-managed volumes (infrastructure included)
- `volume remove` / `volume rm` — remove specific volumes

## Key Symbols

```go
func NewCmdVolume(f *cmdutil.Factory) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages. Volumes persist workspace data (snapshot mode), configuration, and command history. Naming: agent volumes are `clawker.project.agent-purpose` (e.g. `clawker.myapp.dev-workspace`); harness-scoped volumes — bundle-declared persisted dirs (both shipped harnesses declare `config`) plus the clawker lifecycle volume — are keyed by harness as `clawker.project.agent-harness.volume` (e.g. `clawker.myapp.dev-claude.config`).
