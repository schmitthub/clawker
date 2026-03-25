---
description: TUI component guidelines
paths: ["internal/tui/**"]
---

# TUI Rules

- **Import boundary**: `internal/tui/` must NOT import `lipgloss` directly. Use qualified imports from `internal/iostreams` (e.g., `iostreams.PanelStyle`) and `internal/text` (e.g., `text.Truncate`). The `import_boundary_test.go` enforces this.
- Use qualified style references: `iostreams.HeaderStyle`, `iostreams.PanelStyle`, `iostreams.ListItemSelectedStyle`
- Use qualified color constants: `iostreams.ColorPrimary`, `iostreams.ColorSuccess`, `iostreams.ColorError`
- Use type inference (`:=`) for local style variables: `style := iostreams.PanelStyle` (avoids naming lipgloss.Style)
- Use `func(string) string` instead of `lipgloss.Style` in function signatures and struct fields
- Follow BubbleTea `Init()`/`Update()`/`View()` pattern for interactive components
- Use `tui.IsQuit()`, `tui.IsUp()`, `tui.IsDown()`, `tui.IsEnter()`, `tui.IsLeft()`, `tui.IsRight()`, `tui.IsEscape()`, `tui.IsHelp()`, `tui.IsTab()` for key handling (full set in `keys.go`)
- Use layout helpers: `iostreams.Stack()`, `iostreams.Row()`, `iostreams.FlexRow()`
- Use `tui.RunProgram(ios, model)` to run BubbleTea programs with IOStreams
- See `internal/tui/CLAUDE.md` for full API reference
- **Composition principle**: TUI provides generic reusable components — it does NOT contain consumer-specific logic. If you need a special view that doesn't exist, create a generic one in tui that can be customized or expanded upon in the command layer package you need it in. Importing bubbletea types (tea.Model, tea.Cmd, etc.) for type references is acceptable in any package.

## Pure Render Functions (`components.go`)

Stateless rendering helpers that compose `iostreams` styles into reusable visual elements. Use these instead of hand-rolling styled output in command code:

| Function | Purpose |
|----------|---------|
| `RenderHeader(HeaderConfig)` | Title + optional subtitle + optional right-aligned timestamp |
| `RenderDashHeader(cs, DashHeaderConfig)` | Full-width dash-separated header bar (title + subtitle) |
| `RenderStatus(StatusConfig)` | Status indicator with colored symbol (uses `iostreams.StatusIndicator`) |
| `RenderBadge(text, ...render)` | Styled badge (default: `iostreams.BadgeStyle`) |
| `RenderCountBadge(count, label)` | Count + muted label (e.g., "3 containers") |
| `RenderProgress(ProgressConfig)` | Progress bar or fraction (supports bar mode and text mode) |
| `RenderDivider(width)` | Horizontal rule |
| `RenderLabeledDivider(label, width)` | Horizontal rule with centered label |
| `RenderLeftLabeledDivider(label, width)` | Horizontal rule with left-aligned label |
| `RenderEmptyState(message, width, height)` | Centered empty-state message |
| `RenderError(err, width)` | Error message with word wrapping |
| `RenderLabelValue(label, value)` | Single label: value pair |
| `RenderKeyValueTable(pairs, width)` | Aligned key-value table with colon separators |
| `RenderTable(TableConfig)` | Full table with headers, divider, and rows |
| `RenderPercentage(value)` | Color-coded percentage (muted < 60%, warning 60-80%, error >= 80%) |
| `RenderBytes(bytes)` | Human-readable byte sizes (B, KB, MB, GB) |
| `RenderTag(text, ...render)` | Single tag (default: `iostreams.TagStyle`) |
| `RenderTags(tags, ...render)` | Space-separated list of tags |

## Dashboard Framework (`dashboard.go`)

Generic event-driven dashboard for live-display commands. Consumer provides a `DashboardRenderer` interface:

```go
type DashboardRenderer interface {
    ProcessEvent(ev any)
    View(cs *iostreams.ColorScheme, width int) string
}
```

Run with `tui.RunDashboard(ios, renderer, cfg, eventCh)`. Returns `DashboardResult` with `Err`, `Detached` (q/Esc), or `Interrupted` (Ctrl+C) fields.

## Status Bar (`statusbar.go`)

`StatusBarModel` with left/center/right sections and pre-built indicator functions:

- `ModeIndicator(label)`, `ConnectionIndicator(connected)`, `TimerIndicator(elapsed)`, `CounterIndicator(current, total)`
- Render helpers: `RenderStatusBar(cs, left, center, right, width)`, `RenderStatusBarWithSections(cs, sections, width)`

## Help Bar (`help.go`)

`HelpModel` for key binding display with configurable layout:

- `RenderHelpBar(bindings, width)` — single-line help
- `RenderHelpGrid(bindings, width)` — multi-column grid
- Pre-built binding sets: `NavigationBindings()`, `QuitBindings()`, `AllBindings()`, `HelpBinding()`, `QuickHelp()`
