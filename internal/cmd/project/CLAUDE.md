# Project Command Package

Project lifecycle management: initialization, registration, listing, inspection, and removal.

Project commands are the primary user interface for working with the `ProjectManager` domain API.

## Files

| File | Purpose |
|------|---------|
| `project.go` | `NewCmdProject(f)` — parent command |
| `init/init.go` | `NewCmdProjectInit(f, runF)` — initialize project via preset picker + store-backed wizard |
| `edit/edit.go` | `NewCmdProjectEdit(f, runF)` — interactively edit project config via storeui browser |
| `register/register.go` | `NewCmdProjectRegister(f, runF)` — register existing project |
| `list/list.go` | `NewCmdList(f, runF)` — list registered projects with format flags |
| `info/info.go` | `NewCmdInfo(f, runF)` — show project details (name, root, worktrees, status) |
| `remove/remove.go` | `NewCmdRemove(f, runF)` — remove projects from registry (with confirmation) |
| `shared/discovery.go` | `HasLocalProjectConfig(cfg, dir)` — config existence check via storage layers + fallback probe |
| `shared/discovery_test.go` | Table-driven tests: registered/unregistered × all config placements |

## Subcommands

- `project init` — initialize new project in current directory. Guided setup with language presets (Python, Go, Rust, TypeScript, Java, Ruby, C/C++, C#/.NET, Bare) and optional "Build from scratch" customization. Creates `.clawker.yaml` from preset YAML via `config.NewProjectStoreFromPreset`, optionally runs a `storeui.BuildBrowser`-based customize browser for field editing, then writes via `store.WriteTo(configPath)` and registers project. Non-interactive mode (`--yes`) defaults to Bare preset; `--yes --preset <name>` selects a specific preset. Shell completions for `--preset` are dynamically generated from `config.Presets()` via `RegisterFlagCompletionFunc`.
- `project edit` — interactively edit existing project configuration. Opens a storeui browser TUI against `cfg.ProjectStore()` via `projectui.Edit`. No flags.
- `project register` — register existing project in user's registry (`cfg.ProjectRegistryFileName()`)
- `project list` (alias `ls`) — list all registered projects via `ProjectManager.ListProjects()`. Table output with NAME, ROOT, WORKTREES, STATUS columns. Supports `--format`/`--json`/`-q` flags via `FormatFlags`. Status reflects `ProjectState.Status` (ok, missing, inaccessible).
- `project info NAME` — show detailed info for a single project via `ProjectManager.ListProjects()`: name, root, directory status, worktrees with health status. Supports `--json` output (no `--format`/`--quiet`).
- `project remove NAME [NAME...]` (alias `rm`) — remove projects from registry by name. Prompts for confirmation in interactive mode; requires `--yes` in non-interactive mode. Does not delete files from disk.

## Key Symbols

```go
func NewCmdProject(f *cmdutil.Factory) *cobra.Command

type ProjectInitOptions struct {
    IOStreams       *iostreams.IOStreams
    TUI            *tui.TUI
    Config          func() (config.Config, error)
    Logger          func() (*logger.Logger, error)
    ProjectManager  func() (project.ProjectManager, error)
    Name            string // positional arg
    Preset          string // --preset flag
    VCS             string // --vcs flag
    GitProtocol     string // --git-protocol flag
    NoGPG           bool   // --no-gpg flag
    Force           bool
    Yes             bool
}
func NewCmdProjectInit(f *cmdutil.Factory, runF func(context.Context, *ProjectInitOptions) error) *cobra.Command
func Run(ctx context.Context, opts *ProjectInitOptions) error

// Internal types
type initEnv struct { ... }            // Resolved deps + derived state shared by both init paths
func resolveInitEnv(ctx context.Context, opts *ProjectInitOptions) (*initEnv, error)
type performSetupInput struct { ... }  // Narrowed deps for performProjectSetup (ios, tui, vcs, force, ...)
func performProjectSetup(ctx context.Context, in performSetupInput) error
func buildInitWizardSteps(wctx wizardContext) []tui.WizardStep
func customizeFields() []string
func customizeOverrides() []storeui.Override
func PresetCompletions() []cobra.Completion  // Dynamic completions from config.Presets()
func presetByName(presets []config.Preset, name string) (config.Preset, bool)
```

`NewCmdProject` is the parent (no RunE). All subcommands accept `runF` for test injection.

## Architecture

`Run` calls `auth.EnsureAuthMaterial()` first, then dispatches to `runInteractive` (wizard) or `runNonInteractive` based on `--yes` flag and TTY detection. Both delegate to `performProjectSetup` for store creation, file writing, and registration.

```
Run()
  ├── auth.EnsureAuthMaterial()
  ├── runInteractive()
  │   ├── resolveInitEnv(ctx, opts)   → factory nouns + settings bootstrap + derived state
  │   ├── TUI.RunWizard(steps)        → name + preset + vcs + action
  │   │   ├── overwrite declined      → register-only
  │   │   ├── "Save and get started"  → performProjectSetup(preset, vcs, customize=false)
  │   │   ├── "Customize this preset" → performProjectSetup(preset, vcs, customize=true)
  │   │   └── AutoCustomize preset    → performProjectSetup(preset, vcs, customize=true)
  │   └── performProjectSetup()
  │       ├── config.NewProjectStoreFromPreset(preset.YAML) → store
  │       ├── store.Set(applyVCSToProject)
  │       ├── [if customize] storeui.BuildBrowser + TUI.RunWizard(BrowserPage)
  │       ├── store.WriteTo(configPath)
  │       ├── create .clawkerignore
  │       └── pm.Register(name, wd)
  └── runNonInteractive()                 (--yes or non-TTY)
      ├── resolveInitEnv(ctx, opts)
      ├── resolve preset (--preset <name> or default "Bare")
      └── performProjectSetup(preset, vcs, customize=false)
```

### Setup Wizard Fields

| ID | Kind | Title | Default | SkipIf |
|----|------|-------|---------|--------|
| `overwrite` | Confirm | Overwrite | DefaultYes=false | `inSubdir \|\| !configExists \|\| force` |
| `project_name` | Text | Project | dir name lowercase (or positional arg) | `inSubdir \|\| overwriteDeclined` |
| `preset` | Select | Template | idx 0 | `overwriteDeclined` |
| `vcs_provider` | Select | VCS | idx 0 (GitHub) | `overwriteDeclined` |
| `git_protocol` | Select | Protocol | idx 0 (HTTPS) | `overwriteDeclined` |
| `gpg_forward` | Confirm | GPG | DefaultYes=true | `overwriteDeclined` |
| `action` | Select | Action | idx 0 (Save) | `overwriteDeclined` OR preset.AutoCustomize |

### Customize Browser Fields

When user selects "Customize this preset" or "Build from scratch", `storeui.BuildBrowser` runs with these field paths (`customizeFields()` + `customizeOverrides()`):

| Path | Kind | Override |
|------|------|----------|
| `build.image` | Text | — |
| `build.packages` | StringSlice | — |
| `build.instructions.root_run` | StringSlice | — |
| `build.instructions.user_run` | StringSlice | — |
| `build.inject.after_from` | StringSlice | — |
| `build.inject.after_packages` | StringSlice | — |
| `agent.post_init` | Text | — |
| `security.firewall.add_domains` | StringSlice | — |
| `workspace.default_mode` | Select | Options: `["bind", "snapshot"]` |

### Settings Bootstrap

During `resolveInitEnv`, `cfg.SettingsStore().Write()` is called to ensure `settings.yaml` exists on disk with schema defaults. If the file already exists, `Write()` is a no-op. On failure a warning is printed to `ErrOut` but initialization continues.

### Project Name Normalization

Raw user input (positional arg, `--name` flag, dirname fallback) is normalized through `cmdutil.ProjectSlugify` before reaching `pm.Register`: lowercase, whitespace collapsed to `-`, control chars stripped, leading/trailing `-` trimmed. Unicode / dots / underscores pass through. No charset validation here — Docker rejects unusable names at container create time with a clear error.

## Shared Utilities (`shared/`)

### `HasLocalProjectConfig(cfg config.Config, dir string) bool`

Checks whether a project config file exists in the given directory. Two-phase:

1. **Fast path**: Checks the factory-constructed config's discovered layers (covers registered projects via walk-up).
2. **Fallback**: Constructs a temporary `storage.NewStore[config.Project]` with `storage.WithDirs(dir)` to probe the directory using dual-placement discovery — works for unregistered projects where walk-up can't find the directory.

Filenames are derived from `cfg.ProjectConfigFileName()` (main + `.local` variant). Used by both `init` and `register` to detect existing config before proceeding.

## Config Access Pattern

`project init` creates an isolated store from preset YAML via `config.NewProjectStoreFromPreset(preset.YAML)` (no file discovery, no walk-up, no user-level config merging). VCS settings are applied via `store.Set(applyVCSToProject)`. The store is written to the CWD dotfile via `store.WriteTo(configPath)`. Uses `project.ProjectManager` for registry registration.

## Testing

Tests use `runF` injection for flag/option capture. Key patterns:
- `NewCmdProjectInit(f, captureFunc)` for flag parsing tests
- `buildInitWizardSteps(wctx)` tested for step definitions, SkipIf logic, preset options
- `performProjectSetup()` tested directly with `performSetupInput` for file creation/registration (avoids BubbleTea)
- Project name normalization tested centrally in `internal/cmdutil/slugify_test.go`
- `customizeFields()` paths used to configure the storeui browser in `performProjectSetup`
- `TestPerformProjectSetup_PresetRoundTrip` — table-driven test over all presets: write + reload + verify

```bash
go test ./internal/cmd/project/... -v
```
