# TUI Components Package

Reusable BubbleTea components for terminal UIs. Stateless render functions + value-type models (immutable setters return copies).

## Architecture

**Import boundary**: This package does NOT import `lipgloss` or `lipgloss/table` directly. Styles and colors are accessed via qualified imports from `internal/iostreams` (e.g., `iostreams.PanelStyle`). Styled table rendering is delegated to `iostreams.RenderStyledTable`. Text utilities come from `internal/text` (e.g., `text.Truncate`). The `import_boundary_test.go` enforces both constraints.

**Allowed imports**: `bubbletea`, `bubbles/*`, `internal/iostreams`, `internal/text`. The tui package sits one layer above iostreams in the DAG — it adds BubbleTea interactivity on top of iostreams' visual primitives.

**Style usage pattern**: Since `lipgloss.Style` cannot appear in type signatures without importing lipgloss, functions that would return a style instead return `func(string) string` (a render function). Styles can be used inline via type inference: `style := iostreams.PanelStyle` (Go infers the lipgloss.Style type).

## File Overview

| File | Purpose |
|------|---------|
| `tui.go` | `TUI` struct — Factory noun, owns hooks + progress display + wizard; `NewTUI`, `RegisterHooks`, `RunProgress`, `RunWizard` |
| `hooks.go` | `HookResult`, `LifecycleHook` — generic lifecycle hook types for TUI components |
| `keys.go` | `KeyMap` struct, `DefaultKeyMap()`, `Is*` key matchers |
| `components.go` | Stateless renders: header, status, badge, progress, table, divider |
| `spinner.go` | Animated spinner wrapping bubbles/spinner |
| `panel.go` | Bordered panels with focus, `PanelGroup` for multi-panel layouts |
| `list.go` | Selectable list with scrolling, `ListItem` interface |
| `statusbar.go` | Status bar with left/center/right sections, indicator helpers |
| `viewport.go` | Scrollable content viewport wrapping bubbles/viewport |
| `help.go` | Help bar/grid, binding presets, `QuickHelp` |
| `program.go` | `RunProgram` helper for running BubbleTea programs with IOStreams |
| `progress.go` | Generic progress display: BubbleTea TTY mode + plain text mode |
| `table.go` | `TablePrinter` — TTY-aware tabular output; styled mode via `iostreams.RenderStyledTable` (`lipgloss/table`) |
| `fields.go` | Standalone field models: `SelectField`, `TextField`, `ConfirmField`, `FieldOption` |
| `stepper.go` | Step progress indicator: `Step`, `StepState`, `RenderStepperBar` |
| `wizard.go` | Multi-step wizard: `WizardField`, `WizardValues`, `WizardResult`, `wizardModel` |
| `import_boundary_test.go` | Enforces no lipgloss or lipgloss/table imports in non-test files |

## Keys (`keys.go`)

`KeyMap` struct: `Quit`, `Up`, `Down`, `Left`, `Right`, `Enter`, `Escape`, `Help`, `Tab` (all `key.Binding`)

`DefaultKeyMap()` -- standard vim-style + arrow key bindings

**Matchers** (take `tea.KeyMsg`, return bool): `IsQuit`, `IsUp`, `IsDown`, `IsLeft`, `IsRight`, `IsEnter`, `IsEscape`, `IsHelp`, `IsTab`

## Stateless Render Functions (`components.go`)

**Config types**: `HeaderConfig` (Title, Subtitle, Timestamp, Width), `StatusConfig` (Status, Label), `ProgressConfig` (Current, Total, Width, ShowBar), `TableConfig` (Headers, Rows, ColWidths, Width), `KeyValuePair` (Key, Value)

