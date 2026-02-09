# Prompter & Wizard Architecture

Comprehensive reference for the two prompting layers in clawker: line-based prompter (Scenario 2) and BubbleTea wizard (Scenario 4).

## 1. Overview & Decision Tree

Clawker has two distinct prompting mechanisms, each living in its own package with separate import boundaries:

| Layer | Package | Technology | Scenario |
|-------|---------|------------|----------|
| **Prompter** | `internal/prompter` | Line-based stdin/stderr | Scenario 2 (static-interactive) |
| **Wizard** | `internal/tui` | BubbleTea alt screen | Scenario 4 (live-interactive) |

### Decision Tree

```
Need user input?
  │
  ├── Single y/n confirmation? ──────── f.Prompter().Confirm()
  ├── Single text input? ────────────── f.Prompter().String()
  ├── Single list selection? ────────── f.Prompter().Select()
  ├── Multi-step form with navigation? ─ f.TUI.RunWizard(fields)
  ├── Multi-step form + progress? ───── wizard → f.TUI.RunProgress (init pattern)
  └── No input needed? ─────────────── Direct execution; --yes flag to skip
```

**Key rule**: Single prompt → prompter; multi-step form with back-navigation → wizard.

## 2. Prompter Package (`internal/prompter/`)

### Construction & Factory Wiring

```go
type Prompter struct { ios *iostreams.IOStreams }
func NewPrompter(ios *iostreams.IOStreams) *Prompter
```

Access via Factory: `f.Prompter()` (lazy accessor, returns `*prompter.Prompter`).

### Three Methods

**String** — text input with default/validation:
```go
func (p *Prompter) String(cfg PromptConfig) (string, error)

type PromptConfig struct {
    Message   string
    Default   string
    Required  bool
    Validator func(string) error
}
```

**Confirm** — y/n toggle:
```go
func (p *Prompter) Confirm(message string, defaultYes bool) (bool, error)
```

**Select** — numbered list, returns 0-based index:
```go
func (p *Prompter) Select(message string, options []SelectOption, defaultIdx int) (int, error)

type SelectOption struct {
    Label       string
    Description string
}
```

### Non-Interactive Fallback

In CI/non-TTY (checked via `ios.IsInteractive()`):
- `String` → returns `Default` (or error if `Required` with no default)
- `Confirm` → returns `defaultYes`
- `Select` → returns `defaultIdx`

### Output Behavior

All prompts write to `ios.ErrOut` (stderr) — keeps stdout clean for data output.

### Deprecated

`PromptForConfirmation(in io.Reader, message string) bool` — writes to `os.Stderr` directly, not testable. Use `Prompter.Confirm()` instead.

### Real Callers

| Command | Method | Purpose |
|---------|--------|---------|
| `image/prune` | `Confirm` | "Remove all unused images?" |
| `volume/prune` | `Confirm` | "Remove all unused volumes?" |
| `network/prune` | `Confirm` | "Remove all unused networks?" |
| `project/register` | `Select`, `String` | Project selection/naming |
| `project/init` | Various | Project initialization prompts |
| `container/run` | `Confirm` | Runtime confirmations |
| `container/create` | `Confirm` | Creation confirmations |

### Test Pattern

```go
tio := iostreams.NewTestIOStreams()
tio.SetInteractive(true)
tio.InBuf.SetInput("y\n")

p := prompter.NewPrompter(tio.IOStreams)
result, err := p.Confirm("Continue?", false)
// result == true, tio.ErrBuf.String() contains "Continue?"
```

## 3. Wizard TUI Components (`internal/tui/`)

Three composable layers, each independently usable:

### Layer 1 — Field Models (`fields.go`)

Standalone BubbleTea models. Value semantics (setters return copies). Each works alone or composed in a wizard.

**FieldOption** — shared type for select options:
```go
type FieldOption struct { Label string; Description string }
```

