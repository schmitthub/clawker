# Output Styling PR — Follow-Up Issues

**Branch:** `a/output-styling`
**Created:** 2026-02-06 after PR review critical fixes
**Status:** ALL ITEMS FIXED. #1-8 fixed in first session, #9-14 fixed in second session.

---

## Important (should fix soon)

### Code Quality

~~**1. SpinnerFrame comments are misleading** — FIXED in same session (CLAUDE.md, spinner.go updated)~~

~~**2. ProgressBar has no circuit-breaker for write failures** — FIXED in same session (writeErr field + guard added)~~

**3. Deprecated wrappers missing standard Go annotation**
- File: `internal/iostreams/iostreams.go:272-305`
- `StartProgressIndicator`, `StartProgressIndicatorWithLabel`, `StopProgressIndicator`, `RunWithProgress` use plain comments. Need standard `// Deprecated:` with blank separator line for gopls recognition.

### Documentation

**4. CLAUDE.md TablePrinter.Render() error return missing**
- File: `internal/iostreams/CLAUDE.md:~219`
- Shows `tp.Render()` without the `error` return. Update to `tp.Render() error`.

**5. Memory files use wrong package name**
- Files: `.serena/memories/output-styling-initiative.md`, `PRESENTATION-LAYER-DESIGN.md`
- References to `internal/iostream` (singular) should be `internal/iostreams` (plural).

---

## Test Coverage Gaps (should add)

**6. Animated spinner concurrent test missing**
- File: `internal/iostreams/spinner_test.go`
- `TestSpinner_ConcurrentAccess` only tests text fallback mode. Add concurrent Start/Stop/SetLabel test in animated mode to exercise `spinnerMu`, `stopOnce`, and `done` channel.

**7. Set/Increment after Finish not tested**
- File: `internal/iostreams/progress_test.go`
- No test verifying that `Set()` or `Increment()` after `Finish()` produces no output.

**8. Import boundary reverse direction not tested**
- File: `internal/tui/import_boundary_test.go`
- Only checks tui doesn't import lipgloss. Add companion test in iostreams verifying it doesn't import bubbletea/bubbles.

---

## Simplification Opportunities (nice to have)

~~**9. Replace `renderDecoration` closures with pre-defined styles (~25 lines)** — FIXED: package-level `boldStyle`/`italicStyle`/`underlineStyle`/`dimStyle` vars; `renderDecoration` removed~~

~~**10. Replace bar-building loop with `strings.Repeat` (~6 lines)** — FIXED: 7-line loop replaced with `strings.Repeat("=", filled) + strings.Repeat("-", barWidth-filled)`~~

~~**11. Extract common early-return guard in ProgressBar (~4 lines)** — FIXED: `canUpdate() bool` helper extracts duplicate guards in `Set()` and `Increment()`~~

---

## Silent Failure Audit Notes

~~**12. render.go void signatures discard stdout write errors** — FIXED: all 9 Render methods now return `error`~~

~~**13. message.go void signatures discard stderr write errors** — FIXED: all 5 Print methods now return `error`~~

~~**14. testBuffer is not thread-safe** — FIXED: `sync.Mutex` added to `testBuffer` struct; all 5 methods (`Read`, `Write`, `String`, `Reset`, `SetInput`) now lock/unlock~~
