# Text Package

Pure text/string utility functions. Leaf package with zero internal imports.

## Import Boundary

- **Allowed imports**: stdlib only
- **FORBIDDEN imports**: Any `internal/*` package

## Functions

All ANSI-aware functions count visible characters only, excluding escape sequences.

| Function | Purpose |
|----------|---------|
| `Truncate(s, width)` | Shorten to width visible chars, append "..." |
| `TruncateMiddle(s, width)` | Remove middle chars: "/Us.../path" |
| `PadRight(s, width)` | Right-pad to width |
| `PadLeft(s, width)` | Left-pad to width |
| `PadCenter(s, width)` | Center within width |
| `WordWrap(s, width)` | Wrap on word boundaries |
| `WrapLines(s, width)` | Wrap and return []string |
| `CountVisibleWidth(s)` | Visible char count (strips ANSI) |
| `StripANSI(s)` | Remove ANSI escape sequences |
| `Indent(s, spaces)` | Prefix non-empty lines with spaces |
| `JoinNonEmpty(sep, parts...)` | Join non-empty strings |
| `Repeat(s, n)` | Repeat string n times |
| `FirstLine(s)` | First line of multi-line string |
| `LineCount(s)` | Count lines in string |
| `Slugify(s)` | Normalize text to lowercase dash-separated slug |

## Limitations

- `CountVisibleWidth` counts runes, not true visual width (CJK chars counted as 1 column)
- `StripANSI` may not handle all nested ANSI sequences
- When truncation occurs on ANSI-containing strings, ANSI codes are stripped from the result
