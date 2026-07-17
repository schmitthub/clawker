# Skill Command Package

Manage the clawker-support agent skills plugin across host harnesses. The
claude harness wraps the `claude plugin` CLI; codex, opencode, and pi install
by fetching the plugin source from the marketplace and copying its skills into
the harness's native skills directory.

## Files

| File | Purpose |
|------|---------|
| `skill.go` | `NewCmdSkill(f)` — parent command, registers install/show/remove |
| `install/install.go` | `NewCmdInstall(f, runF)` — claude: add marketplace + install plugin; others: fetch + copy skills |
| `show/show.go` | `NewCmdShow(f, runF)` — display manual install commands per harness |
| `remove/remove.go` | `NewCmdRemove(f, runF)` — claude: uninstall plugin; others: fetch to enumerate, delete skill dirs |
| `shared/shared.go` | Constants (`PluginName`, `MarketplaceSource`), harness constants, `ValidateScope`, `ValidateHarness`, `CheckClaudeCLI`, `RunClaude` |
| `shared/copy.go` | Copy-lane machinery: `SkillsDir`, `FetchPluginSkills`, `CopySkills`, `RemoveSkills` |

## Key Symbols

```go
func NewCmdSkill(f *cmdutil.Factory) *cobra.Command
func NewCmdInstall(f *cmdutil.Factory, runF func(context.Context, *InstallOptions) error) *cobra.Command
func NewCmdShow(f *cmdutil.Factory, runF func(context.Context, *ShowOptions) error) *cobra.Command
func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command
```

## Shared Package

`shared/shared.go` centralizes:

- **Constants**: `MarketplaceSource` (`schmitthub/clawker-plugin`), `PluginName` (`clawker-support@schmitthub-plugins`), harness names (`HarnessClaude`/`HarnessCodex`/`HarnessOpencode`/`HarnessPi`, `ValidHarnesses`)
- **`ValidateScope(scope)`** / **`ValidateHarness(harness)`**: Return `FlagError` for invalid values
- **`CheckClaudeCLI()`**: `exec.LookPath` with differentiated errors (not found vs not usable)
- **`RunClaude(ctx, ios, args...)`**: Subprocess execution with stdin wired, context cancellation handling, and actionable exit code errors

`shared/copy.go` owns the copy lane:

- **`SkillsDir(harness)`**: The harness's native skills dir — codex `~/.agents/skills`, pi `~/.pi/agent/skills`, opencode `${OPENCODE_CONFIG_DIR:-~/.config/opencode}/skills`
- **`FetchPluginSkills(ctx, fetcher)`**: Clones the marketplace repo, resolves the plugin's source (a relative path inside the marketplace, or a git object url + path + sha), fetches it via `bundle/fetch.Fetcher`, returns the skills dir + names + cleanup. The marketplace catalog decides what ships — same release the claude lane installs
- **`CopySkills` / `RemoveSkills`**: Wholesale per-skill dir replace / idempotent delete. Skills sit exactly one level under `skills/` (the flat layout every harness discovers)

## DI for Testing

`InstallOptions` and `RemoveOptions` accept `CheckCLI`, `RunClaude`,
`FetchSkills`, and `SkillsDir` function fields, defaulting to the shared
implementations. Tests inject fakes to verify orchestration flow without
shelling out or touching the network.

## Flags

| Flag | Shorthand | Default | Commands |
|------|-----------|---------|----------|
| `--scope` | `-s` | `user` | install, remove (claude only) |
| `--harness` | | `claude` | install, remove, show |

Valid scopes: `user`, `project`, `local` (mirrors Claude CLI `--scope`).
Valid harnesses: `claude`, `codex`, `opencode`, `pi`.

## Output Conventions

- Progress/status messages → `ios.ErrOut` (headings, icons, step progress)
- Data output → `ios.Out` (pipeable command strings in `show`, per-skill install/remove results)
- Errors: `FlagError` for bad flags, `fmt.Errorf` wrapping subprocess failures
