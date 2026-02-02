# TUI Components Package

Reusable BubbleTea/Lipgloss components for terminal UIs. Stateless render functions + value-type models (immutable setters return copies).

## File Overview

| File | Purpose |
|------|---------|
| `tokens.go` | Design tokens: spacing, breakpoints, `LayoutMode`, utility math |
| `text.go` | Text manipulation: truncate, pad, wrap, ANSI-aware width |
| `time.go` | Time formatting: relative, duration, uptime, timestamps |
| `styles.go` | Lipgloss color palette, text/border/component/badge/status styles |
| `keys.go` | `KeyMap` struct, `DefaultKeyMap()`, `Is*` key matchers |
| `layout.go` | Layout composition: split, stack, row, grid, box, responsive |
| `components.go` | Stateless renders: header, status, badge, progress, table, divider |
| `spinner.go` | Animated spinner wrapping bubbles/spinner |
| `panel.go` | Bordered panels with focus, `PanelGroup` for multi-panel layouts |
| `list.go` | Selectable list with scrolling, `ListItem` interface |
| `statusbar.go` | Status bar with left/center/right sections, indicator helpers |
| `help.go` | Help bar/grid, binding presets, `QuickHelp` |

## Design Tokens (`tokens.go`)

**Spacing**: `SpaceNone` (0), `SpaceXS` (1), `SpaceSM` (2), `SpaceMD` (4), `SpaceLG` (8)

**Breakpoints**: `WidthCompact` (60), `WidthNormal` (80), `WidthWide` (120)

**Layout mode**: `LayoutMode` type -- `LayoutCompact`, `LayoutNormal`, `LayoutWide`

`GetLayoutMode(width)`, `GetContentWidth(totalWidth, padding)`, `GetContentHeight(totalHeight, headerH, footerH)`

**Math**: `MinInt`, `MaxInt`, `ClampInt`

## Styles (`styles.go`)

**Colors**: `ColorPrimary`, `ColorSecondary`, `ColorSuccess`, `ColorWarning`, `ColorError`, `ColorInfo`, `ColorMuted`, `ColorHighlight`, `ColorDisabled`, `ColorSelected`, `ColorBorder`, `ColorAccent`, `ColorBg`, `ColorBgAlt`

**Text**: `TitleStyle`, `SubtitleStyle`, `ErrorStyle`, `SuccessStyle`, `WarningStyle`, `MutedStyle`, `HighlightStyle`

**Borders**: `BorderStyle`, `BorderActiveStyle`, `BorderMutedStyle`

**Panel**: `PanelStyle`, `PanelActiveStyle`, `PanelTitleStyle` | **Header**: `HeaderStyle`, `HeaderTitleStyle`, `HeaderSubtitleStyle`

**List**: `ListItemStyle`, `ListItemSelectedStyle`, `ListItemDimStyle` | **Help**: `HelpKeyStyle`, `HelpDescStyle`, `HelpSeparatorStyle`

**Label/value**: `LabelStyle`, `ValueStyle`, `CountStyle` | **Other**: `DividerStyle`, `EmptyStateStyle`

**Status**: `StatusRunningStyle`, `StatusStoppedStyle`, `StatusErrorStyle`, `StatusWarningStyle`, `StatusInfoStyle`

**Badges**: `BadgeStyle`, `BadgeSuccessStyle`, `BadgeWarningStyle`, `BadgeErrorStyle`, `BadgeMutedStyle`

**Functions**: `StatusStyle(running bool)`, `StatusText(running bool)`, `StatusIndicator(status string) (Style, string)`

## Keys (`keys.go`)

`KeyMap` struct: `Quit`, `Up`, `Down`, `Left`, `Right`, `Enter`, `Escape`, `Help`, `Tab` (all `key.Binding`)

`DefaultKeyMap()` -- standard vim-style + arrow key bindings

**Matchers** (take `tea.KeyMsg`, return bool): `IsQuit`, `IsUp`, `IsDown`, `IsLeft`, `IsRight`, `IsEnter`, `IsEscape`, `IsHelp`, `IsTab`

## Text Utilities (`text.go`)

`Truncate(s, maxLen)`, `TruncateMiddle(s, maxLen)` -- ANSI-aware truncation with ellipsis

`PadRight`, `PadLeft`, `PadCenter` -- ANSI-aware padding to width

`WordWrap(s, width)`, `WrapLines(s, width) []string` -- word-boundary wrapping

`CountVisibleWidth(s)`, `StripANSI(s)` -- ANSI escape handling

`Indent(s, prefix)`, `JoinNonEmpty(sep, parts...)`, `Repeat(s, n)`, `FirstLine(s)`, `LineCount(s)`

## Time Utilities (`time.go`)

`FormatRelative(t)` -- "2 hours ago", "in 5 minutes" | `FormatDuration(d)` -- "2h 30m" compact

`FormatUptime(d)` -- "01:15:42" clock format, "Xd HH:MM:SS" for >99h