**SelectField** — arrow-key selection wrapping `ListModel`:
```go
func NewSelectField(id, prompt string, options []FieldOption, defaultIdx int) SelectField
func (f SelectField) Value() string          // returns selected option's Label (NOT index)
func (f SelectField) SelectedIndex() int
func (f SelectField) IsConfirmed() bool      // true after Enter
func (f SelectField) SetSize(w, h int) SelectField
```
Keys: Up/Down (or j/k) navigate, Enter confirms, Ctrl+C quits.

**TextField** — text input wrapping `bubbles/textinput`:
```go
func NewTextField(id, prompt string, opts ...TextFieldOption) TextField
// Options: WithPlaceholder, WithDefault, WithValidator, WithRequired
func (f TextField) Value() string
func (f TextField) IsConfirmed() bool
func (f TextField) Err() string              // validation error message
func (f TextField) SetSize(w, h int) TextField
```
Keys: Type to input, Enter validates + confirms, Ctrl+C quits. Required check runs before custom validator.

**ConfirmField** — yes/no toggle:
```go
func NewConfirmField(id, prompt string, defaultYes bool) ConfirmField
func (f ConfirmField) Value() string         // "yes" or "no" (lowercase)
func (f ConfirmField) BoolValue() bool
func (f ConfirmField) IsConfirmed() bool
func (f ConfirmField) SetSize(w, h int) ConfirmField
```
Keys: Left/Right/Tab toggle, y/n set directly, Enter confirms, Ctrl+C quits.

### Layer 2 — StepperBar (`stepper.go`)

Pure render function for horizontal step progress indicator.

```go
type StepState int
const (
    StepPendingState   // ○ (MutedStyle)
    StepActiveState    // ◉ (TitleStyle)
    StepCompleteState  // ✓ (SuccessStyle)
    StepSkippedState   // hidden
)

type Step struct {
    Title string
    Value string   // shown after ": " on complete steps
    State StepState
}

func RenderStepperBar(steps []Step, width int) string
```

**Rendering**: `StepState.String()` returns icon. Skipped steps hidden entirely. Steps joined by ` → ` separator (MutedStyle). Truncates to width.

**Example output**: `✓ Build Image: Yes  →  ◉ Flavor  →  ○ Submit`

### Layer 3 — WizardModel (`wizard.go`)

Composes fields + stepper into multi-step flow with navigation. Runs in alt screen via `TUI.RunWizard`.

## 4. WizardModel Internals

### Public Types

```go
type WizardFieldKind int
const (
    FieldSelect  WizardFieldKind = iota
    FieldText
    FieldConfirm
)

type WizardField struct {
    ID, Title, Prompt string
    Kind              WizardFieldKind

    // Select-specific
    Options    []FieldOption
    DefaultIdx int

    // Text-specific
    Placeholder string
    Default     string
    Validator   func(string) error
    Required    bool

    // Confirm-specific
    DefaultYes bool

    // Conditional: skip when predicate returns true
    SkipIf func(WizardValues) bool
}

type WizardValues map[string]string
type WizardResult struct {
    Values    WizardValues
    Submitted bool
}
```

### Two-Layer Design

`WizardField` is an immutable spec defining Kind/Options/SkipIf. At runtime, the wizard creates actual field instances (`SelectField`/`TextField`/`ConfirmField`) stored in maps by step index. When navigating backward, fields are recreated (fresh state).

### Constructor Validation

`newWizardModel(fields)` panics on:
- Empty field ID
- Duplicate field ID
- `FieldSelect` with no options

These are programming errors caught at construction, not runtime.

After validation: creates field instances, finds first visible step, auto-completes if all steps are skipped.

### Init/Update/View

