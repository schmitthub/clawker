# Loop Command Package

Autonomous Claude Code loops — repeated execution with circuit breaker protection.

## Files

| File | Purpose |
|------|---------|
| `loop.go` | `NewCmdLoop(f)` — parent command |

## Subcommands

- `loop run --agent <name> --prompt "..."` — start autonomous loop
- `loop status` — show session status
- `loop reset` — reset circuit breaker after stagnation
- `loop tui` — interactive dashboard

## Key Symbols

```go
func NewCmdLoop(f *cmdutil.Factory) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages. Circuit breaker logic (max loops, stagnation threshold, timeouts) is configurable in `clawker.yaml` under the `ralph` key (will be renamed to `loop` in Task 3). Agent signals completion via `RALPH_STATUS` block in output (will be renamed to `LOOP_STATUS` in Task 9).
