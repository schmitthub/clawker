# Build Progress Display Architecture

## Overview
The `clawker image build` command uses a generic TUI progress display. Domain logic lives in
`pkg/whail/progress.go`, the generic display component in `internal/tui/progress.go`, and the
composition root in `internal/cmd/image/build/build.go`. TTY mode uses BubbleTea for proper
terminal management. Plain mode uses sequential text output. Both consume events from a channel.

## Threading Model
```
build.go → goroutine: builder.Build → OnProgress callback → channel → tui.RunProgress
                                                                       ├─ TTY: BubbleTea model
                                                                       └─ Plain: sequential lines
```

The build runs in a goroutine. Events flow through a `chan tui.ProgressStep` to the display.
Channel closure signals completion — no Done/BuildErr fields on events.
Build error captured in goroutine local variable, checked after RunProgress returns.
A `BuildProgressFunc` callback defined in `pkg/whail/types.go` threads through the options chain.

## Package DAG
```
pkg/whail/progress.go      — domain helpers (IsInternalStep, CleanStepName, ParseBuildStage, FormatBuildDuration)
pkg/whail/types.go          — BuildProgressEvent, BuildStepStatus, BuildProgressFunc
pkg/whail/buildkit/         — produces events from BuildKit SolveStatus channel
internal/docker/client.go   — produces events from legacy JSON stream
internal/tui/progress.go    — generic display component (zero build knowledge)
internal/cmd/image/build/   — composition root: bridges domain→view via explicit status conversion
```

## Key Types
- `whail.BuildStepStatus` (int enum): Pending, Running, Complete, Cached, Error
- `tui.ProgressStepStatus` (int enum): StepPending, StepRunning, StepComplete, StepCached, StepError
- `tui.ProgressStep`: channel event (ID, Name, Status, Cached, Error, LogLine)
- `tui.ProgressDisplayConfig`: callbacks for domain behavior (IsInternal, CleanName, ParseGroup, FormatDuration)
- `tui.ProgressResult`: returned by RunProgress (Err)

## Status Conversion
Explicit switch in build command — no iota alignment tricks between packages:
```go
func progressStatus(s whail.BuildStepStatus) tui.ProgressStepStatus
```

## Rendering Modes
- **TTY**: BubbleTea model, pulsing ●/○ spinner (BrandOrange), configurable sliding window (MaxVisible),
  group headings via ParseGroup callback, bordered log viewport (LogLines), Ctrl+C handling
- **Plain**: Sequential `[run]`/`[ok]`/`[fail]` lines, internal steps hidden via IsInternal callback, dedup
- **Quiet/None**: No display; `SuppressOutput` flag, build runs synchronously

## Colors
- Active spinner: `BrandOrangeStyle` (#E8714A) — warm orange
- Complete: `ColorSuccess` (#04B575) — green check
- Pending: `ColorMuted` (#626262) — gray circle
- Error: `ColorError` (#FF5F87) — coral X
- Panel border: `ColorBorder` (#3C3C3C)
- Durations: Muted
- Header: Bold + BrandOrange

## Testing
- `pkg/whail/whailtest/build_scenarios.go` — pre-built event sequences: Simple, Cached, MultiStage, Error,
  LargeLogOutput, ManySteps, InternalOnly. `AllBuildScenarios()` returns all.
- `pkg/whail/whailtest/recorded_scenario.go` — JSON-serializable events with timing: `RecordedBuildEvent`,
  `RecordedBuildScenario`, `EventRecorder`. Load/save: `LoadRecordedScenario`, `LoadRecordedScenarioFromBytes`, `SaveRecordedScenario`
- `pkg/whail/whailtest/testdata/*.json` — 7 recorded JSON scenarios with synthetic timing
- `whailtest.FakeBuildKitBuilder` emits `BuildKitCapture.ProgressEvents` via OnProgress callback
- `whailtest.FakeTimedBuildKitBuilder` — same but sleeps recorded delays between events
- `dockertest.SetupBuildKitWithProgress(events)` — wires fake BuildKit with progress event emission
- `dockertest.SetupBuildKitWithRecordedProgress(events)` — wires timed replay from recorded scenarios
- `dockertest.SetupPingBuildKit()` — wires PingFn for BuildKit detection (used by fawker)
- `internal/cmd/image/build/build_progress_test.go` — full pipeline test exercising all scenarios
- `internal/cmd/image/build/build_progress_golden_test.go` — golden snapshot tests for command output
- `internal/tui/progress_test.go` — unit tests for rendering, model, plain mode, summary
- `internal/tui/progress_golden_test.go` — golden snapshot tests for plain mode output