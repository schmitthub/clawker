---
description: IOStreams usage guidelines
paths: ["internal/iostreams/**", "internal/cmd/**"]
---

# IOStreams Rules

- All CLI commands access I/O through `f.IOStreams` from Factory — never create IOStreams directly
- Use `ios.ColorScheme()` for color output that respects `NO_COLOR`. Semantic methods (`cs.Primary`/`Success`/`Warning`/`Error`/`Info`/`Muted`) are the canonical API — avoid raw color methods (`cs.Red`/`Green`/`Yellow`) in command code
- Icons: `cs.SuccessIcon()`, `cs.WarningIcon()`, `cs.FailureIcon()`, `cs.InfoIcon()` (and `*WithColor(text)` variants) for single-line status output via `fmt.Fprintf`
- Simple spinners: `ios.StartSpinner(label)` / `ios.StartSpinnerWithType(type, label)` / `ios.RunWithSpinner(label, fn)` (writes to stderr)
- Multi-step progress: use `f.TUI.RunProgress(progressMode, cfg, ch)` for tree displays (live-display scenario) — not `iostreams` directly
- Check `ios.CanPrompt()` before interactive prompts (respects CI env var)
- Test with `iostreams.Test()` — returns `(*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer)`, non-TTY, no colors, nil Logger
- Import boundaries: only `iostreams` imports `lipgloss`; only `tui` imports `bubbletea`/`bubbles`
- See `internal/iostreams/CLAUDE.md` for full API reference