`FormatTimestamp(t, short)`, `FormatDate(t)`, `FormatDateTime(t)` -- display formatting

`ParseDurationOrDefault(s, defaultVal)` -- safe duration parsing

## Layout (`layout.go`)

**Config types**: `SplitConfig` (Ratio, MinFirst, MinSecond, Gap), `GridConfig` (Columns, Gap, Width), `BoxConfig` (Width, Height, Padding)

`DefaultSplitConfig()` -- 50/50 split, min 10 each, gap 1

`SplitHorizontal(width, SplitConfig) (leftW, rightW)`, `SplitVertical(height, SplitConfig) (topH, bottomH)`

`Stack(spacing, ...string)` -- vertical | `Row(spacing, ...string)` -- horizontal | `Columns(width, gap, ...string)` -- equal-width

`FlexRow(width, left, center, right)` -- distributed spacing across width

`Grid(GridConfig, ...string)` -- multi-row grid | `Box(BoxConfig, content)` -- fixed-size box

`CenterInRect(content, w, h)`, `AlignLeft`, `AlignCenter`, `AlignRight` -- alignment within width

`ResponsiveLayout` struct with `Compact`, `Normal`, `Wide` func fields + `Render(width)` method

## Stateless Render Functions (`components.go`)

**Config types**: `HeaderConfig` (Title, Subtitle, Timestamp, Width), `StatusConfig` (Status, Label), `ProgressConfig` (Current, Total, Width, ShowBar), `TableConfig` (Headers, Rows, ColWidths, Width), `KeyValuePair` (Key, Value)

`RenderHeader(HeaderConfig)`, `RenderStatus(StatusConfig)` -- title bar, status indicator

`RenderBadge(text, style)`, `RenderCountBadge(count, label)` -- inline badges

`RenderProgress(ProgressConfig)` -- text "3/10" or visual bar

`RenderDivider(width)`, `RenderLabeledDivider(label, width)` -- horizontal rules

`RenderEmptyState(message, w, h)`, `RenderError(err, width)` -- state displays

`RenderLabelValue(label, value)`, `RenderKeyValueTable([]KeyValuePair, width)` -- key-value rendering

`RenderTable(TableConfig)` -- headers + divider + rows

`RenderPercentage(float64)` -- color-coded (>=80 error, >=60 warning) | `RenderBytes(int64)` -- "1.5 GB"

`RenderTag(text, color)`, `RenderTags([]string, color)` -- bordered tag elements

## Interactive Components

All models use value semantics -- setters return new copies. Each has `View() string`.

### SpinnerModel (`spinner.go`)

`SpinnerType` constants: `SpinnerDots`, `SpinnerLine`, `SpinnerMiniDots`, `SpinnerJump`, `SpinnerPulse`, `SpinnerPoints`, `SpinnerGlobe`, `SpinnerMoon`, `SpinnerMonkey`

`NewSpinner(SpinnerType, label)`, `NewDefaultSpinner(label)` -- constructors

BubbleTea: `Init() tea.Cmd`, `Update(tea.Msg) (SpinnerModel, tea.Cmd)`, `View()`, `Tick() tea.Msg`

Setters: `SetLabel`, `SetStyle`, `SetSpinnerStyle`, `SetSpinnerType` | Type alias: `SpinnerTickMsg`

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

Setters: `SetLeft`, `SetCenter`, `SetRight`, `SetWidth`, `SetStyle` | Getters: `Left`, `Center`, `Right`, `Width`

`StatusBarSection` (Content, Style) for `RenderStatusBarWithSections([]StatusBarSection, width)`

Convenience: `RenderStatusBar(left, center, right, width)`

**Indicators**: `ModeIndicator(mode, active)`, `ConnectionIndicator(connected)`, `TimerIndicator(label, value)`, `CounterIndicator(label, current, total)`

### HelpModel (`help.go`)

`HelpConfig` (Width, ShowAll, Separator). `DefaultHelpConfig()`.

`NewHelp(HelpConfig)` -- help bar from key bindings

Setters: `SetBindings`, `SetWidth`, `SetShowAll`, `SetSeparator` | Methods: `View()`, `ShortHelp()`, `FullHelp()`, `Bindings()`

**Standalone**: `RenderHelpBar(bindings, width)`, `RenderHelpGrid(bindings, columns, width)`

**Binding presets**: `NavigationBindings()`, `QuitBindings()`, `AllBindings()` -- return `[]key.Binding`

**Quick helpers**: `HelpBinding(keys, desc)`, `QuickHelp(pairs ...string)` -- inline help strings

## Tests & Limitations

Every file has a corresponding `*_test.go` with `testify/assert`.

- `CountVisibleWidth` counts runes, not true visual width (CJK chars counted as 1)
- `StripANSI` may not handle all nested ANSI sequences
- Spinner requires `Init()` and tick message handling in BubbleTea Update loop
