# TUI Components Package

Reusable BubbleTea components for terminal UIs. Stateless render functions + value-type models (immutable setters return copies).

## Architecture

**Import boundary**: This package does NOT import `lipgloss` directly. All colors, styles, text/layout/time utilities are re-exported from `internal/iostreams` via `iostreams.go`. The `import_boundary_test.go` enforces this constraint.

**Allowed imports**: `bubbletea`, `bubbles/*`, `internal/iostreams`. The tui package sits one layer above iostreams in the DAG — it adds BubbleTea interactivity on top of iostreams' visual primitives.

**Style usage pattern**: Since `lipgloss.Style` cannot appear in type signatures without importing lipgloss, functions that would return a style instead return `func(string) string` (a render function). Styles can still be used inline via type inference: `style := PanelStyle` (Go infers the lipgloss.Style type from the re-exported variable).

## File Overview

| File | Purpose |
|------|---------|
| `iostreams.go` | Re-export shim: colors, styles, tokens, text, layout, time from iostreams |
| `keys.go` | `KeyMap` struct, `DefaultKeyMap()`, `Is*` key matchers |
| `components.go` | Stateless renders: header, status, badge, progress, table, divider |
| `spinner.go` | Animated spinner wrapping bubbles/spinner |
| `panel.go` | Bordered panels with focus, `PanelGroup` for multi-panel layouts |
| `list.go` | Selectable list with scrolling, `ListItem` interface |
| `statusbar.go` | Status bar with left/center/right sections, indicator helpers |
| `viewport.go` | Scrollable content viewport wrapping bubbles/viewport |
| `help.go` | Help bar/grid, binding presets, `QuickHelp` |
| `program.go` | `RunProgram` helper for running BubbleTea programs with IOStreams |
| `import_boundary_test.go` | Enforces no lipgloss imports in non-test files |

## Re-exports (`iostreams.go`)

All of the following are delegated to `internal/iostreams`:

**Colors**: `ColorPrimary`, `ColorSecondary`, `ColorSuccess`, `ColorWarning`, `ColorError`, `ColorInfo`, `ColorMuted`, `ColorHighlight`, `ColorDisabled`, `ColorSelected`, `ColorBorder`, `ColorAccent`, `ColorBg`, `ColorBgAlt`

**Text styles**: `TitleStyle`, `SubtitleStyle`, `ErrorStyle`, `SuccessStyle`, `WarningStyle`, `MutedStyle`, `HighlightStyle`, `AccentStyle`, `DisabledStyle`, `BlueStyle`, `CyanStyle`

**Border styles**: `BorderStyle`, `BorderActiveStyle`, `BorderMutedStyle`

**Component styles**: `PanelStyle`, `PanelActiveStyle`, `PanelTitleStyle`, `HeaderStyle`, `HeaderTitleStyle`, `HeaderSubtitleStyle`, `ListItemStyle`, `ListItemSelectedStyle`, `ListItemDimStyle`, `HelpKeyStyle`, `HelpDescStyle`, `HelpSeparatorStyle`, `LabelStyle`, `ValueStyle`, `CountStyle`, `DividerStyle`, `EmptyStateStyle`, `StatusBarStyle`, `TagStyle`

**Status styles**: `StatusRunningStyle`, `StatusStoppedStyle`, `StatusErrorStyle`, `StatusWarningStyle`, `StatusInfoStyle`

**Badge styles**: `BadgeStyle`, `BadgeSuccessStyle`, `BadgeWarningStyle`, `BadgeErrorStyle`, `BadgeMutedStyle`

**Tokens**: `SpaceNone` (0), `SpaceXS` (1), `SpaceSM` (2), `SpaceMD` (4), `SpaceLG` (8), `WidthCompact` (60), `WidthNormal` (80), `WidthWide` (120)

**Layout mode**: `LayoutMode` type alias, `LayoutCompact`, `LayoutNormal`, `LayoutWide`, `GetLayoutMode(width)`

**Math**: `MinInt`, `MaxInt`, `ClampInt`, `GetContentWidth`, `GetContentHeight`

**Text**: `Truncate`, `TruncateMiddle`, `PadRight`, `PadLeft`, `PadCenter`, `WordWrap`, `WrapLines`, `CountVisibleWidth`, `StripANSI`, `Indent`, `JoinNonEmpty`, `Repeat`, `FirstLine`, `LineCount`

**Layout**: `SplitConfig`, `GridConfig`, `BoxConfig`, `ResponsiveLayout` (type aliases), `DefaultSplitConfig`, `SplitHorizontal`, `SplitVertical`, `Stack`, `Row`, `Columns`, `FlexRow`, `Grid`, `Box`, `CenterInRect`, `AlignLeft`, `AlignRight`, `AlignCenter`

**Time**: `FormatRelative`, `FormatDuration`, `FormatUptime`, `FormatDate`, `FormatDateTime`, `FormatTimestamp`

**Status helpers** (wrapped to avoid lipgloss in return types):
- `StatusStyle(running bool) func(string) string` — returns render function, not lipgloss.Style
- `StatusText(running bool) string`
- `StatusIndicator(status string) (string, string)` — returns (rendered_indicator, symbol), not (lipgloss.Style, string)

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

## Tests & Limitations

Every file has a corresponding `*_test.go` with `testify/assert`.

- `CountVisibleWidth` counts runes, not true visual width (CJK chars counted as 1)
- `StripANSI` may not handle all nested ANSI sequences
- Spinner requires `Init()` and tick message handling in BubbleTea Update loop
- `import_boundary_test.go` enforces that no non-test `.go` files import lipgloss