| Function | Purpose |
|----------|---------|
| `RenderHeader(HeaderConfig)` | Title bar with optional subtitle/timestamp |
| `RenderStatus(StatusConfig)` | Status indicator with label |
| `RenderBadge(text, ...func(string) string)` | Inline badge (default: BadgeStyle) |
| `RenderCountBadge(count, label)` | Count with label like "3 tasks" |
| `RenderProgress(ProgressConfig)` | Text "3/10" or visual bar |
| `RenderDivider(width)`, `RenderLabeledDivider(label, width)` | Horizontal rules |
| `RenderEmptyState(message, w, h)`, `RenderError(err, width)` | State displays |
| `RenderLabelValue`, `RenderKeyValueTable`, `RenderTable` | Key-value and tabular rendering |
| `RenderPercentage(float64)`, `RenderBytes(int64)` | Color-coded percentage, human-readable bytes |
| `RenderTag(text, ...func(string) string)` | Bordered tag (default: TagStyle) |
| `RenderTags([]string, ...func(string) string)` | Multiple tags inline |

## Interactive Components

All models use value semantics -- setters return new copies. Each has `View() string`.

### SpinnerModel (`spinner.go`)

`SpinnerType` constants: `SpinnerDots`, `SpinnerLine`, `SpinnerMiniDots`, `SpinnerJump`, `SpinnerPulse`, `SpinnerPoints`, `SpinnerGlobe`, `SpinnerMoon`, `SpinnerMonkey`

`NewSpinner(SpinnerType, label)`, `NewDefaultSpinner(label)` -- constructors

BubbleTea: `Init() tea.Cmd`, `Update(tea.Msg) (SpinnerModel, tea.Cmd)`, `View()`, `Tick() tea.Msg`

Setters: `SetLabel`, `SetSpinnerType` | Type alias: `SpinnerTickMsg`

### PanelModel (`panel.go`)

`PanelConfig` (Title, Width, Height, Focused, Padding). `DefaultPanelConfig()`.

`NewPanel(PanelConfig)` -- bordered container with focus highlight

Setters: `SetContent`, `SetTitle`, `SetFocused`, `SetWidth`, `SetHeight`, `SetPadding`

Getters: `Width()`, `Height()`, `Title()`, `Content()`, `IsFocused()`

Convenience: `RenderInfoPanel(title, content, width)`, `RenderDetailPanel(title, []KeyValuePair, width)`, `RenderScrollablePanel(title, lines, offset, visibleLines, width)`

**PanelGroup**: `NewPanelGroup(...PanelModel)` -- manages focus across panels. Methods: `Add`, `FocusNext`, `FocusPrev`, `Focus(index)`, `FocusedPanel`, `FocusedIndex`, `Panels`, `RenderHorizontal(gap)`, `RenderVertical(gap)`

### ListModel (`list.go`)

`ListItem` interface: `Title()`, `Description()`, `FilterValue()`. `SimpleListItem` implements it.

`ListConfig` (Width, Height, ShowDescriptions, Wrap). `DefaultListConfig()`.

`NewList(ListConfig)` -- selectable list with scrolling

BubbleTea: `Update(tea.Msg)` handles up/down/home/end/pgup/pgdown

Navigation: `SelectNext`, `SelectPrev`, `SelectFirst`, `SelectLast`, `Select(index)`, `PageUp`, `PageDown`

Setters: `SetItems`, `SetWidth`, `SetHeight`, `SetShowDescriptions`, `SetWrap`

Getters: `SelectedItem`, `SelectedIndex`, `Items`, `Len`, `IsEmpty`

### StatusBarModel (`statusbar.go`)

`NewStatusBar(width)` -- left/center/right section bar

Setters: `SetLeft`, `SetCenter`, `SetRight`, `SetWidth` | Getters: `Left`, `Center`, `Right`, `Width`

`StatusBarSection` struct: `Content string`, `Render func(string) string`

`RenderStatusBarWithSections([]StatusBarSection, width)`, `RenderStatusBar(left, center, right, width)`

**Indicators**: `ModeIndicator(mode, active)`, `ConnectionIndicator(connected)`, `TimerIndicator(label, value)`, `CounterIndicator(label, current, total)`

### ViewportModel (`viewport.go`)

`ViewportConfig` (Width, Height, Title, Content). Wraps `bubbles/viewport`.

`NewViewport(ViewportConfig)` -- scrollable content with panel styling

Setters: `SetContent`, `SetSize`, `SetTitle`

Scroll: `ScrollToTop`, `ScrollToBottom` | Queries: `AtTop`, `AtBottom`, `ScrollPercent`

