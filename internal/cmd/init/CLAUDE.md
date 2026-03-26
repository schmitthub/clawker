# Init Command Package

Alias for `clawker project init`. Delegates all functionality to `internal/cmd/project/init`.

## Files

| File | Purpose |
|------|---------|
| `init.go` | `NewCmdInit(f, runF)` — thin alias command that forwards flags and delegates to `projectinit.Run()` |
| `init_test.go` | Flag forwarding tests, positional arg forwarding, alias tip output assertion |

## Key Symbols

```go
func NewCmdInit(f *cmdutil.Factory, runF func(context.Context, *projectinit.ProjectInitOptions) error) *cobra.Command
```

## Flags

- `--yes` / `-y` — Non-interactive mode, accept all defaults (forwarded to project init)
- `--force` / `-f` — Overwrite existing configuration files (forwarded to project init)
- `--preset` — Select a language preset (requires `--yes`); shell completions via `projectinit.PresetCompletions()`

## Behavior

1. Accepts optional positional arg `[project-name]` (forwarded to project init)
2. Prints alias tip to stderr: "Tip: 'clawker init' is an alias for 'clawker project init'"
3. Delegates to `projectinit.Run(ctx, opts)` with all flags forwarded via `ProjectInitOptions`

## Factory Wiring

Five Factory fields captured in `NewCmdInit`:
- `f.IOStreams` — I/O streams
- `f.TUI` — TUI wizard + progress display
- `f.Config` — lazy config gateway accessor
- `f.Logger` — lazy logger accessor
- `f.ProjectManager` — lazy project manager accessor

## Testing

Tests use `runF` injection to capture `ProjectInitOptions` without executing the real project init flow:

- `TestNewCmdInit_FlagForwarding` — table-driven: defaults, `--yes`, `--force`, positional arg, combined flags+arg, too many args rejection
- `TestNewCmdInit_PrintsAliasTip` — alias tip printed to stderr
- `TestNewCmdInit_FlagParityWithProjectInit` — drift detection: verifies alias has all flags that project init has

```bash
go test ./internal/cmd/init/... -v
```
