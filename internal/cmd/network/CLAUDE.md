# Network Command Package

Network management for the dedicated clawker container network.

## Files

| File | Purpose |
|------|---------|
| `network.go` | `NewCmdNetwork(f)` — parent command |

## Subcommands

- `network create` — create clawker network
- `network inspect` — inspect network details
- `network list` / `network ls` — list clawker networks
- `network prune` — remove unused networks
- `network remove` / `network rm` — remove specific networks

## Key Symbols

```go
func NewCmdNetwork(f *cmdutil.Factory) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages. Clawker uses the clawker network for container communication and the monitoring stack.
