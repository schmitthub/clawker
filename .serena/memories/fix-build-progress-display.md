# Fix Build Progress Display — Completed

## Summary
Two phases of work on the build progress display:

### Phase 1 — TUI Migration (earlier session)
Replaced raw ANSI cursor manipulation in `iostreams/buildprogress.go` with a BubbleTea model
in `internal/tui/buildprogress.go`. Moved all build progress rendering to the `tui` package.

### Phase 2 — Architecture Cleanup (this session)
Decomposed monolithic `tui/buildprogress.go` following the package DAG:
- Domain helpers → `pkg/whail/progress.go` (IsInternalStep, CleanStepName, ParseBuildStage, FormatBuildDuration)
- Generic progress display → `internal/tui/progress.go` (zero build knowledge, callbacks for domain logic)
- Build command as composition root → bridges domain→view with explicit status conversion
- BuildKit output fake → `whailtest/build_scenarios.go` (7 pre-built event sequences)
- Pipeline test → `build_progress_test.go` (full fake→channel→display→assert)

All 3342 tests pass. See `build-progress-display` memory for detailed architecture.

## Branch
`a/image-build-output`
