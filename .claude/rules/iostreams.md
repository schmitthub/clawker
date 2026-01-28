---
description: IOStreams usage guidelines
paths: ["internal/iostreams/**", "internal/cmd/**"]
---

# IOStreams Rules

- All CLI commands access I/O through `f.IOStreams` from Factory — never create IOStreams directly
- Use `ios.ColorScheme()` for color output that respects `NO_COLOR`
- Use `ios.StartProgressIndicatorWithLabel()` for spinners (writes to stderr)
- Check `ios.CanPrompt()` before interactive prompts (respects CI env var)
- Test with `iostreams.NewTestIOStreams()` — colors disabled and non-TTY by default
- See `internal/iostreams/CLAUDE.md` for full API reference
