# TUI Build Progress Display — Architecture Cleanup Complete

## Branch
`a/image-build-output`

## What Was Done (Architecture Cleanup)

Decomposed monolithic `tui/buildprogress.go` following the package DAG:

### Part 1 — Domain helpers → `pkg/whail/progress.go`
- Moved `IsInternalStep`, `CleanStepName`, `ParseBuildStage`, `FormatBuildDuration`
- `CleanStepName` signature simplified: removed `width` param (presentation concern)
- `parseStage` renamed to `ParseBuildStage` (exported)

### Part 2 — Generic progress component → `internal/tui/progress.go`
- Replaced `buildprogress.go` entirely with domain-agnostic progress display
- Types: `ProgressStepStatus`, `ProgressStep`, `ProgressDisplayConfig`, `ProgressResult`
- Callbacks in config: `IsInternal`, `CleanName`, `ParseGroup`, `FormatDuration`
- Channel closure = done signal (no more Done/BuildErr fields on events)
- BubbleTea model + plain mode rendering, all build knowledge removed

### Part 3 — Composition root → `internal/cmd/image/build/build.go`
- Explicit `progressStatus()` conversion function (no iota alignment tricks)
- Bridges `whail.BuildProgressEvent` → `tui.ProgressStep` via channel
- Build error captured in goroutine local, checked after RunProgress returns

### Part 4 — Build scenarios → `pkg/whail/whailtest/build_scenarios.go`
- Enhanced `BuildKitCapture` with `ProgressEvents` field
- Enhanced `FakeBuildKitBuilder` to emit events via OnProgress callback
- 7 pre-built event sequences: Simple, Cached, MultiStage, Error, LargeLogOutput, ManySteps, InternalOnly

### Part 5 — dockertest wiring
- Added `SetupBuildKitWithProgress(events)` to `dockertest.FakeClient`

### Part 6 — Pipeline test → `build_progress_test.go`
- Full pipeline test: fake events → OnProgress → channel → RunProgress (plain) → assert
- Table-driven over all 7 build scenarios + specific content assertions

### Verification
- 3342 unit tests pass (up from 3305 pre-refactor)
- All documentation updated: CLAUDE.md, tui/CLAUDE.md, whail/CLAUDE.md, docker/CLAUDE.md

## Remaining TTY Visual Bugs
From prior user feedback — not addressed in this cleanup:
- Invisible lines printed in TTY mode
- Viewport lower-right border collapses in/out
- Summary/statusline sometimes duplicates

These are rendering stability issues in the BubbleTea model, not architecture concerns.
Root causes likely: View() height instability, width floor, ANSI width miscounting.
See `internal/tui/progress.go` progressModel for the BubbleTea implementation.
