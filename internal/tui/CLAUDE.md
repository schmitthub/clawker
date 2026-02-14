# TUI Components Package

Reusable BubbleTea components for terminal UIs. Stateless render functions + value-type models (immutable setters return copies).

## Architecture

**Import boundary**: Does NOT import `lipgloss` or `lipgloss/table` directly. Styles via `iostreams` (e.g., `iostreams.PanelStyle`), text via `internal/text`. Enforced by `import_boundary_test.go`.

**Allowed imports**: `bubbletea`, `bubbles/*`, `internal/iostreams`, `internal/text`.

**Style pattern**: Use `func(string) string` instead of `lipgloss.Style` in signatures. Inline via type inference: `style := iostreams.PanelStyle`.

**Composition principle**: TUI provides generic reusable components — it does NOT contain consumer-specific logic. If you need a special view that doesn't exist, create a generic one in tui that can be customized or expanded upon in the command layer package you need it in. For example, `RunDashboard` is a generic channel-driven dashboard; `cmd/loop/shared/` implements `DashboardRenderer` to create the loop-specific dashboard. Importing bubbletea types for interface implementation is acceptable in consumer packages.

## Keys (`keys.go`)

`KeyMap` struct with `Quit`, `Up`, `Down`, `Left`, `Right`, `Enter`, `Escape`, `Help`, `Tab`. `DefaultKeyMap()`.

**Matchers** (take `tea.KeyMsg`, return bool): `IsQuit`, `IsUp`, `IsDown`, `IsLeft`, `IsRight`, `IsEnter`, `IsEscape`, `IsHelp`, `IsTab`

## Stateless Render Functions (`components.go`)

**Config types**: `HeaderConfig`, `StatusConfig`, `ProgressConfig`, `TableConfig`, `KeyValuePair`

| Function | Purpose |
|----------|---------|
| `RenderHeader(HeaderConfig)` | Title bar with optional subtitle/timestamp |
| `RenderStatus(StatusConfig)` | Status indicator with label |
| `RenderBadge(text, ...func(string) string)` | Inline badge |
| `RenderCountBadge(count, label)` | Count with label |
| `RenderProgress(ProgressConfig)` | Text "3/10" or visual bar |
| `RenderDivider(width)`, `RenderLabeledDivider(label, width)` | Horizontal rules |
| `RenderEmptyState(message, w, h)`, `RenderError(err, width)` | State displays |
| `RenderLabelValue`, `RenderKeyValueTable`, `RenderTable` | Key-value and tabular |
| `RenderPercentage(float64)`, `RenderBytes(int64)` | Color-coded percentage, human bytes |
| `RenderTag(text, ...func(string) string)`, `RenderTags([]string, ...)` | Bordered tags |

## Interactive Components

All models use value semantics — setters return new copies. Each has `Init()`, `Update()`, `View()`.

### SpinnerModel (`spinner.go`)

Types: `SpinnerDots`, `SpinnerLine`, `SpinnerMiniDots`, `SpinnerJump`, `SpinnerPulse`, `SpinnerPoints`, `SpinnerGlobe`, `SpinnerMoon`, `SpinnerMonkey`. `NewSpinner(type, label)`, `NewDefaultSpinner(label)`. Setters: `SetLabel`, `SetSpinnerType`.

### PanelModel (`panel.go`)

`NewPanel(PanelConfig)` — bordered container with focus. Setters: `SetContent/Title/Focused/Width/Height/Padding`. Getters: `Width/Height/Title/Content/IsFocused`.

Convenience: `RenderInfoPanel`, `RenderDetailPanel`, `RenderScrollablePanel`.

**PanelGroup**: `NewPanelGroup(...PanelModel)` — focus management. Methods: `Add`, `FocusNext/Prev`, `Focus(idx)`, `FocusedPanel`, `RenderHorizontal/Vertical(gap)`.

### ListModel (`list.go`)

