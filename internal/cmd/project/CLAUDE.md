# Project Command Package

Project lifecycle management (initialization and registration).

## Files

| File | Purpose |
|------|---------|
| `project.go` | `NewCmdProject(f)` — parent command |
| `init/init.go` | `NewCmdProjectInit(f, runF)` — initialize project via TUI wizard |
| `register/register.go` | `NewCmdProjectRegister(f, runF)` — register existing project |

## Subcommands

- `project init` — initialize new project in current directory (creates `.clawker.yaml` dotfile and `cfg.ClawkerIgnoreName()`). Uses TUI wizard for interactive prompts, `scaffoldProjectConfig()` based on `config.DefaultConfigYAML`. Optionally prompts to save as user-level default in configDir.
- `project register` — register existing project in user's registry (`cfg.ProjectRegistryFileName()`)

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

// Internal functions (unexported)
func runInteractive(ctx, opts)       // wizard-based flow via TUI.RunWizard
func runNonInteractive(ctx, opts)    // --yes / non-TTY path (no prompts)
func performProjectSetup(ctx, opts, projectName, buildImage, workspaceMode)
func buildProjectWizardFields(wctx wizardContext) []tui.WizardField
func flavorFieldOptionsWithCustom() []tui.FieldOption
func resolveImageFromWizard(values tui.WizardValues) string

type RegisterOptions struct {
    IOStreams *iostreams.IOStreams
    Prompter  func() *prompter.Prompter
    Config    func() (config.Config, error)
    Name      string // positional arg
    Yes       bool
}
func NewCmdProjectRegister(f *cmdutil.Factory, runF func(context.Context, *RegisterOptions) error) *cobra.Command
```

`NewCmdProject` is the parent (no RunE). Both subcommands accept `runF` for test injection.

## Architecture

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

`maybeOfferUserDefault` uses `prompter.Confirm` (not wizard) to offer saving config as user-level default. This is a one-off post-action offer, separate from the setup wizard.

## Config Access Pattern

Both commands use `config.Config` interface. `project init` uses `config.DefaultConfigYAML` as scaffold template, `config.UserProjectConfigFilePath()` for user-level default, and `project.ProjectManager` for registry.

## Testing

Tests use `runF` injection for flag/option capture. Key patterns:
- `NewCmdProjectInit(f, captureFunc)` for flag parsing tests
- `buildProjectWizardFields(wctx)` tested for field definitions, SkipIf logic
- `performProjectSetup()` tested directly for file creation/registration (avoids BubbleTea)
- `flavorFieldOptionsWithCustom()` and `resolveImageFromWizard()` tested as pure functions

```bash
go test ./internal/cmd/project/init/... -v
```
