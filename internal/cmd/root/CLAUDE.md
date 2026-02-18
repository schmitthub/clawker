# Root Command Package

Root CLI command, global flags, logger initialization, and top-level aliases.

## Files

| File | Purpose |
|------|---------|
| `root.go` | `NewCmdRoot(f, version, buildDate)` — root command with global flags and subcommand registration |
| `aliases.go` | `Alias` type, `registerAliases()`, `topLevelAliases` — top-level command shortcuts |

## Key Symbols

```go
func NewCmdRoot(f *cmdutil.Factory, version, buildDate string) (*cobra.Command, error)
```

## Global Flags

- `--debug` / `-D` — enable debug logging

## PersistentPreRunE

Logger is initialized by `factory.ioStreams()` during Factory construction. PersistentPreRunE logs startup info via `logger.Debug()`.

## Registered Commands

- **Top-level:** `init`, `project`, `config`, `monitor`, `generate`, `loop`, `version`
- **Management:** `container`, `image`, `volume`, `network`, `worktree`

## Testing

No unit tests for `root.go` — it is straightforward wiring and regressions surface via downstream command tests and `make test`. Tests that need `NewCmdRoot` (e.g., `aliases_test.go`) should pass empty strings for version and date.

## Aliases

```go
type Alias struct { /* factory for aliasing subcommands to top level */ }
func registerAliases(root *cobra.Command, f *cmdutil.Factory)
```

20 top-level aliases following Docker CLI patterns:
- **Container shortcuts:** `attach`, `create`, `cp`, `exec`, `kill`, `logs`, `pause`, `ps`, `rename`, `restart`, `rm`, `run`, `start`, `stats`, `stop`, `top`, `unpause`, `wait`
- **Image shortcuts:** `build`, `rmi`