- `Init()`: delegates to `currentFieldInit()` (e.g., textinput blink cursor)
- `Update(msg)`:
  - `WindowSizeMsg` → stores dimensions, calls `updateAllFieldSizes`
  - `KeyMsg`:
    - `Ctrl+C` → cancel, return `tea.Quit`
    - `Esc` → `goBack()` (cancel if first step)
    - `Enter` → `confirmAndAdvance()` (submit if last step)
    - Other keys → `delegateToCurrentField()` (Up/Down, Left/Right, y/n, typing)
  - Non-key messages (blink ticks) → `delegateNonKeyToCurrentField()`
- `View()`: `"  " + stepperBar + "\n\n" + fieldView + "\n\n" + "  " + helpBar`

### Navigation

- `nextVisibleStep(from)` / `prevVisibleStep(from)` — skip SkipIf-true steps
- `activateStep(idx)` — recreates field when going backward (fresh state)
- `confirmAndAdvance()` — stores current field value, finds next visible step; if none, submits
- `goBack()` — finds previous visible step; if none (at first step), cancels

### SkipIf Re-Evaluation

`SkipIf` predicates are re-evaluated on every navigation check. Changing an earlier answer can show or hide later steps. Both forward and backward navigation respects SkipIf.

### filterQuit

`filterQuit(cmd tea.Cmd) tea.Cmd` wraps field commands to suppress `tea.QuitMsg`. Individual fields send `tea.Quit` on Enter/Ctrl+C (designed for standalone use), but the wizard needs to intercept these to manage its own lifecycle.

The implementation uses deferred evaluation — wraps the cmd in a closure that only calls `cmd()` during BubbleTea's runtime, not eagerly outside it.

### Size Management

`updateAllFieldSizes` reserves 4 lines for chrome (stepper + gaps + help bar), distributes the remainder to all field instances:
```
fieldHeight = height - 4  // 1 stepper + 1 gap + 1 gap + 1 help
fieldWidth  = width - 4   // 2-char indent on each side
```

### Help Bar

Context-sensitive per field kind:
- SelectField: `↑/↓ navigate • enter confirm • esc back`
- ConfirmField: `←/→ toggle • y/n set • enter confirm • esc back`
- TextField: `type to enter • enter confirm • esc back`

## 5. TUI.RunWizard Integration

```go
func (t *TUI) RunWizard(fields []WizardField) (WizardResult, error)
```

**Flow**:
1. Validates non-empty fields (`len(fields) == 0` → error)
2. Creates `wizardModel` via `newWizardModel(fields)`
3. Runs via `RunProgram(ios, &model, WithAltScreen(true))`
4. Alt-screen mode: wizard takes full terminal, restores on exit
5. Extracts `submitted`/`cancelled`/`values` from final model
6. Returns explicit error on unexpected model type

**Return semantics**:
- `result.Submitted == true` + `result.Values` populated → user completed the wizard
- `result.Submitted == false` → user cancelled (Esc on first step or Ctrl+C)
- `err != nil` → BubbleTea runtime error or empty fields

## 6. Reference Implementation: `clawker init`

### Full Flow

```
Run()
  ├── runInteractive()     → TUI.RunWizard(fields) → performSetup(build, flavor)
  └── runNonInteractive()  → performSetup(false, "")
```

`Run` dispatches based on `opts.Yes || !opts.IOStreams.IsInteractive()`.

### buildWizardFields()

Three fields:
```go
{ID: "build",   Kind: FieldSelect,  Options: [{Yes, Recommended}, {No, Skip}], DefaultIdx: 0}
{ID: "flavor",  Kind: FieldSelect,  Options: flavorFieldOptions(), SkipIf: vals["build"] != "Yes"}
{ID: "confirm", Kind: FieldConfirm, DefaultYes: true}
```

### flavorFieldOptions()

Converts `bundler.DefaultFlavorOptions()` (name/description pairs) to `[]tui.FieldOption`.

### Value Extraction

```go
buildImage := result.Values["build"] == "Yes"    // string equality on label
flavor := result.Values["flavor"]                 // label passthrough to FlavorToImage()
if result.Values["confirm"] != "yes" { return nil } // "yes"/"no" lowercase from ConfirmField
```

