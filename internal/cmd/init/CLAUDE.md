# Init Command Package

Initialize user-level clawker settings (`~/.local/clawker/settings.yaml`).

## Files

| File | Purpose |
|------|---------|
| `init.go` | `NewCmdInit(f, runF)` — user initialization command with wizard-based interactive flow |
| `init_test.go` | Unit tests with dockertest fakes, wizard field assertions, and `performSetup` tests |

## Key Symbols

```go
type InitOptions struct {
    IOStreams *iostreams.IOStreams
    TUI      *tui.TUI
    Prompter func() *prompter.Prompter  // retained for backward compat; unused by wizard path
    Config   func() config.Provider
    Client   func(context.Context) (*docker.Client, error)
    Yes      bool
}

func NewCmdInit(f *cmdutil.Factory, runF func(context.Context, *InitOptions) error) *cobra.Command
func Run(ctx context.Context, opts *InitOptions) error

// Internal functions (unexported)
func runInteractive(ctx, opts)    // wizard-based flow via TUI.RunWizard
func runNonInteractive(ctx, opts) // --yes / non-TTY path (no prompts, no build)
func performSetup(ctx, opts, buildBaseImage, selectedFlavor) // shared setup logic
func buildWizardFields() []tui.WizardField    // wizard field definitions
func flavorFieldOptions() []tui.FieldOption   // converts bundler flavors to TUI field options
```

## Flags

- `--yes` / `-y` — skip interactive prompts (non-interactive mode)

## Output Scenario

**Hybrid Scenario 3+4** — TUI wizard for interactive prompts followed by live-display progress:
1. Interactive wizard via `TUI.RunWizard` (build image?, select flavor, confirm)
2. TUI progress display via `TUI.RunProgress` for image build
3. Static output for status messages and next steps

## Architecture

`Run` dispatches to `runInteractive` (wizard) or `runNonInteractive` based on `--yes` flag and TTY detection. Both delegate to `performSetup` for the actual settings save and optional image build, making the core logic easily testable without driving BubbleTea.

```
Run()
  ├── runInteractive()     → TUI.RunWizard(fields) → performSetup(build, flavor)
  └── runNonInteractive()  → performSetup(false, "")
```

### Wizard Fields

| ID | Kind | Purpose |
|----|------|---------|
| `build` | Select | "Build an initial base image?" — Yes/No |
| `flavor` | Select | "Select Linux flavor" — from `bundler.DefaultFlavorOptions()`; skipped if build=No |
| `confirm` | Confirm | "Proceed with setup?" — DefaultYes=true |

## Behavior

1. Creates/updates user settings file via config gateway:
   - Loads SettingsLoader via `Config().SettingsLoader()`
   - Falls back to `config.NewSettingsLoader()` if nil (e.g., first run)
   - Sets it via `Config().SetSettingsLoader()` for subsequent use
2. Interactive wizard (unless `--yes` or non-TTY):
   - Build initial base image? (Select: Yes/No)
   - Select Linux flavor (Debian/Alpine) via `bundler.DefaultFlavorOptions()`
   - Confirm setup
3. Saves settings, then builds base image if requested:
   - Generates Dockerfile via `bundler.FlavorToImage` + `bundler.NewProjectGenerator`
   - Builds with `client.BuildImage` (not the deprecated `BuildDefaultImage`)
   - Progress displayed via `TUI.RunProgress("auto", ...)` with single "build" step; result checked for errors
4. On progress display error (e.g., Ctrl+C): returns error immediately
5. On build failure: prints error + manual recovery steps (does not return error)
6. Ensures shared directory at `config.ShareDir()` exists via `config.EnsureDir()`
7. On build success: updates `settings.DefaultImage` to `docker.DefaultImageTag`
7. Prints next steps guidance to stderr

## Factory Wiring

All five Factory fields are captured eagerly in `NewCmdInit`:
- `f.IOStreams` — I/O streams
- `f.TUI` — TUI wizard + progress display
- `f.Prompter` — lazy prompter accessor (retained for backward compat, unused by wizard)
- `f.Config` — lazy config gateway accessor
- `f.Client` — lazy Docker client constructor

## Testing

Tests use `runF` injection and dockertest fakes. Key patterns:
- `NewCmdInit(f, captureFunc)` for flag/option capture tests
- `performSetup()` tested directly for build/no-build/failure scenarios (avoids BubbleTea)
- `buildWizardFields()` tested for field definitions, SkipIf logic
- `flavorFieldOptions()` tested for correct conversion from bundler types
- `configtest.NewInMemorySettingsLoader()` for in-memory settings (no temp dirs)

```bash
go test ./internal/cmd/init/... -v
```
