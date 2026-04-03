# Skill Command Package

Manage the clawker-support Claude Code skill plugin. Wraps the `claude plugin` CLI.

## Files

| File | Purpose |
|------|---------|
| `skill.go` | `NewCmdSkill(f)` — parent command, registers install/show/remove |
| `install/install.go` | `NewCmdInstall(f, runF)` — add marketplace + install plugin |
| `show/show.go` | `NewCmdShow(f, runF)` — display manual install commands |
| `remove/remove.go` | `NewCmdRemove(f, runF)` — uninstall plugin |
| `shared/shared.go` | Constants (`PluginName`, `MarketplaceSource`), `ValidateScope`, `CheckClaudeCLI`, `RunClaude` |

## Key Symbols

```go
func NewCmdSkill(f *cmdutil.Factory) *cobra.Command
func NewCmdInstall(f *cmdutil.Factory, runF func(context.Context, *InstallOptions) error) *cobra.Command
func NewCmdShow(f *cmdutil.Factory, runF func(context.Context, *ShowOptions) error) *cobra.Command
func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command
```

## Shared Package

`shared/shared.go` centralizes:

- **Constants**: `MarketplaceSource` (`schmitthub/claude-plugins`), `PluginName` (`clawker-support@schmitthub-plugins`)
- **`ValidateScope(scope)`**: Returns `FlagError` for invalid scopes (user/project/local)
- **`CheckClaudeCLI()`**: `exec.LookPath` with differentiated errors (not found vs not usable)
- **`RunClaude(ctx, ios, args...)`**: Subprocess execution with stdin wired, context cancellation handling, and actionable exit code errors

## DI for Testing

`InstallOptions` and `RemoveOptions` accept `CheckCLI` and `RunClaude` function fields, defaulting to the shared implementations. Tests inject fakes to verify orchestration flow without shelling out.

## Flags

| Flag | Shorthand | Default | Commands |
|------|-----------|---------|----------|
| `--scope` | `-s` | `user` | install, remove |

Valid scopes: `user`, `project`, `local` (mirrors Claude CLI `--scope`).

## Output Conventions

- Progress/status messages → `ios.ErrOut` (headings, icons, step progress)
- Data output → `ios.Out` (pipeable command strings in `show`)
- Errors: `FlagError` for bad flags, `fmt.Errorf` wrapping subprocess failures
