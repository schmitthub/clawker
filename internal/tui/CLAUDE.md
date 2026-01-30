# TUI Components Package

Reusable BubbleTea components for building terminal user interfaces in clawker.

## File Overview

| File | Purpose |
|------|---------|
| `tokens.go` | Design tokens: spacing constants (`SpaceNone`-`SpaceLG`), layout breakpoints (`WidthCompact`/`WidthNormal`/`WidthWide`), `GetLayoutMode()`, `GetContentWidth()` |
| `text.go` | Text manipulation: `Truncate`, `TruncateMiddle`, `PadRight`/`PadLeft`/`PadCenter`, `WordWrap`, `CountVisibleWidth`, `StripANSI` |
| `time.go` | Time formatting: `FormatRelative`, `FormatDuration`, `FormatUptime`, `FormatTimestamp`, `FormatDate` |
| `styles.go` | Lipgloss styles and colors |
| `keys.go` | Key bindings: `DefaultKeyMap()`, `IsQuit`/`IsUp`/`IsDown`/`IsLeft`/`IsRight`/`IsEnter`/`IsEscape`/`IsTab` |
| `layout.go` | Layout composition: `SplitHorizontal`/`SplitVertical`, `Stack`, `Row`, `Columns`, `FlexRow`, `Grid`, `Box`, `ResponsiveLayout` |
| `components.go` | Stateless renders: `RenderHeader`, `RenderStatus`, `RenderBadge`, `RenderProgress`, `RenderDivider`, `RenderTable`, `RenderKeyValueTable` |
| `spinner.go` | Animated spinner with multiple styles (`SpinnerDots`, `SpinnerLine`, etc.) |
| `panel.go` | Bordered panels with focus states, `PanelGroup` for multi-panel layouts |
| `list.go` | Selectable list with scrolling: `ListItem` interface, `SimpleListItem`, navigation methods |
| `statusbar.go` | Status bar with left/center/right sections, pre-built indicators (`ModeIndicator`, `ConnectionIndicator`, `TimerIndicator`) |
| `help.go` | Help bar from `[]key.Binding`, `RenderHelpBar`, `QuickHelp` |

## Colors & Styles

```go
// Colors
tui.ColorPrimary   // Purple (#7C3AED)
tui.ColorSuccess   // Green (#10B981)
tui.ColorWarning   // Amber (#F59E0B)
tui.ColorError     // Red (#EF4444)
tui.ColorInfo      // Sky blue (#87CEEB)
tui.ColorMuted     // Gray (#6B7280)

// Component styles
tui.HeaderStyle, tui.PanelStyle, tui.PanelActiveStyle
tui.ListItemStyle, tui.ListItemSelectedStyle
tui.HelpKeyStyle, tui.HelpDescStyle
tui.BadgeStyle, tui.StatusRunningStyle, tui.StatusStoppedStyle
```

## Layout Helpers

```go
leftW, rightW := tui.SplitHorizontal(width, tui.SplitConfig{Ratio: 0.4, MinFirst: 30, MinSecond: 40, Gap: 1})
content := tui.Stack(0, header, body, footer)   // Vertical stack
row := tui.Row(1, col1, col2, col3)             // Horizontal row
columns := tui.Columns(width, 2, items...)      // Equal-width columns
tui.ResponsiveLayout(width, compact, normal, wide)
```

## Interactive Components

### List

```go
list := tui.NewList(tui.ListConfig{Width: 40, Height: 10, ShowDescriptions: true})
list = list.SetItems([]tui.ListItem{
    tui.SimpleListItem{ItemTitle: "Agent 1", ItemDescription: "Running"},
})
list = list.SelectNext()
selected := list.SelectedItem()
```

### Panel & PanelGroup

```go
panel := tui.NewPanel(tui.PanelConfig{Title: "Details", Width: 40, Height: 20, Focused: true})
panel = panel.SetContent("content here")

group := tui.NewPanelGroup(leftPanel, rightPanel)
group = group.FocusNext()  // Tab between panels
```

### Spinner

```go
spinner := tui.NewSpinner(tui.SpinnerDots, "Loading...")
spinner = spinner.SetLabel("Processing...")
// Must call Init() and handle tick messages in Update loop
```

### Status Bar

```go
bar := tui.NewStatusBar(width)
bar = bar.SetLeft(tui.ModeIndicator("running", true)).
    SetCenter(tui.CounterIndicator("Loop", 5, 10)).
    SetRight(tui.TimerIndicator("Uptime", tui.FormatUptime(d)))
```

## Input Handling Pattern

```go
case tea.KeyMsg:
    if tui.IsQuit(msg)   { return m, tea.Quit }
    if tui.IsUp(msg)     { m.list = m.list.SelectPrev() }
    if tui.IsDown(msg)   { m.list = m.list.SelectNext() }
    if tui.IsEnter(msg)  { /* handle selection */ }
    if tui.IsTab(msg)    { m.panelGroup = m.panelGroup.FocusNext() }
```

