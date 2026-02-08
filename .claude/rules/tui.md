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
- Use `tui.IsQuit()`, `tui.IsUp()`, `tui.IsDown()`, `tui.IsEnter()` for key handling
- Use layout helpers: `iostreams.Stack()`, `iostreams.Row()`, `iostreams.FlexRow()`
- Use `tui.RunProgram(ios, model)` to run BubbleTea programs with IOStreams
- See `internal/tui/CLAUDE.md` for full API reference