### Hybrid Scenario 3+4

1. **Wizard phase** (alt screen) — `TUI.RunWizard` collects user choices
2. **Progress phase** (inline/alt) — `TUI.RunProgress` for image build
3. **Static next steps** — `fmt.Fprintf(ios.ErrOut, ...)` guidance

### performSetup — Shared Testable Core

`performSetup(ctx, opts, buildBaseImage, flavor)` handles settings save + optional build. Called by both interactive and non-interactive paths. Testable without BubbleTea.

**Error handling**:
- Build errors → graceful message + next steps + return nil (not fatal)
- Progress errors (Ctrl+C) → returned as error
- Settings save warnings → user-visible warning to stderr

### Testing

```go
// Test shared logic directly — no BubbleTea needed
performSetup(ctx, opts, true, "bookworm")

// Test with InMemorySettingsLoader + dockertest.FakeClient
cfg := config.NewConfigForTest()
cfg.SetSettingsLoader(configtest.NewInMemorySettingsLoader())
fake := dockertest.NewFakeClient()
fake.SetupLegacyBuild()
```

## 7. Guidelines for Future Commands

| Need | Tool | Example |
|------|------|---------|
| Single y/n | `f.Prompter().Confirm()` | `image prune`, `volume prune` |
| Single text input | `f.Prompter().String()` | Project name entry |
| Single list pick | `f.Prompter().Select()` | Base image selection |
| Multi-step form | `f.TUI.RunWizard(fields)` | `clawker init` |
| Multi-step + progress | Wizard then `f.TUI.RunProgress` | `clawker init` (build phase) |
| No input needed | Direct execution | `--yes` flag bypasses interactive |

**Non-interactive mode**: Check `ios.IsInteractive()` or `--yes` flag early and bypass wizard entirely. Both the prompter and wizard have non-interactive fallback, but the recommended pattern is to have a separate `runNonInteractive` path (like init) for clarity.

## 8. Key Gotchas

1. **Separate packages**: `internal/prompter` (line-based, stdin/stderr) vs `internal/tui` (BubbleTea, alt screen). Don't mix them in a single flow.

2. **Import boundary**: Only `internal/tui` imports `bubbletea`/`bubbles`. Only `internal/iostreams` imports `lipgloss`. Only `internal/prompter` reads from `ios.In` directly.

3. **Prompter writes to stderr directly**: Prompts render to `ios.ErrOut`. Wizard uses BubbleTea's alt screen (writes to `ios.ErrOut` via `RunProgram`).

4. **Wizard panics on invalid definitions**: Empty ID, duplicate ID, FieldSelect with no options → panic (programming errors, not runtime errors).

5. **Value semantics per kind**:
   - `SelectField.Value()` returns the **label** string, not the index
   - `ConfirmField.Value()` returns lowercase `"yes"` or `"no"` (not `"Yes"/"No"`)
   - `TextField.Value()` returns the text as entered

6. **SkipIf re-evaluates dynamically**: Changing an earlier answer can show/hide later steps. Both forward and backward navigation respects SkipIf.

7. **filterQuit prevents premature exit**: Individual fields send `tea.Quit` on Enter (designed for standalone use), but the wizard intercepts this. Uses deferred evaluation to avoid calling `cmd()` outside BubbleTea runtime.

8. **Testing wizards**: Use `newWizardModel(fields)` + direct `Update()` calls — no BubbleTea program runtime needed. Test shared logic (like `performSetup`) directly, bypassing BubbleTea entirely.

9. **Non-interactive bypass**: Check `ios.IsInteractive()` or `--yes` flag early. Don't attempt to run a wizard in non-interactive mode — have a separate code path.

10. **Prompter.Select returns index, wizard SelectField returns label**: These are different APIs with different return types. The prompter returns `(int, error)`, the wizard stores the label string in `WizardValues`.
