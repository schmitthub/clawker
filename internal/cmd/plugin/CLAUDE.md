# Plugin Command Package

Manage the clawker-support agent skills plugin across host harnesses
(`clawker plugin`, alias `clawker skill`). The claude harness wraps the
`claude plugin` CLI; codex, opencode, and pi install by fetching the plugin
source from the marketplace and copying its skills into the harness's native
skills directory.

## Files

| File | Purpose |
|------|---------|
| `plugin.go` | `NewCmdPlugin(f)` — parent command (alias `skill`), registers install/show/remove |
| `install/install.go` | `NewCmdInstall(f, runF)` — claude: add marketplace + install plugin; others: fetch + copy skills |
| `show/show.go` | `NewCmdShow(f, runF)` — display manual install commands per harness |
| `remove/remove.go` | `NewCmdRemove(f, runF)` — claude: uninstall plugin; others: fetch to enumerate, delete skill dirs |
| `shared/shared.go` | Claude-lane constants (`PluginName`, `MarketplaceSource`), harness constants, `ValidateScope`, `ValidateHarness`, `CheckClaudeCLI`, `RunClaude` |
| `shared/copy.go` | Marketplace constants (`MarketplaceGitURL`, `MarketplaceManifestPath`, `MarketplacePluginName`), `ErrSourceTraversal`, copy-lane machinery: `SkillsDir`, `FetchPluginSkills`, `CopySkills`, `RemoveSkills` |

## Key Symbols

```go
func NewCmdPlugin(f *cmdutil.Factory) *cobra.Command
func NewCmdInstall(f *cmdutil.Factory, runF func(context.Context, *InstallOptions) error) *cobra.Command
func NewCmdShow(f *cmdutil.Factory, runF func(context.Context, *ShowOptions) error) *cobra.Command
func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command
```

## Shared Package

`shared/shared.go` centralizes:

- **Constants**: `MarketplaceSource` (the marketplace's GitHub repo slug for `claude plugin marketplace add`), `PluginName` (the plugin's name@marketplace identifier for `claude plugin install`), harness names (`HarnessClaude`/`HarnessCodex`/`HarnessOpencode`/`HarnessPi`, `ValidHarnesses`)
- **`ValidateScope(scope)`** / **`ValidateHarness(harness)`**: Return `FlagError` for invalid values
- **`CheckClaudeCLI()`**: `exec.LookPath` with differentiated errors (not found vs not usable)
- **`RunClaude(ctx, ios, args...)`**: Subprocess execution with stdin wired, context cancellation handling (cancellation errors carry the subprocess error), and actionable exit code errors

Note: the claude lane runs `claude plugin marketplace add` unconditionally on
every install — the Claude CLI itself is idempotent (re-adding an existing
marketplace exits 0).

`shared/copy.go` owns the copy lane and the marketplace constants
(`MarketplaceGitURL` clone URL, `MarketplaceManifestPath`,
`MarketplacePluginName` — the plugin's bare catalog name, also used in
success messages):

- **`SkillsDir(harness)`**: The harness's native skills dir — codex `~/.agents/skills`, pi `~/.pi/agent/skills`, opencode `${OPENCODE_CONFIG_DIR:-~/.config/opencode}/skills`
- **`FetchPluginSkills(ctx, fetcher)`**: Clones the marketplace repo, resolves the plugin's source (a relative path inside the marketplace, or a git object url + path + sha), fetches it via `bundle/fetch.Fetcher`, returns the skills dir + names + cleanup. The marketplace catalog decides what ships — same release the claude lane installs. A relative source containing a `..` path segment is rejected with `ErrSourceTraversal` (segment comparison — names merely containing dots pass)
- **`CopySkills` / `RemoveSkills`**: Wholesale per-skill dir replace / idempotent delete. Skills sit exactly one level under `skills/` (the flat layout every harness discovers). Copies preserve source file permission bits (skill scripts keep exec bits); non-regular entries (symlinks, FIFOs) are skipped and `CopySkills` returns the skipped count, which install surfaces as one warning. `RemoveSkills` returns the names actually removed; remove prints a distinct "not installed, skipped" line for the rest and warns once about skill dirs left in the destination that aren't in the current catalog

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
