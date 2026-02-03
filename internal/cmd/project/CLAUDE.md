# Project Command Package

Project lifecycle management (initialization and registration).

## Files

| File | Purpose |
|------|---------|
| `project.go` | `NewCmdProject(f)` — parent command |

## Subcommands

- `project init` — initialize new project in current directory (creates `clawker.yaml`)
- `project register` — register existing project in user's registry (`~/.local/clawker/projects.yaml`)

## Key Symbols

```go
func NewCmdProject(f *cmdutil.Factory) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages.