Getters: `Title`, `Width`, `Height`

BubbleTea: `Init() tea.Cmd`, `Update(tea.Msg) (ViewportModel, tea.Cmd)`, `View()`

### HelpModel (`help.go`)

`HelpConfig` (Width, ShowAll, Separator). `DefaultHelpConfig()`.

`NewHelp(HelpConfig)` -- help bar from key bindings

Setters: `SetBindings`, `SetWidth`, `SetShowAll`, `SetSeparator` | Methods: `View()`, `ShortHelp()`, `FullHelp()`, `Bindings()`

**Standalone**: `RenderHelpBar(bindings, width)`, `RenderHelpGrid(bindings, columns, width)`

**Binding presets**: `NavigationBindings()`, `QuitBindings()`, `AllBindings()` -- return `[]key.Binding`

**Quick helpers**: `HelpBinding(keys, desc)`, `QuickHelp(pairs ...string)` -- inline help strings

## RunProgram (`program.go`)

`RunProgram(ios *iostreams.IOStreams, model tea.Model, opts ...ProgramOption) (tea.Model, error)`

Runs a BubbleTea program using IOStreams for input/output. Reads from `ios.In`, writes to `ios.ErrOut`.

**Options**: `WithAltScreen(bool)`, `WithMouseMotion(bool)`

## Generic Progress Display (`progress.go`)

Generic multi-step progress display using BubbleTea for TTY mode and sequential text for plain mode. Zero domain knowledge — build-specific logic flows in through callbacks.

```go
ch := make(chan tui.ProgressStep, 64)

buildOpts.OnProgress = func(event whail.BuildProgressEvent) {
    ch <- tui.ProgressStep{
        ID: event.StepID, Name: event.StepName,
        Status: progressStatus(event.Status), // explicit switch, no iota alignment
        Cached: event.Cached, Error: event.Error, LogLine: event.LogLine,
    }
}

go func() {
    buildErr = builder.Build(ctx, imageTag, buildOpts)
    close(ch) // channel closure = done signal
}()

result := tui.RunProgress(ios, opts.Progress, tui.ProgressDisplayConfig{
    Title: "Building " + project, Subtitle: imageTag,
    CompletionVerb: "Built",
    MaxVisible: 5, LogLines: 3,
    IsInternal:     whail.IsInternalStep,
    CleanName:      whail.CleanStepName,
    ParseGroup:     whail.ParseBuildStage,
    FormatDuration: whail.FormatBuildDuration,
}, ch)
```

**Types**: `ProgressStepStatus` (`StepPending`, `StepRunning`, `StepComplete`, `StepCached`, `StepError`), `ProgressStep`, `ProgressDisplayConfig`, `ProgressResult`

**Entry point**: `RunProgress(ios, mode, cfg, ch)` — mode is `"auto"`, `"plain"`, or `"tty"`; cfg provides callbacks for domain-specific behavior

**Fields in ProgressDisplayConfig**:
- `CompletionVerb string` — success summary verb (e.g., "Built", "Deployed"). Default: "Completed"
- `AltScreen bool` — use alternate screen buffer for TTY mode. When true, progress renders in alt screen and clears on finish for clean handoff (e.g., to container TTY). Only affects `runProgressTTY`. Default: false.

**Callbacks in ProgressDisplayConfig**:
- `IsInternal func(string) bool` — filter hidden steps (nil = show all)
- `CleanName func(string) string` — strip noise from step names (nil = pass through)
- `ParseGroup func(string) string` — extract group/stage names (nil = no groups)
- `FormatDuration func(time.Duration) string` — format step durations (nil = default)

**TTY mode**: BubbleTea model with tree-based stage display. Stages are parent nodes with tree-connected child steps (`├─`/`└─` connectors). Complete/pending/error stages show collapsed (`✓ name ── N steps`). Active stages (with running step) show expanded with inline log lines (`⎿` connector) under the running step. Per-stage child window (MaxVisible) centers on the running step with collapsed header/footer for overflow. High-water mark frame padding prevents BubbleTea inline renderer cursor drift. Pulsing spinner in Primary color. Per-step log buffers (LogLines capacity). Ctrl+C handling.

