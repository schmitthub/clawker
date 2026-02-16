---
description: IOStreams usage guidelines
paths: ["internal/iostreams/**", "internal/cmd/**"]
---

# IOStreams Rules

- All CLI commands access I/O through `f.IOStreams` from Factory — never create IOStreams directly
- Use `ios.ColorScheme()` for color output that respects `NO_COLOR`
- Simple spinners: `ios.StartSpinner(label)` / `ios.RunWithSpinner(label, fn)` (writes to stderr)
- Multi-step progress: use `f.TUI.RunProgress(ctx, ch, cfg)` for tree displays (live-display scenario)
- Check `ios.CanPrompt()` before interactive prompts (respects CI env var)
- Test with `iostreamstest.New()` — colors disabled and non-TTY by default
- Import boundaries: only `iostreams` imports `lipgloss`; only `tui` imports `bubbletea`/`bubbles`
- See `internal/iostreams/CLAUDE.md` for full API reference
