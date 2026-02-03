# Monitor Command Package

Manage local observability stack (OpenTelemetry, Jaeger, Prometheus, Grafana).

## Files

| File | Purpose |
|------|---------|
| `monitor.go` | `NewCmdMonitor(f)` — parent command |

## Subcommands

- `monitor init` — scaffold monitoring configuration files
- `monitor up` — start observability stack
- `monitor down` — stop observability stack
- `monitor status` — show stack status

## Key Symbols

```go
func NewCmdMonitor(f *cmdutil.Factory) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages.