**Internal types**: `stageNode` (group with steps), `stageTree` (stages + ungrouped), `progressStep.logBuf` (per-step ring buffer). `buildStageTree()` groups steps by ParseGroup callback. `stageState()` returns aggregate: Error > Running > Complete > Pending.

**Tree rendering pipeline**: `renderTreeSection()` → `renderStageNode()` (dispatches collapsed vs expanded) → `renderStageChildren()` (tree connectors + child window + inline logs) → `renderTreeStepLine()` (step with connector prefix) + `renderTreeLogLines()` (inline `⎿` log output). Both `renderProgressStepLine()` and `renderTreeStepLine()` delegate to `renderStepLineWithPrefix()` for shared layout (icon + name + right-aligned duration).

**Plain mode**: Sequential `[run]`/`[ok]`/`[fail]` lines, internal steps hidden via IsInternal callback, dedup on status transitions

**Domain helpers** (moved to `pkg/whail/progress.go`): `whail.IsInternalStep(name)`, `whail.CleanStepName(name)`, `whail.ParseBuildStage(name)`, `whail.FormatBuildDuration(d)`

## Lifecycle Hooks (`hooks.go`)

Generic lifecycle hook mechanism for TUI components. Hooks fire at key moments during component execution, enabling callers to inject behavior (pausing, logging, test assertions) without the TUI package knowing about the caller's domain.

```go
// HookResult controls execution flow after a lifecycle hook fires.
type HookResult struct {
    Continue bool   // false = quit execution
    Message  string // reason for quitting (only meaningful when Continue=false)
    Err      error  // hook's own failure (independent of Continue)
}

// LifecycleHook is called at key moments during TUI component execution.
type LifecycleHook func(component, event string) HookResult
```

**Wiring**: Hooks are threaded via component config structs. `ProgressDisplayConfig.OnLifecycle` is the first; future components follow the same pattern. Nil hooks are never called — each config struct has a nil-safe `fireHook()` helper.

**Firing**: Hooks fire AFTER BubbleTea exits (no stdin conflict) but BEFORE the summary is rendered. `View()` handles both live and finished states, ensuring the progress display persists in BubbleTea's final frame.

**Hook abort handling**: `handleHookResult()` converts a `HookResult` to a `ProgressResult`. When a hook aborts (`Continue: false`) with no error or message, a default error "aborted by lifecycle hook" is produced — preventing silent success on abort.

**Hook events for progress display**:
- `"progress"`, `"before_complete"` — fired after all steps complete, before summary

**Factory threading**: `cmdutil.Factory.TUI` → `BuildOptions.TUI` → `TUI.RunProgress()` injects hooks into `ProgressDisplayConfig.OnLifecycle`. Hooks registered on TUI struct via `.RegisterHooks()` in PersistentPreRunE; pointer sharing ensures commands see them.

## TUI Struct (`tui.go`)

The `TUI` struct is the Factory noun for the presentation layer. Commands receive it eagerly; hooks are registered post-construction.

```go
type TUI struct {
    ios   *iostreams.IOStreams
    hooks []LifecycleHook
}

func NewTUI(ios *iostreams.IOStreams) *TUI
func (t *TUI) RegisterHooks(hooks ...LifecycleHook)
func (t *TUI) RunProgress(mode string, cfg ProgressDisplayConfig, ch <-chan ProgressStep) ProgressResult
func (t *TUI) RunWizard(fields []WizardField) (WizardResult, error)
func (t *TUI) IOStreams() *iostreams.IOStreams
```

- `NewTUI(ios)` -- constructor, binds to IOStreams
- `RegisterHooks(hooks...)` -- appends lifecycle hooks; hooks fire in registration order
- `RunProgress(mode, cfg, ch)` -- delegates to package-level `RunProgress()`, injecting composed hooks into `cfg.OnLifecycle` if caller hasn't set one explicitly
- `RunWizard(fields)` -- runs multi-step wizard via `RunProgram` with alt screen; returns collected values and submit/cancel status
- `IOStreams()` -- accessor for the underlying IOStreams

**Hook composition**: Multiple hooks are composed into a single `LifecycleHook` that fires in order. First abort (`Continue=false`) or error short-circuits remaining hooks.

