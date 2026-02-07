# Presentation Integration

## Branch & Status
**Branch**: `a/presentation-integration` (was `a/image-build-output`)
**Commit**: `157a7bf` — feat(tui): generic progress display, TUI Factory noun, lifecycle hooks, fawker demo CLI
**Status**: All implementation complete. 3399 unit tests pass. Both clawker and fawker binaries compile.

## Architecture

### Threading Model
```
build.go → goroutine: builder.Build → OnProgress callback → channel → tui.RunProgress
                                                                       ├─ TTY: BubbleTea model
                                                                       └─ Plain: sequential lines
```
Build runs in a goroutine. Events flow through `chan tui.ProgressStep`. Channel closure signals completion. Build error captured in goroutine local variable, checked after RunProgress returns.

### Package DAG
```
pkg/whail/progress.go      — domain helpers (IsInternalStep, CleanStepName, ParseBuildStage, FormatBuildDuration)
pkg/whail/types.go          — BuildProgressEvent, BuildStepStatus, BuildProgressFunc
pkg/whail/buildkit/         — produces events from BuildKit SolveStatus channel
internal/docker/client.go   — produces events from legacy JSON stream
internal/tui/progress.go    — generic display component (zero build knowledge)
internal/tui/tui.go         — TUI Factory noun: NewTUI, RegisterHooks, RunProgress, composedHook
internal/cmd/image/build/   — composition root: bridges domain→view via explicit status conversion
```

### Key Types
- `whail.BuildStepStatus` (int enum): Pending, Running, Complete, Cached, Error
- `tui.ProgressStepStatus` (int enum): StepPending, StepRunning, StepComplete, StepCached, StepError
- `tui.ProgressStep`: channel event (ID, Name, Status, Cached, Error, LogLine)
- `tui.ProgressDisplayConfig`: callbacks (IsInternal, CleanName, ParseGroup, FormatDuration, OnLifecycle)
- `tui.LifecycleHook`: generic hook function type for TUI lifecycle events
- `tui.HookResult`: Continue (bool), Message (string), Err (error)
- `tui.TUI`: Factory noun — pointer sharing fixes eager capture bugs
- Explicit `progressStatus()` switch in build command — no iota alignment tricks

## Completed Work

### Phase 1: TUI Migration
Replaced raw ANSI cursor manipulation in `iostreams/buildprogress.go` with BubbleTea model in `internal/tui/`.

### Phase 2: Architecture Cleanup (7 parts)
1. Domain helpers → `pkg/whail/progress.go`
2. Generic progress component → `internal/tui/progress.go` (replaced `buildprogress.go`)
3. Build command as composition root with explicit status conversion
4. whailtest fake enhancements: `FakeBuildKitBuilder` emits events, 7 pre-built scenarios
5. dockertest wiring: `SetupBuildKitWithProgress(events)`
6. Build command pipeline test (table-driven over all 7 scenarios)
7. Documentation updates across CLAUDE.md files

### Phase 3: Golden Files + Demo CLI (7 parts)
1. `RecordedBuildEvent` type + JSON serialization in `whailtest/recorded_scenario.go`
2. `EventRecorder` wrapper for wall-clock timing capture
3. `FakeTimedBuildKitBuilder` + `SetupBuildKitWithRecordedProgress` + `SetupPingBuildKit`
4. 7 JSON testdata files in `pkg/whail/whailtest/testdata/` with synthetic timing
5. TUI golden output tests in `internal/tui/progress_golden_test.go`
6. Command golden output tests in `internal/cmd/image/build/build_progress_golden_test.go`
7. Fawker demo CLI: `cmd/fawker/` with embedded JSON scenarios, `make fawker`

### Phase 4: TUI Factory Noun + Lifecycle Hooks
1. `internal/tui/hooks.go` — `HookResult` + `LifecycleHook` function type (4 tests)
2. Fawker `--step` flag for UAT step-through
3. `TUI` struct as Factory noun: `NewTUI`, `RegisterHooks`, `RunProgress`, `composedHook`
4. Factory wiring: `f.TUI` replaces `TUILifecycleHook`, pointer sharing
5. Design rules: 4-scenario output model, zerolog file-only rule

## Design Decisions & Lessons
- **TUI is a Factory noun** (`*tui.TUI`) — pointer sharing fixes `--step` TTY bug where eager callback capture caused silent failures
- **4-scenario output model**: static | static-interactive | live-display | live-interactive
- **zerolog for file logging only** — user output via `fmt.Fprintf` to IOStreams
- **Hooks fire AFTER BubbleTea exits** (no stdin conflict), BEFORE summary render
- **`viewFinished()` must render ALL visual elements** (header + steps + viewport) for BubbleTea final frame persistence — returning `""` erases everything
- **`ProgressConfig` name clash** with `components.go` — renamed to `ProgressDisplayConfig`
- **Plain mode does NOT render log lines** — only status transitions (`[run]`/`[ok]`/`[fail]`)
- **`renderProgressSummary`** detects errors from step statuses (not explicit parameter)
- **Channel closure = done signal** — no Done/BuildErr fields on events

## Remaining Work

### Output Interface (Step 3 from presentation-layer-refactor)
Experimental design for abstracting simple output:
- `Output` interface — `HandleError(err)`, `PrintWarning(format, args...)`, `PrintSuccess(format, args...)`, `PrintNextSteps(steps...)`
- Replaces `cmdutil.Print*` free functions scattered across `cmdutil/output.go`
- Handles scenario 1 (non-interactive/static) in the 4-scenario model
- Design questions: wrap `IOStreams` or live alongside? Factory field or per-command? Interaction with `HandleError` duck-typing?

### TTY Visual Bugs (noted, not blocking)
- Invisible lines printed in TTY mode
- Viewport lower-right border collapses in/out
- Summary/statusline sometimes duplicates
- Root causes likely: View() height instability, width floor, ANSI width miscounting

## Testing

### Test Infrastructure
- `whailtest.FakeBuildKitBuilder` — emits `BuildKitCapture.ProgressEvents` via OnProgress
- `whailtest.FakeTimedBuildKitBuilder` — sleeps recorded delays between events
- `dockertest.SetupBuildKitWithProgress(events)` — wires fake BuildKit with progress emission
- `dockertest.SetupBuildKitWithRecordedProgress(events)` — wires timed replay
- `dockertest.SetupPingBuildKit()` — wires PingFn for BuildKit detection (used by fawker)

### Golden File Commands
```bash
GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeed -v          # JSON testdata
GOLDEN_UPDATE=1 go test ./internal/tui/... -run TestProgressPlain_Golden -v # TUI golden files
GOLDEN_UPDATE=1 go test ./internal/cmd/image/build/... -run TestBuildProgress_Golden -v  # Command golden files
```

### 7 Build Scenarios
Simple, Cached, MultiStage, Error, LargeLogOutput, ManySteps, InternalOnly — available via `whailtest.AllBuildScenarios()`.