`ListItem` interface: `Title()`, `Description()`, `FilterValue()`. `SimpleListItem` implements it. `NewList(ListConfig)`.

Navigation: `SelectNext/Prev/First/Last`, `Select(idx)`, `PageUp/Down`. Setters: `SetItems/Width/Height`. Getters: `SelectedItem/Index`, `Items`, `Len`, `IsEmpty`.

### StatusBarModel (`statusbar.go`)

`NewStatusBar(width)` — left/center/right sections. `StatusBarSection{Content, Render}`. `RenderStatusBar(left, center, right, width)`.

**Indicators**: `ModeIndicator`, `ConnectionIndicator`, `TimerIndicator`, `CounterIndicator`

### ViewportModel (`viewport.go`)

`NewViewport(ViewportConfig)` — scrollable content wrapping `bubbles/viewport`. Setters: `SetContent/Size/Title`. Scroll: `ScrollToTop/Bottom`. Queries: `AtTop/Bottom`, `ScrollPercent`.

### HelpModel (`help.go`)

`NewHelp(HelpConfig)` — help bar from key bindings. `RenderHelpBar`, `RenderHelpGrid`. Presets: `NavigationBindings()`, `QuitBindings()`, `AllBindings()`. Helpers: `HelpBinding(keys, desc)`, `QuickHelp(pairs...)`.

## RunProgram (`program.go`)

`RunProgram(ios, model, opts...) (tea.Model, error)` — runs BubbleTea with IOStreams. Options: `WithAltScreen(bool)`, `WithMouseMotion(bool)`.

## Generic Progress Display (`progress.go`)

Multi-step progress display — BubbleTea for TTY, sequential text for plain. Zero domain knowledge; callbacks provide domain logic.

**Types**: `ProgressStepStatus` (`StepPending/Running/Complete/Cached/Error`), `ProgressStep`, `ProgressDisplayConfig`, `ProgressResult`

**Entry point**: `RunProgress(ios, mode, cfg, ch)` — mode: `"auto"`, `"plain"`, `"tty"`

**ProgressDisplayConfig fields**: `CompletionVerb` (default: "Completed"), `AltScreen` (default: false), `MaxVisible`, `LogLines`, `Title`, `Subtitle`

**Callbacks**: `IsInternal(name) bool`, `CleanName(name) string`, `ParseGroup(name) string`, `FormatDuration(d) string`, `OnLifecycle LifecycleHook`

**TTY mode**: Tree-based stage display — collapsed stages (`✓ name ── N steps`), expanded active stage with inline logs. High-water mark frame padding for BubbleTea inline renderer.

**Plain mode**: Sequential `[run]`/`[ok]`/`[fail]` lines, dedup on status transitions.

## Lifecycle Hooks (`hooks.go`)

`HookResult{Continue bool, Message string, Err error}` — controls post-hook flow. `LifecycleHook func(component, event string) HookResult`.

Threaded via config structs (e.g., `ProgressDisplayConfig.OnLifecycle`). Nil = no-op. Fires AFTER BubbleTea exits, BEFORE summary. Abort without error/message produces default error.

## Generic Dashboard (`dashboard.go`)

Reusable channel-driven BubbleTea dashboard framework. Consumer packages implement `DashboardRenderer` to provide domain-specific views.

**DashboardRenderer interface**: `ProcessEvent(ev any)` handles domain events from the channel. `View(cs *iostreams.ColorScheme, width int) string` renders dashboard content (framework handles help line and padding).

**DashboardConfig**: `HelpText` (e.g., `"q detach  ctrl+c stop"`).

**DashboardResult**: `Err` (display error), `Detached` (user pressed q/Esc), `Interrupted` (user pressed Ctrl+C).

**Entry point**: `RunDashboard(ios, renderer, cfg, ch)` — creates internal `dashboardModel`, runs BubbleTea via `RunProgram`, returns result.

