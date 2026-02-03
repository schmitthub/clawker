# Image Command Package

Image management parent command.

## Files

| File | Purpose |
|------|---------|
| `image.go` | `NewCmdImage(f)` — parent command |

## Subcommands

- `image build` — build project image
- `image inspect` — inspect image details
- `image list` / `image ls` — list clawker images
- `image prune` — remove unused images
- `image remove` / `image rm` — remove specific images

## Key Symbols

```go
func NewCmdImage(f *cmdutil.Factory) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages.
