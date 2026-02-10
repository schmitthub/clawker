# TablePrinter Migration — lipgloss/table Switch

## Branch & Status
**Branch**: `a/presentation-layer-tables`
**Status**: ARCHIVAL REFERENCE — retained until presentation layer rollout is complete. lipgloss/table switch complete, all tests pass.

## End Goal
Replace `bubbles/table` (interactive TUI table, overkill for static output) with `lipgloss/table` (purpose-built for static rendering with per-cell `StyleFunc`). Apply style refinements: muted uppercase headers, primary color first column.

## What Changed (This Session)

### Architecture Change: Styled Rendering Moved to `iostreams`
- **New**: `internal/iostreams/table.go` — `(*IOStreams).RenderStyledTable(headers, rows) string`
  - Uses `lipgloss/table` with `StyleFunc` for per-cell styling
  - `table.HeaderRow` → muted uppercase (`TableHeaderStyle`)
  - `col == 0` → primary color (`TablePrimaryColumnStyle` using `ColorPrimary` = `ColorBurntOrange` `#E8714A`)
  - Default cells → plain with `Padding(0, 1)`
  - All 7 borders disabled, `.Width(termWidth)` for auto column sizing
  - Column widths handled by `lipgloss/table`'s built-in median-based resizer (replaced our manual proportional `calculateWidths`)

### Style Token Updates: `iostreams/styles.go`
- `TableHeaderStyle`: Changed from `Bold(true).Foreground(ColorPrimary)` → `Foreground(ColorMuted)` (muted, no bold)
- **New** `TablePrimaryColumnStyle`: `Foreground(ColorPrimary)` (= `ColorBurntOrange`, warm orange)

### Simplified: `tui/table.go`
- Removed: `bubbles/table` import, `go-runewidth` import, `visibleWidth()`, `calculateWidths()`, `cellPadding`, `minColWidth` constants
- `renderStyled()` now just delegates: `tp.tui.ios.RenderStyledTable(tp.headers, tp.rows)`

### Import Boundary: `tui/import_boundary_test.go`
- Now checks for both `lipgloss` AND `lipgloss/table` in non-test files

### Tests
| File | Change |
|------|--------|
| `internal/iostreams/table_test.go` | **New** — 5 tests: basic, uppercase headers, empty, no borders, fits terminal width |
| `internal/tui/table_test.go` | Removed 4 `calculateWidths` tests (responsibility moved to `lipgloss/table` resizer), kept 9 TablePrinter integration tests |
| `internal/tui/table_golden_test.go` | Unchanged structure, regenerated styled goldens |
| `internal/tui/import_boundary_test.go` | Added `lipgloss/table` check |

### Golden Files Regenerated
- `testdata/TestTableStyled_Golden_basic/basic.golden` — muted headers (ANSI 90), orange first col (ANSI 91)
- `testdata/TestTableStyled_Golden_image_list/image_list.golden` — same
- `testdata/TestTableStyled_Golden_narrow/narrow.golden` — wrapping works correctly
- Plain goldens unchanged (non-TTY mode unaffected)
- `image/list` goldens unchanged (those tests use plain mode)

### go.mod
- `muesli/termenv` promoted from indirect to direct (test imports `termenv.ANSI` for `forceColorProfile`)
- `bubbles/table` no longer directly imported anywhere (still indirect via bubbles package)

### Documentation Updated
- `internal/tui/CLAUDE.md` — Import boundary description, file overview table, TablePrinter section
- `internal/iostreams/CLAUDE.md` — Table output section, color palette table styles, import boundary
- `.serena/memories/cli-output-style-guide` — Rendering modes, section 7
- `.serena/memories/project-overview` — Presentation line

## Verification (All Passing)
```bash
make test                    # 3262 tests, 0 failures
go build ./...               # Clean
make fawker && ./bin/fawker image ls   # Visual UAT
```

## Key Design Decisions
1. **`lipgloss/table` for styled mode** — subpackage of `lipgloss` v1.1.0 (already in go.mod). Purpose-built for static table rendering with `StyleFunc` for per-cell styling.
2. **Architecture: styled rendering in `iostreams`** — `tui/table.go` delegates to `iostreams.RenderStyledTable()`. This keeps `lipgloss/table` inside the `iostreams` import boundary, enforced by `import_boundary_test.go`.
3. **Content-aware column widths** — `lipgloss/table`'s built-in median-based resizer handles column sizing automatically when `.Width(termWidth)` is set. No manual width calculation needed (simpler than plan's approach of porting `calculateWidths`).
4. **Primary = BurntOrange** — Two-layer color system: `ColorPrimary` now maps to `ColorBurntOrange` (`#E8714A`). `TablePrimaryColumnStyle` uses `ColorPrimary`.

## Remaining TODOs
- [x] Update style tokens in `iostreams/styles.go`
- [x] Create `iostreams/table.go` with `RenderStyledTable`
- [x] Simplify `tui/table.go` to delegate styled rendering
- [x] Create `iostreams/table_test.go`
- [x] Update `tui/table_test.go` and `import_boundary_test.go`
- [x] Regenerate golden files
- [x] Update documentation and memories
- [x] Fix brand color to orange (user feedback)
- [ ] **Commit and PR** — All code changes are unstaged. Ready to commit.
- [ ] **Visual UAT in real TTY** — `fawker image ls` in a real terminal to see true color rendering (test suite uses ANSI fallback)

## Format/Filter Flags (Complete, same branch)

Reusable `--format`/`--json`/`--quiet`/`--filter` flag system added in `cmdutil/`:
- `format.go` + `json.go` + `filter.go` + `template.go` (each with tests)
- `image list` migrated: uses `cmdutil.FormatFlags` and `cmdutil.FilterFlags`
- New rendering tests for JSON, template, filter, and mutual exclusivity
- All tests pass

### PR Review Fixes Applied
- `FormatFlags` convenience delegates: `IsJSON()`, `IsTemplate()`, `IsDefault()`, `IsTableTemplate()`, `Template()` — eliminates `opts.Format.Format.IsJSON()` stutter
- `cmdutil.ToAny[T any]` generic — replaces per-command `toAny` helpers
- `json` template func returns `(string, error)` — errors propagate through `template.Execute()`
- `title` template func is unicode-safe — uses `utf8.DecodeRuneInString`
- `truncate` template func handles negative n — returns empty string
- `ExecuteTemplate` checks `fmt.Fprintln` error return
- `WriteJSON` sets `SetEscapeHTML(false)` — `<none>:<none>` no longer becomes `\u003cnone\u003e`
- `ImageSummary` and `ImageListResult` re-exported through `internal/docker/types.go`
- `image list` no longer imports `pkg/whail` directly

## Remaining Raw Tabwriter (7 files, out of scope)
These commands still use raw `tabwriter.NewWriter` — each is a separate migration PR:
- `container/list`, `container/top`, `container/stats` (×2)
- `volume/list`, `network/list`, `worktree/list`

## Plan File Reference
Original plan transcript: `/Users/andrew/.claude/projects/-Users-andrew-Code-clawker/708c6bfd-d4e3-4ed8-b0de-beff8ac67971.jsonl`

## IMPORTANT
Always check with the user before proceeding with any remaining todo item. If all work is done, ask the user if they want to delete this memory.