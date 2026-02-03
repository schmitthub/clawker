# Config Command Package

Configuration management commands.

## Files

| File | Purpose |
|------|---------|
| `config.go` | `NewCmdConfig(f)` — parent command |

## Subcommands

- `config check` — validate `clawker.yaml` configuration (in `check/` subpackage)

## Key Symbols

```go
func NewCmdConfig(f *cmdutil.Factory) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages.
