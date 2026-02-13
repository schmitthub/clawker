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
- `volume prune` — remove unused volumes
- `volume remove` / `volume rm` — remove specific volumes

## Key Symbols

```go
func NewCmdVolume(f *cmdutil.Factory) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages. Volumes persist workspace data (snapshot mode), configuration, and command history. Naming: `clawker.project.agent-purpose` (e.g., `clawker.myapp.dev-workspace`).
