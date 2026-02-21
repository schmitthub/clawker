# Config Command Package

Configuration management commands.

## Files

| File | Purpose |
|------|---------|
| `config.go` | `NewCmdConfig(f)` — parent command |

## Subcommands

- `config check` — validate `clawker.yaml` configuration (in `check/` subpackage). Supports `--file`/`-f` flag for validating a specific file. Reads file content and passes to `config.ReadFromString()` for YAML parsing validation. Prints errors to stderr with `ColorScheme` icons.

## Key Symbols

```go
func NewCmdConfig(f *cmdutil.Factory) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages.
