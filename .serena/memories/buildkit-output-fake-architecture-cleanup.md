# BuildKit Output Fake + Architecture Cleanup

## End Goal
Decompose monolithic `tui/buildprogress.go` following the package DAG, then build a proper BuildKit output fake for pipeline testing.

## Branch
`a/image-build-output`

## Plan File
`.claude/plans/snug-enchanting-prism.md` — original 7-part implementation plan

## Background Context
The build progress display was a single monolithic file (`tui/buildprogress.go`) that coupled TUI rendering to Docker build concerns. The refactor follows the DAG: domain logic in `whail/`, generic progress component in `tui/`, composition in the build command.

## Implementation Progress

### [x] Part 1 — Domain helpers → `pkg/whail/progress.go`
- Moved `IsInternalStep`, `CleanStepName`, `ParseBuildStage` (exported from `parseStage`), `FormatBuildDuration`
- `CleanStepName` signature simplified: removed `width` param (presentation concern)
- Created `pkg/whail/progress_test.go`

### [x] Part 2 — Generic progress component → `internal/tui/progress.go`
- Replaced `buildprogress.go` entirely with domain-agnostic progress display
- Types: `ProgressStepStatus`, `ProgressStep`, `ProgressDisplayConfig`, `ProgressResult`
- Named `ProgressDisplayConfig` (not `ProgressConfig`) to avoid clash with existing `ProgressConfig` in `components.go`
- Callbacks: `IsInternal`, `CleanName`, `ParseGroup`, `FormatDuration`
- Channel closure = done signal (no more Done/BuildErr fields)
- `renderProgressSummary` detects errors from step statuses (not explicit parameter)
- Deleted `buildprogress.go` and `buildprogress_test.go`
- Created `internal/tui/progress_test.go`

### [x] Part 3 — Build command composition root
- Added explicit `progressStatus()` conversion function in `build.go`
- Bridges `whail.BuildProgressEvent` → `tui.ProgressStep` via channel
- Build error captured in goroutine local, checked after RunProgress returns

### [x] Part 4 — whailtest fake enhancements + build scenarios
- Added `ProgressEvents []whail.BuildProgressEvent` field to `BuildKitCapture`
- Enhanced `FakeBuildKitBuilder` to emit events via OnProgress callback
- Created `pkg/whail/whailtest/build_scenarios.go` with 7 scenarios:
  Simple, Cached, MultiStage, Error, LargeLogOutput, ManySteps, InternalOnly
- Created `pkg/whail/whailtest/build_scenarios_test.go`

### [x] Part 5 — dockertest wiring
- Added `SetupBuildKitWithProgress(events)` to `dockertest.FakeClient`

### [x] Part 6 — Build command pipeline test
- Created `internal/cmd/image/build/build_progress_test.go`
- Full pipeline: fake events → OnProgress → channel → RunProgress (plain) → assert
- Table-driven over all 7 scenarios + specific content/suppression/capture tests

### [x] Part 7 — Documentation updates
- Updated: `CLAUDE.md`, `tui/CLAUDE.md`, `whail/CLAUDE.md`, `docker/CLAUDE.md`
- Updated Serena memories: `build-progress-display`, `tui-build-progress-display`, `fix-build-progress-display`

## Verification
- **3342 tests pass** (up from 3305 pre-refactor), 3 skipped (expected)
- `make test` passes clean

## Lessons Learned
- `ProgressConfig` name clash with `components.go` — renamed to `ProgressDisplayConfig`
- Plain mode does NOT render log lines — only status transitions (`[run]`/`[ok]`/`[fail]`)
- `replace_all` for pointer vs value references needs separate passes
- `renderProgressSummary` error detection via step statuses is cleaner than explicit parameter
- `t.Setenv("DOCKER_BUILDKIT", "1")` simplest way to bypass Ping check in tests

## Phase 2: Two-Golden-File System + Demo CLI

Plan: `.claude/plans/snuggly-swinging-kahan.md`

### [x] Part 1 — RecordedBuildEvent type + serialization
- `pkg/whail/whailtest/recorded_scenario.go`: RecordedBuildEvent, RecordedBuildScenario, Load/Save, FromEvents

### [x] Part 2 — EventRecorder wrapper
- Same file: EventRecorder wraps BuildProgressFunc to capture wall-clock timing

### [x] Part 3 — FakeTimedBuildKitBuilder
- `whailtest/helpers.go`: FakeTimedBuildKitBuilder, RecordedEvents field on BuildKitCapture
- `dockertest/helpers.go`: SetupBuildKitWithRecordedProgress, SetupPingBuildKit

### [x] Part 4 — Seed JSON testdata
- 7 JSON files in `pkg/whail/whailtest/testdata/` with synthetic timing
- TestSeedRecordedScenarios (GOLDEN_UPDATE=1), TestRecordedScenarios_MatchGoScenarios

### [x] Part 5 — TUI golden output tests
- `internal/tui/progress_golden_test.go` with inline golden helper
- 7 golden files in `internal/tui/testdata/`

### [x] Part 6 — Command golden output tests
- `internal/cmd/image/build/build_progress_golden_test.go` using test/harness
- 7 golden files in `internal/cmd/image/build/testdata/`

### [x] Part 7 — Fawker demo CLI
- `cmd/fawker/` — main.go, root.go, factory.go, scenarios/ (embedded JSON)
- Management commands only (no top-level aliases)
- `make fawker` Makefile target

## Status
**ALL PARTS COMPLETE (Phase 1 + Phase 2).** 3383 tests pass. Fawker demo CLI builds and runs all scenarios.

## Remaining TTY Visual Bugs (out of scope, noted for reference)
- Invisible lines in TTY mode
- Viewport lower-right border collapses in/out
- Summary/statusline sometimes duplicates

---

**IMPERATIVE: Always check with the user before proceeding with the next todo item. Present what you plan to do and get approval. If all work is done, ask the user if they want to delete this memory.**