---
description: TUI component guidelines
paths: ["internal/tui/**"]
---

# TUI Rules

- Use `tui.ColorPrimary`, `tui.ColorSuccess`, `tui.ColorError`, etc. for consistent colors
- Use component styles: `tui.HeaderStyle`, `tui.PanelStyle`, `tui.ListItemSelectedStyle`
- Follow BubbleTea `Init()`/`Update()`/`View()` pattern for interactive components
- Use `tui.IsQuit()`, `tui.IsUp()`, `tui.IsDown()`, `tui.IsEnter()` for key handling
- Use layout helpers: `tui.SplitHorizontal()`, `tui.Stack()`, `tui.Row()`
- Use `tui.GetLayoutMode(width)` for responsive designs
- See `internal/tui/CLAUDE.md` for full API reference