**Pointer-sharing pattern**: TUI is constructed eagerly in Factory and captured by commands at `NewCmd` time. Hooks are registered later in `PersistentPreRunE` (after flag parsing). Since it's a pointer, commands see the hooks when `RunE` fires. This fixes the `--step` flag bug where hooks were captured eagerly before flag values were resolved.

## TablePrinter (`table.go`)

TTY-aware table rendering for command output. Styled mode delegates to `iostreams.RenderStyledTable` (backed by `lipgloss/table`). Plain mode uses `text/tabwriter`.

```go
tp := t.NewTable("IMAGE", "ID", "CREATED", "SIZE")
tp.AddRow("myapp:latest", "a1b2c3d4e5f6", "2 months ago", "256.00MB")
tp.AddRow("node:20-slim", "b2c3d4e5f678", "3 months ago", "180.00MB")
err := tp.Render() // writes to ios.Out
```

**Constructors**: `(*TUI).NewTable(headers ...string) *TablePrinter`

**Methods**: `AddRow(cols ...string)`, `Len() int`, `Render() error`

**Styled mode** (TTY + color): Delegates to `iostreams.RenderStyledTable`, which uses `lipgloss/table` with `StyleFunc` for per-cell styling. Headers are muted uppercase (`TableHeaderStyle`). First column uses brand color (`TablePrimaryColumnStyle`). All borders disabled. Column widths auto-sized by `lipgloss/table`'s median-based resizer to fit terminal width.

**Plain mode** (non-TTY/piped): `text/tabwriter` with 2-space gaps, tab-separated. Machine-friendly output, no ANSI sequences.

**Golden tests**: `GOLDEN_UPDATE=1 go test ./internal/tui/... -run "TestTable.*_Golden" -v`

## Field Models (`fields.go`)

Three standalone BubbleTea models for form fields. Each is independently usable without a wizard — commands can use a single `SelectField` for a one-off choice, or compose multiple fields manually. All use value semantics (setters return copies).

### FieldOption

```go
type FieldOption struct { Label string; Description string }
```

Shared option type used by `SelectField` and `WizardField`.

### SelectField

Arrow-key selection from a list of options. Wraps `ListModel` for navigation state, renders compact label+description view.

```go
func NewSelectField(id, prompt string, options []FieldOption, defaultIdx int) SelectField
func (f SelectField) Init() tea.Cmd
func (f SelectField) Update(msg tea.Msg) (SelectField, tea.Cmd)
func (f SelectField) View() string
func (f SelectField) Value() string          // selected option's Label
func (f SelectField) SelectedIndex() int
func (f SelectField) IsConfirmed() bool      // true after Enter
func (f SelectField) SetSize(w, h int) SelectField
```

**Keys**: Up/Down (or j/k) navigate, Enter confirms (sends `tea.Quit` for standalone use), Ctrl+C quits without confirming.

### TextField

Text input with optional validation. Wraps `bubbles/textinput`.

```go
func NewTextField(id, prompt string, opts ...TextFieldOption) TextField
// Options: WithPlaceholder, WithDefault, WithValidator, WithRequired
func (f TextField) Init() tea.Cmd         // returns textinput.Blink
func (f TextField) Update(msg tea.Msg) (TextField, tea.Cmd)
func (f TextField) View() string
func (f TextField) Value() string
func (f TextField) IsConfirmed() bool
func (f TextField) Err() string           // validation error message
func (f TextField) SetSize(w, h int) TextField
```

**Keys**: Type to input, Enter validates + confirms, Ctrl+C quits. Required check runs before custom validator.

### ConfirmField

Yes/No toggle.

```go
func NewConfirmField(id, prompt string, defaultYes bool) ConfirmField
func (f ConfirmField) Init() tea.Cmd
func (f ConfirmField) Update(msg tea.Msg) (ConfirmField, tea.Cmd)
func (f ConfirmField) View() string
func (f ConfirmField) Value() string      // "yes" or "no"
func (f ConfirmField) BoolValue() bool
func (f ConfirmField) IsConfirmed() bool
func (f ConfirmField) SetSize(w, h int) ConfirmField
```

