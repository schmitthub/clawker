# Root Command Package

Root CLI command, global flags, logger initialization, and top-level aliases.

## Files

| File | Purpose |
|------|---------|
| `root.go` | `NewCmdRoot(f)` — root command with global flags and subcommand registration |
| `aliases.go` | `Alias` type, `registerAliases()`, `topLevelAliases` — top-level command shortcuts |

## Key Symbols

```go
func NewCmdRoot(f *cmdutil.Factory) *cobra.Command
```

## Global Flags

- `--debug` / `-D` — enable debug logging
- `--workdir` / `-w` — override working directory

## PersistentPreRunE

Initializes logger with file logging via `initializeLogger(debug)`.

## Registered Commands

- **Top-level:** `init`, `project`, `config`, `monitor`, `generate`, `ralph`
- **Management:** `container`, `image`, `volume`, `network`

## Aliases

```go
type Alias struct { /* factory for aliasing subcommands to top level */ }
func registerAliases(root *cobra.Command, f *cmdutil.Factory)
```

17 top-level aliases following Docker CLI patterns:
- **Container shortcuts:** `attach`, `create`, `cp`, `exec`, `kill`, `logs`, `pause`, `ps`, `rename`, `restart`, `rm`, `run`, `start`, `stats`, `stop`, `top`, `unpause`, `wait`
- **Image shortcuts:** `build`, `rmi`