## Basic TUI Layout Example

```go
func (m Model) View() string {
    header := tui.RenderHeader(tui.HeaderConfig{
        Title: "DASHBOARD", Subtitle: m.project, Width: m.width,
    })
    leftW, rightW := tui.SplitHorizontal(m.width, tui.SplitConfig{Ratio: 0.4})
    content := tui.Row(1, m.renderList(leftW), m.renderDetails(rightW))
    footer := tui.RenderHelpBar(m.getBindings(), m.width)
    return tui.Stack(0, header, content, footer)
}
```

## API Quick Reference

### Text Utilities (`text.go`)

`Truncate`, `TruncateMiddle`, `PadRight`, `PadLeft`, `PadCenter`, `WordWrap`, `WrapLines`, `CountVisibleWidth`, `StripANSI`, `Indent`, `JoinNonEmpty`, `Repeat`, `FirstLine`, `LineCount`

### Components (`components.go`)

`RenderHeader`, `RenderStatus`, `RenderBadge`, `RenderCountBadge`, `RenderProgress`, `RenderDivider`, `RenderLabeledDivider`, `RenderEmptyState`, `RenderError`, `RenderLabelValue`, `RenderKeyValueTable`, `RenderTable`, `RenderPercentage`, `RenderBytes`, `RenderTag`, `RenderTags`

### Styles (`styles.go`)

**Colors**: `ColorPrimary`, `ColorSecondary`, `ColorSuccess`, `ColorWarning`, `ColorError`, `ColorInfo`, `ColorMuted`, `ColorSurface`, `ColorBorder`, `ColorText`, `ColorTextDim`

**Text**: `Bold`, `Dim`, `Italic`, `Underline`, `Code`

**Borders**: `PanelStyle`, `PanelActiveStyle`, `PanelFocusedStyle`

**Components**: `HeaderStyle`, `FooterStyle`, `ListItemStyle`, `ListItemSelectedStyle`, `TableHeaderStyle`, `TableRowStyle`

**Badges**: `BadgeStyle`, `BadgeSuccessStyle`, `BadgeWarningStyle`, `BadgeErrorStyle`

**Status**: `StatusRunningStyle`, `StatusStoppedStyle`, `StatusErrorStyle`, `StatusPendingStyle`

### Keys (`keys.go`)

`DefaultKeyMap()`, `IsQuit`, `IsUp`, `IsDown`, `IsLeft`, `IsRight`, `IsEnter`, `IsEscape`, `IsHelp`, `IsTab`

### Panel (`panel.go`)

`PanelModel`, `NewPanel(PanelConfig)`, `PanelGroup`, `NewPanelGroup(...PanelModel)`

### StatusBar (`statusbar.go`)

`StatusBarModel`, `NewStatusBar(width)`, `RenderStatusBar`, `RenderStatusBarWithSections`, `ModeIndicator`, `ConnectionIndicator`, `TimerIndicator`, `CounterIndicator`

### Help (`help.go`)

`HelpModel`, `NewHelp(bindings, width)`, `RenderHelpBar`, `RenderHelpGrid`, `QuickHelp`

### Spinner (`spinner.go`)

`SpinnerModel`, `NewSpinner(spinnerType, label)`, `NewDefaultSpinner(label)`

Spinner types: `SpinnerDots`, `SpinnerLine`, `SpinnerMiniDot`, `SpinnerJump`, `SpinnerPulse`, `SpinnerPoints`, `SpinnerGlobe`, `SpinnerMoon`, `SpinnerMonkey`, `SpinnerMeter`

### Layout (`layout.go`)

`SplitHorizontal`, `SplitVertical`, `Stack`, `Row`, `Columns`, `FlexRow`, `Grid`, `Box`, `CenterInRect`, `AlignLeft`, `AlignCenter`, `AlignRight`, `ResponsiveLayout`

### List (`list.go`)

`ListModel`, `NewList(ListConfig)`, `ListItem` (interface), `SimpleListItem`

### Tokens (`tokens.go`)

Spacing: `SpaceNone`, `SpaceXS`, `SpaceSM`, `SpaceMD`, `SpaceLG`

Width breakpoints: `WidthCompact`, `WidthNormal`, `WidthWide`

`GetLayoutMode(width)`, `GetContentWidth(width)`, `GetContentHeight(height)`, `MinInt`, `MaxInt`, `ClampInt`

## Known Limitations

- `CountVisibleWidth` counts runes, not true visual width (CJK chars counted as 1)
- `StripANSI` may not handle all nested ANSI sequences
- Spinner requires `Init()` and tick message handling in Update loop