**Keys**: Left/Right/Tab toggle, y/n set directly, Enter confirms, Ctrl+C quits.

## StepperBar (`stepper.go`)

Pure render function for horizontal step progress indicator. Reusable in any multi-phase flow (wizards, deployments, pipelines).

```go
type StepState int
const (StepPendingState, StepActiveState, StepCompleteState, StepSkippedState)

type Step struct { Title string; Value string; State StepState }

func RenderStepperBar(steps []Step, width int) string
```

**Icons**: `✓` (SuccessStyle) complete, `◉` (TitleStyle) active, `○` (MutedStyle) pending. Skipped steps hidden. Separator ` → ` in MutedStyle. Truncates to width.

**Example**: `✓ Build Image: Yes  →  ◉ Flavor  →  ○ Submit`

## WizardModel (`wizard.go`)

Multi-step form composing field models + stepper bar with navigation. Runs in alt screen via `TUI.RunWizard`.

### Public Types

```go
type WizardFieldKind int // FieldSelect, FieldText, FieldConfirm

type WizardField struct {
    ID, Title, Prompt string
    Kind  WizardFieldKind
    // Select: Options []FieldOption, DefaultIdx int
    // Text: Placeholder, Default string, Validator func(string) error, Required bool
    // Confirm: DefaultYes bool
    SkipIf func(WizardValues) bool  // conditional skip
}

type WizardValues map[string]string
type WizardResult struct { Values WizardValues; Submitted bool }
```

### Navigation

| Key | Action |
|-----|--------|
| Enter | Confirm field → advance to next visible step (submit on last) |
| Esc | Go back to previous visible step (cancel on first) |
| Ctrl+C | Cancel wizard |
| Up/Down | Delegated to current field (SelectField list) |
| Left/Right | Delegated to current field (ConfirmField toggle) |

`SkipIf` predicates are respected in both forward and backward navigation.

### TUI Integration

```go
result, err := t.RunWizard(fields)
// result.Submitted: true if wizard completed, false if cancelled
// result.Values: map of field ID → string value
```

Uses `RunProgram(ios, model, WithAltScreen(true))`. Internal `wizardModel` uses pointer receivers (map mutation). `filterQuit` prevents individual fields from terminating the wizard.

### View Layout

```
  ✓ Build Image: Yes  →  ◉ Flavor  →  ○ Submit     ← StepperBar
  Select Linux flavor                                 ← Field prompt
  › bookworm     Debian stable (Recommended)          ← Field body
    trixie       Debian testing
  ↑↓ select  enter confirm  esc back  ctrl+c quit    ← QuickHelp
```

## Tests & Limitations

Every file has a corresponding `*_test.go` with `testify/assert`.

- `CountVisibleWidth` counts runes, not true visual width (CJK chars counted as 1)
- `StripANSI` may not handle all nested ANSI sequences
- Spinner requires `Init()` and tick message handling in BubbleTea Update loop
- `import_boundary_test.go` enforces that no non-test `.go` files import lipgloss

### Golden Output Tests (`progress_golden_test.go`)

Golden snapshot tests for `RunProgress` in plain mode. Each JSON scenario from `pkg/whail/whailtest/testdata/` is played through the progress display with deterministic config (`FormatDuration` returns "0.0s") and compared against `.golden` files in `testdata/`.

Uses inline golden helper (avoids `test/harness` heavy transitive deps). Regenerate: `GOLDEN_UPDATE=1 go test ./internal/tui/... -run TestProgressPlain_Golden -v`

Golden files: `internal/tui/testdata/TestProgressPlain_Golden_*/*.golden`

### Table Golden Tests (`table_golden_test.go`)

Golden snapshot tests for `TablePrinter` in both plain and styled modes. Tests use `forceColorProfile(t)` (via `lipgloss.SetColorProfile`) to ensure styled output emits ANSI regardless of writer type.

Regenerate: `GOLDEN_UPDATE=1 go test ./internal/tui/... -run "TestTable.*_Golden" -v`

Golden files: `internal/tui/testdata/TestTable{Plain,Styled}_Golden_*/*.golden`
