# Project Command Package

Project lifecycle management: initialization, registration, listing, inspection, and removal.

Project commands are the primary user interface for working with the `ProjectManager` domain API.

## Files

| File | Purpose |
|------|---------|
| `project.go` | `NewCmdProject(f)` — parent command |
| `init/init.go` | `NewCmdProjectInit(f, runF)` — initialize project via TUI wizard |
| `register/register.go` | `NewCmdProjectRegister(f, runF)` — register existing project |
| `list/list.go` | `NewCmdList(f, runF)` — list registered projects with format flags |
| `info/info.go` | `NewCmdInfo(f, runF)` — show project details (name, root, worktrees, status) |
| `remove/remove.go` | `NewCmdRemove(f, runF)` — remove projects from registry (with confirmation) |
| `shared/discovery.go` | `HasLocalProjectConfig(cfg, dir)` — config existence check via storage layers + fallback probe |
| `shared/discovery_test.go` | Table-driven tests: registered/unregistered × all config placements |

## Subcommands

- `project init` — initialize new project in current directory (creates `.clawker.yaml` dotfile and `cfg.ClawkerIgnoreName()`). Uses TUI wizard for interactive prompts. Creates a `storage.Store[config.Project]` with `WithDefaultsFromStruct`, applies wizard selections via `store.Set`, and persists via `store.Write(storage.ToPath(...))`. Optionally offers `projectui.Edit` for further configuration customization.
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
    Prompter        func() *prompter.Prompter
    Config          func() (config.Config, error)
    ProjectManager  func() (project.ProjectManager, error)
    Name            string // positional arg
    Force           bool
    Yes             bool
}
func NewCmdProjectInit(f *cmdutil.Factory, runF func(context.Context, *ProjectInitOptions) error) *cobra.Command
func Run(ctx context.Context, opts *ProjectInitOptions) error

type RegisterOptions struct {
    IOStreams *iostreams.IOStreams
    Prompter  func() *prompter.Prompter
    Config    func() (config.Config, error)
    Name      string // positional arg
    Yes       bool
}
func NewCmdProjectRegister(f *cmdutil.Factory, runF func(context.Context, *RegisterOptions) error) *cobra.Command
```

`NewCmdProject` is the parent (no RunE). All subcommands accept `runF` for test injection.

## Architecture

Project commands consume `ProjectManager.ListProjects()` for enriched views (`ProjectState`) with runtime health checks. `list` and `info` both use `ProjectState` — no ad-hoc `os.Stat` in command layer.

`Run` dispatches to `runInteractive` (wizard) or `runNonInteractive` based on `--yes` flag and TTY detection. Both delegate to `performProjectSetup` for file creation and registration.

```
Run()
  ├── runInteractive()     → TUI.RunWizard(fields) → performProjectSetup(name, image, mode)
  │                          ↳ overwrite declined  → register-only + maybeOfferUserDefault
  └── runNonInteractive()  → performProjectSetup(defaults)
```

### Wizard Fields

| ID | Kind | Title | Default | SkipIf |
|----|------|-------|---------|--------|
| `overwrite` | Confirm | Overwrite | DefaultYes=false | `!configExists \|\| force` |
| `project_name` | Text | Project | dir name (or positional arg) | `overwrite == "no"` |
| `flavor` | Select | Image | idx 0 (bookworm) | `overwrite == "no"` |
| `custom_image` | Text | Custom Image | — | `overwrite == "no"` OR `flavor != "Custom"` |
| `workspace_mode` | Select | Workspace | idx 0 (bind) | `overwrite == "no"` |

### Post-wizard Prompter

After setup, `performProjectSetup` uses `prompter.Confirm` to offer "Customize configuration?" which launches `projectui.Edit` (the storeui-based project config editor). This is a one-off post-action offer, separate from the setup wizard.

## Shared Utilities (`shared/`)

### `HasLocalProjectConfig(cfg config.Config, dir string) bool`

Checks whether a project config file exists in the given directory. Two-phase:

1. **Fast path**: Checks the factory-constructed config's discovered layers (covers registered projects via walk-up).
2. **Fallback**: Constructs a temporary `storage.NewStore[config.Project]` with `storage.WithDirs(dir)` to probe the directory using dual-placement discovery — works for unregistered projects where walk-up can't find the directory.

Filenames are derived from `cfg.ProjectConfigFileName()` (main + `.local` variant). Used by both `init` and `register` to detect existing config before proceeding.

## Config Access Pattern

All subcommands use the `config.Config` interface. `project init` creates a `storage.Store[config.Project]` with `WithDefaultsFromStruct` and applies wizard values via `store.Set`, then persists with `store.Write(storage.ToPath(configPath))`. Uses `project.ProjectManager` for registry registration.

## Testing

Tests use `runF` injection for flag/option capture. Key patterns:
- `NewCmdProjectInit(f, captureFunc)` for flag parsing tests
- `buildProjectWizardFields(wctx)` tested for field definitions, SkipIf logic
- `performProjectSetup()` tested directly for file creation/registration (avoids BubbleTea)
- `flavorFieldOptionsWithCustom()` and `resolveImageFromWizard()` tested as pure functions

```bash
go test ./internal/cmd/project/... -v
```
