---
description: TUI component guidelines
paths: ["internal/tui/**"]
---

# TUI Rules

- **Import boundary**: `internal/tui/` must NOT import `lipgloss` directly. Use re-exports from `iostreams.go`
- Use `tui.ColorPrimary`, `tui.ColorSuccess`, `tui.ColorError`, etc. for consistent colors
- Use component styles: `tui.HeaderStyle`, `tui.PanelStyle`, `tui.ListItemSelectedStyle`
- Use type inference (`:=`) for local style variables: `style := tui.PanelStyle` (avoids naming lipgloss.Style)
- Use `func(string) string` instead of `lipgloss.Style` in function signatures and struct fields
- Follow BubbleTea `Init()`/`Update()`/`View()` pattern for interactive components
- Use `tui.IsQuit()`, `tui.IsUp()`, `tui.IsDown()`, `tui.IsEnter()` for key handling
- Use layout helpers: `tui.SplitHorizontal()`, `tui.Stack()`, `tui.Row()`
- Use `tui.GetLayoutMode(width)` for responsive designs
- Use `tui.RunProgram(ios, model)` to run BubbleTea programs with IOStreams
- See `internal/tui/CLAUDE.md` for full API reference
