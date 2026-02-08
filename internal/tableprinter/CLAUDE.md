# TablePrinter Package

Renders tabular data to IOStreams. TTY-aware: styled headers with divider in TTY mode, plain tabwriter for piped output.

## API

```go
tp := tableprinter.New(ios, "NAME", "STATUS", "IMAGE")
tp.AddRow("web", "running", "nginx:latest")
tp.AddRow("db", "stopped", "postgres:16")
err := tp.Render()  // writes to ios.Out
```

| Function/Method | Purpose |
|----------------|---------|
| `New(ios, headers...)` | Create table with column headers |
| `AddRow(cols...)` | Add data row (missing columns = empty) |
| `Len()` | Number of data rows |
| `Render()` | Write table to ios.Out |

## Rendering Modes

- **Plain** (non-TTY): `text/tabwriter` with 2-space column gaps
- **Styled** (TTY + color): lipgloss bold headers in ColorPrimary, `─` divider, width-aware column distribution

## Dependencies

- `internal/iostreams` — IOStreams struct, color palette, styles
- `internal/text` — ANSI-aware truncation
- `lipgloss` — styled rendering
