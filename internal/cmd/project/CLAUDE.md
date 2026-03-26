# Project Command Package

Project lifecycle management: initialization, registration, listing, inspection, and removal.

Project commands are the primary user interface for working with the `ProjectManager` domain API.

## Files

| File | Purpose |
|------|---------|
| `project.go` | `NewCmdProject(f)` — parent command |
| `init/init.go` | `NewCmdProjectInit(f, runF)` — initialize project via preset picker + store-backed wizard |
| `register/register.go` | `NewCmdProjectRegister(f, runF)` — register existing project |
| `list/list.go` | `NewCmdList(f, runF)` — list registered projects with format flags |
| `info/info.go` | `NewCmdInfo(f, runF)` — show project details (name, root, worktrees, status) |
| `remove/remove.go` | `NewCmdRemove(f, runF)` — remove projects from registry (with confirmation) |
| `shared/discovery.go` | `HasLocalProjectConfig(cfg, dir)` — config existence check via storage layers + fallback probe |
| `shared/discovery_test.go` | Table-driven tests: registered/unregistered × all config placements |

## Subcommands

- `project init` — initialize new project in current directory. Guided setup with language presets (Python, Go, Rust, TypeScript, Java, Ruby, C/C++, C#/.NET, Bare) and optional "Build from scratch" customization. Creates `.clawker.yaml` from preset YAML via `storage.NewFromString[Project]` + `WithDefaultsFromStruct`, optionally runs `storeui.Wizard[T]` for field customization, then writes via `store.Write(storage.ToPath(...))` and registers project. Non-interactive mode (`--yes`) defaults to Bare preset; `--yes --preset <name>` selects a specific preset. Shell completions for `--preset` are dynamically generated from `config.Presets()` via `RegisterFlagCompletionFunc`.
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
    Force           bool
    Yes             bool
}
func NewCmdProjectInit(f *cmdutil.Factory, runF func(context.Context, *ProjectInitOptions) error) *cobra.Command
func Run(ctx context.Context, opts *ProjectInitOptions) error

// Internal types
type initEnv struct { ... }            // Resolved deps + derived state shared by both init paths
func resolveInitEnv(opts *ProjectInitOptions) (*initEnv, error)
type performSetupInput struct { ... }  // Narrowed deps for performProjectSetup (ios, tui, force, ...)
func performProjectSetup(ctx context.Context, in performSetupInput) error
func bootstrapSettings() error
func buildInitWizardFields(wctx wizardContext) []tui.WizardField
func customizeWizardFields() []string
func customizeWizardOverrides() []storeui.Override
func PresetCompletions() []cobra.Completion  // Dynamic completions from config.Presets()
func presetByName(presets []config.Preset, name string) (config.Preset, bool)
func validateProjectName(s string) error
```

`NewCmdProject` is the parent (no RunE). All subcommands accept `runF` for test injection.

## Architecture

`Run` dispatches to `runInteractive` (wizard) or `runNonInteractive` based on `--yes` flag and TTY detection. Both delegate to `performProjectSetup` for store creation, file writing, and registration.

```
Run()
  ├── runInteractive()
  │   ├── resolveInitEnv()           → factory nouns + bootstrap + derived state
  │   ├── TUI.RunWizard(fields)     → name + preset + action
  │   │   ├── overwrite declined    → register-only
  │   │   ├── "Save and get started" → performProjectSetup(preset, customize=false)
  │   │   ├── "Customize this preset" → performProjectSetup(preset, customize=true)
  │   │   └── AutoCustomize preset   → performProjectSetup(preset, customize=true)
  │   └── performProjectSetup()
  │       ├── NewFromString[Project](preset.YAML) → store
  │       ├── [if customize] storeui.Wizard[T](store) → field editing
  │       ├── store.Write(ToPath(configPath))
  │       ├── create .clawkerignore
  │       └── pm.Register(name, wd)
  └── runNonInteractive()                 (--yes or non-TTY)
      ├── resolveInitEnv()
      ├── resolve preset (--preset <name> or default "Bare")
      └── performProjectSetup(preset, customize=false)
```

### Setup Wizard Fields

| ID | Kind | Title | Default | SkipIf |
|----|------|-------|---------|--------|
| `overwrite` | Confirm | Overwrite | DefaultYes=false | `!configExists \|\| force` |
| `project_name` | Text | Project | dir name lowercase (or positional arg) | `overwrite == "no"` |
| `preset` | Select | Template | idx 0 | `overwrite == "no"` |
| `action` | Select | Action | idx 0 (Save) | `overwrite == "no"` OR preset.AutoCustomize |

### Customize Wizard Fields

When user selects "Customize this preset" or "Build from scratch", `storeui.Wizard[T]` runs with these field paths:

| Path | Kind | Override |
|------|------|----------|
| `build.image` | Text | — |
| `build.packages` | StringSlice | — |
| `build.instructions.root_run` | StringSlice | — |
| `build.instructions.user_run` | StringSlice | — |
| `build.inject.after_from` | StringSlice | — |
| `build.inject.after_packages` | StringSlice | — |
| `security.firewall.add_domains` | StringSlice | — |
| `workspace.default_mode` | Select | Options: `["bind", "snapshot"]` |

### Settings Bootstrap

On first run, `bootstrapSettings()` checks if `settings.yaml` exists on disk (via `config.SettingsFilePath()`). If missing, writes `GenerateDefaultsYAML[Settings]()` to the config directory. This is silent — no user prompt.

### Project Name Validation

`validateProjectName` enforces Docker-compatible lowercase names: `^[a-z0-9][a-z0-9._-]*$`. Rejects uppercase (suggests lowercase), spaces, and invalid start characters. Non-interactive mode auto-lowercases the name.

## Shared Utilities (`shared/`)

### `HasLocalProjectConfig(cfg config.Config, dir string) bool`

Checks whether a project config file exists in the given directory. Two-phase:

1. **Fast path**: Checks the factory-constructed config's discovered layers (covers registered projects via walk-up).
2. **Fallback**: Constructs a temporary `storage.NewStore[config.Project]` with `storage.WithDirs(dir)` to probe the directory using dual-placement discovery — works for unregistered projects where walk-up can't find the directory.

Filenames are derived from `cfg.ProjectConfigFileName()` (main + `.local` variant). Used by both `init` and `register` to detect existing config before proceeding.

## Config Access Pattern

`project init` creates a `storage.Store[config.Project]` from preset YAML via `storage.NewFromString[Project](preset.YAML, WithDefaultsFromStruct[Project]())`. Schema defaults fill any fields not specified by the preset. The store is written to the CWD dotfile via `store.Write(storage.ToPath(configPath))`. Uses `project.ProjectManager` for registry registration.

## Testing

Tests use `runF` injection for flag/option capture. Key patterns:
- `NewCmdProjectInit(f, captureFunc)` for flag parsing tests
- `buildInitWizardFields(wctx)` tested for field definitions, SkipIf logic, preset options
- `performProjectSetup()` tested directly with `performSetupInput` for file creation/registration (avoids BubbleTea)
- `validateProjectName()` tested as pure function with table-driven cases
- `customizeWizardFields()` validated against `storeui.WalkFields` to ensure all paths match real schema fields
- `TestPerformProjectSetup_PresetRoundTrip` — table-driven test over all presets: write + reload + verify

```bash
go test ./internal/cmd/project/... -v
```
