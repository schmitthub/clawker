# Config Command Package

Configuration management commands.

## Files

| File | Purpose |
|------|---------|
| `config.go` | `NewCmdConfig(f)` — parent command |

## Subcommands

- `config check` — validate `clawker.yaml` configuration (in `check/` subpackage). Supports `--file`/`-f` flag for validating a specific file. Runs `ProjectLoader` (catches unknown fields via `ErrorUnused`) + `Validator` (semantic checks). Prints warnings/errors to stderr with `ColorScheme` icons.

## Key Symbols

```go
func NewCmdConfig(f *cmdutil.Factory) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages.
