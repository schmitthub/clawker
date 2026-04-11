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

Full table of render helpers (`RenderHeader`, `RenderStatus`, `RenderBadge`, `RenderProgress`, `RenderDivider`, `RenderTable`, `RenderPercentage`, `RenderBytes`, `RenderTag`, etc.) lives in `.claude/rules/tui.md` — it's auto-loaded whenever you touch `internal/tui/**`. Config types: `HeaderConfig`, `StatusConfig`, `ProgressConfig`, `TableConfig`, `KeyValuePair`.

## Interactive Components

All models use value semantics — setters return new copies. Each has `Init()`, `Update()`, `View()`.

| Component | File | Key API |
|-----------|------|---------|
| `SpinnerModel` | `spinner.go` | `NewSpinner(type, label)`, `NewDefaultSpinner(label)`; types: `SpinnerDots/Line/MiniDots/Jump/Pulse/Points/Globe/Moon/Monkey`; setters `SetLabel`, `SetSpinnerType` |
| `PanelModel` | `panel.go` | `NewPanel(PanelConfig)` — bordered+focusable; setters `SetContent/Title/Focused/Width/Height/Padding`. Convenience: `RenderInfoPanel`, `RenderDetailPanel`, `RenderScrollablePanel` |
| `PanelGroup` | `panel.go` | `NewPanelGroup(...PanelModel)` — focus mgmt via `Add`, `FocusNext/Prev`, `Focus(idx)`, `FocusedPanel`, `RenderHorizontal/Vertical(gap)` |
| `ListModel` | `list.go` | `NewList(ListConfig)` with `ListItem` interface (`Title`/`Description`/`FilterValue`). `SimpleListItem` is the default impl. Navigation: `SelectNext/Prev/First/Last`, `Select(idx)`, `PageUp/Down` |
| `StatusBarModel` | `statusbar.go` | `NewStatusBar(width)` with left/center/right sections. Indicators: `ModeIndicator`, `ConnectionIndicator`, `TimerIndicator`, `CounterIndicator` |
| `ViewportModel` | `viewport.go` | `NewViewport(ViewportConfig)` — wraps `bubbles/viewport`. `SetContent/Size/Title`, `ScrollToTop/Bottom`, `AtTop/Bottom`, `ScrollPercent` |
| `HelpModel` | `help.go` | `NewHelp(HelpConfig)` + `RenderHelpBar`/`RenderHelpGrid`. Presets: `NavigationBindings/QuitBindings/AllBindings`, `HelpBinding(keys, desc)`, `QuickHelp(pairs...)` |

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

Three standalone BubbleTea field models. All use value semantics and share `FieldOption{Label, Description}`.

| Model | Constructor | Methods | Notes |
|-------|-------------|---------|-------|
| `SelectField` | `NewSelectField(id, prompt, options, defaultIdx)` | `Value`, `SelectedIndex`, `IsConfirmed`, `SetSize` | Arrow-key selection |
| `TextField` | `NewTextField(id, prompt, opts...)` | `Value`, `IsConfirmed`, `Err`, `SetSize` | Options: `WithPlaceholder/Default/Validator/Required` |
| `ConfirmField` | `NewConfirmField(id, prompt, defaultYes)` | `Value` ("yes"/"no"), `BoolValue`, `IsConfirmed`, `SetSize` | Left/Right/Tab toggle |

## StepperBar / WizardModel

- `RenderStepperBar(steps, width) string` (`stepper.go`) — horizontal step progress. `Step{Title, Value, State}` with states `StepPendingState/ActiveState/CompleteState/SkippedState`. Icons: `✓` complete, `◉` active, `○` pending.
- `WizardModel` (`wizard.go`) — multi-step form via `TUI.RunWizard(fields)` → `WizardResult{Values, Submitted}`. `WizardField{ID, Title, Prompt, Kind, Options, SkipIf}` with kinds `FieldSelect/Text/Confirm`. Navigation: Enter advance, Esc back, Ctrl+C cancel. `SkipIf` predicates respected in both directions.

## FieldBrowserModel (`fieldbrowser.go`)

Generic tabbed field browser/editor. Domain-agnostic — no knowledge of stores, reflection, or config schemas. Used by `internal/storeui` to edit any `Store[T]`.

**Types**: `BrowserFieldKind` (`BrowserText/Bool/TriState/Select/Int/StringSlice/Duration/Map/StructSlice`), `BrowserField`, `BrowserLayerTarget`, `BrowserLayer`, `BrowserResult`, `BrowserConfig`. Constructor `NewFieldBrowser(cfg)`; result via `.Result() BrowserResult{Saved, Cancelled, SavedCount}`.

Fields are grouped into tabs by top-level path key with sub-section headings for 3+ segment paths. Inline editing dispatches to `SelectField`/`TextField`/`ListEditorModel`/`TextareaEditorModel`/`KVEditorModel` based on kind. Keys: `←/→` tabs, `↑/↓` navigate, `enter` edit, `d` delete (when `OnFieldDeleted` is wired), `esc/q/ctrl+c` quit.

## Inline Editors (`listeditor.go`, `textareaeditor.go`, `kveditor.go`)

All three share the same shape: value-type model, `New<Kind>(label, value, opts...)` constructor, `With<Kind>Validator` option, `Value()`/`IsConfirmed()`/`IsCancelled()`/`Err()` accessors.

| Editor | Value format | Browse keys | Edit keys | Notes |
|--------|-------------|-------------|-----------|-------|
| `ListEditorModel` | Comma-separated string | `a` add, `e` edit, `d/backspace` delete, `↑/↓` nav, `enter/esc` done | `enter` confirm, `esc` cancel | For `[]string` fields |
| `TextareaEditorModel` | Raw string | — (single mode) | `ctrl+s` save, `esc` cancel | Wraps `bubbles/textarea`; auto-sizes height |
| `KVEditorModel` | YAML map string | `a` add pair, `e` edit value, `E` edit key, `d/backspace` delete, `↑/↓` nav, `enter` done, `esc` cancel | `enter` confirm, `esc` cancel | Default for `BrowserMap` fields. Shows merged store state — duplicate key validation belongs at the write boundary, not here |

## Golden Tests

- Progress: `GOLDEN_UPDATE=1 go test ./internal/tui/... -run TestProgressPlain_Golden -v`
- Tables: `GOLDEN_UPDATE=1 go test ./internal/tui/... -run "TestTable.*_Golden" -v`