**Key bindings**: `q`/`Esc` = detach, `Ctrl+C` = interrupt. Does NOT use the shared `IsQuit` matcher because detach and interrupt have different semantics.

**Internal model**: `Init()` → `waitForDashEvent(ch)`, `Update()` → key handling, window size, event dispatch to `renderer.ProcessEvent()`, channel close detection. `View()` → `renderer.View(cs, width)` + help line + high-water padding.

**Consumers**: `internal/cmd/loop/shared/loopdash.go` implements `DashboardRenderer` for the loop dashboard.

## TUI Struct (`tui.go`)

Factory noun for presentation layer. Commands receive eagerly; hooks registered post-construction via pointer sharing.

```go
func NewTUI(ios *iostreams.IOStreams) *TUI
func (t *TUI) RegisterHooks(hooks ...LifecycleHook)
func (t *TUI) RunProgress(mode string, cfg ProgressDisplayConfig, ch <-chan ProgressStep) ProgressResult
func (t *TUI) RunWizard(fields []WizardField) (WizardResult, error)
func (t *TUI) IOStreams() *iostreams.IOStreams
```

Multiple hooks composed in order; first abort/error short-circuits.

## TablePrinter (`table.go`)

TTY-aware table rendering. Styled mode → `iostreams.RenderStyledTable` (`lipgloss/table`). Plain mode → `text/tabwriter`.

```go
tp := t.NewTable("IMAGE", "ID", "CREATED", "SIZE")
tp.AddRow("myapp:latest", "a1b2c3d4e5f6", "2 months ago", "256.00MB")
err := tp.Render()
```

**Constructor**: `(*TUI).NewTable(headers ...string) *TablePrinter`

**Methods**: `AddRow(cols ...string)`, `Len() int`, `Render() error`, `WithHeaderStyle(fn func(string) string) *TablePrinter`, `WithPrimaryStyle(fn func(string) string) *TablePrinter`, `WithCellStyle(fn func(string) string) *TablePrinter`

**Golden tests**: `GOLDEN_UPDATE=1 go test ./internal/tui/... -run "TestTable.*_Golden" -v`

## Field Models (`fields.go`)

Three standalone BubbleTea field models. All use value semantics.

**FieldOption**: `{Label, Description string}` — shared by `SelectField` and `WizardField`.

**SelectField**: `NewSelectField(id, prompt, options, defaultIdx)`. Arrow-key selection. Methods: `Value()`, `SelectedIndex()`, `IsConfirmed()`, `SetSize(w, h)`.

**TextField**: `NewTextField(id, prompt, opts...)` with `WithPlaceholder/Default/Validator/Required`. Methods: `Value()`, `IsConfirmed()`, `Err()`, `SetSize(w, h)`.

**ConfirmField**: `NewConfirmField(id, prompt, defaultYes)`. Left/Right/Tab toggle. Methods: `Value()` ("yes"/"no"), `BoolValue()`, `IsConfirmed()`, `SetSize(w, h)`.

## StepperBar (`stepper.go`)

`RenderStepperBar(steps []Step, width int) string` — horizontal step progress. `Step{Title, Value, State}`, `StepState` (`StepPendingState/ActiveState/CompleteState/SkippedState`). Icons: `✓` complete, `◉` active, `○` pending.

## WizardModel (`wizard.go`)

Multi-step form via `TUI.RunWizard(fields)`. Returns `WizardResult{Values WizardValues, Submitted bool}`.

`WizardField{ID, Title, Prompt, Kind, Options, SkipIf}` — `WizardFieldKind`: `FieldSelect`, `FieldText`, `FieldConfirm`.

Navigation: Enter advance, Esc back, Ctrl+C cancel. `SkipIf` predicates respected in both directions.

## Golden Tests

- Progress: `GOLDEN_UPDATE=1 go test ./internal/tui/... -run TestProgressPlain_Golden -v`
- Tables: `GOLDEN_UPDATE=1 go test ./internal/tui/... -run "TestTable.*_Golden" -v`
