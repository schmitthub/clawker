# Ralph Command Package

Autonomous Claude Code loops ("Ralph Wiggum" technique — repeated `--continue` runs).

## Files

| File | Purpose |
|------|---------|
| `ralph.go` | `NewCmdRalph(f)` — parent command |

## Subcommands

- `ralph run --agent <name> --prompt "..."` — start autonomous loop
- `ralph status` — show session status
- `ralph reset` — reset circuit breaker after stagnation
- `ralph tui` — interactive dashboard

## Key Symbols

```go
func NewCmdRalph(f *cmdutil.Factory) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages. Circuit breaker logic (max loops, stagnation threshold, timeouts) is configurable in `clawker.yaml` under the `ralph` key. Agent signals completion via `RALPH_STATUS` block in output.
