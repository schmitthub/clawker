# Presentation Integration

## Branch & Status
**Branch**: `a/presentation-integration`
**Latest Commit**: `043f4af` — refactor(term): split into term, docker/pty, signals packages
**Status**: All phases complete. 3202 unit tests pass (count varies with test refactoring). Both clawker and fawker binaries compile.

## Completed Work (All Done)

### Phase 1: TUI Migration
Replaced raw ANSI cursor manipulation in `iostreams/buildprogress.go` with BubbleTea model in `internal/tui/`.

### Phase 2: Architecture Cleanup
Domain helpers → `pkg/whail/progress.go`. Generic progress → `internal/tui/progress.go`. Build command as composition root. whailtest fakes + dockertest wiring. Pipeline tests. Docs.

### Phase 3: Golden Files + Demo CLI
RecordedBuildEvent + EventRecorder + FakeTimedBuildKitBuilder. 7 JSON testdata files. TUI + command golden tests. Fawker demo CLI (`cmd/fawker/`, `make fawker`).

### Phase 4: TUI Factory Noun + Lifecycle Hooks
`tui.TUI` as Factory noun with `RegisterHooks`/`RunProgress`/`composedHook`. Fawker `--step` flag. 4-scenario output model.

### Phase 5: Term Package Refactor
Split `internal/term` (was middle-tier due to docker import) into three packages:
- `internal/term` — **leaf** (stdlib + x/term only). Sole `golang.org/x/term` gateway. Capability detection + raw mode + `GetTerminalSize`.
- `internal/signals` — **leaf** (stdlib only). `SetupSignalContext` + `ResizeHandler`. No logging.
- `internal/docker/pty.go` — `PTYHandler` moved here (Docker session hijacking). Imports term + signals.
- `internal/iostreams` — no longer imports `x/term` directly, uses `internal/term` gateway.
- Dead code removed: `SignalHandler`, `WaitForSignal` (zero consumers).
- 6 consumer commands updated to new import paths.

## PR Review Fixes (Complete)

All 24 review issues + 7 test coverage gaps addressed:

### Critical (Phase 1)
- **#1 Channel panic + #2 Data race** — `build.go`: replaced shared `var buildErr` with `buildErrCh` channel; `done` channel guards OnProgress sends
- **#15 Redundant var** — removed `var buildkitEnabled bool`
- **#6 BuildKit warning** — user-visible warning when detection fails
- **#14 Silent label drop** — `parseKeyValuePairs` returns invalid entries; caller warns

### High Priority (Phase 2)
- **#4 Double-close panic** — `signals.go`: `sync.Once` on Stop()
- **#23 Wrong lint directive** — `//nolint:errcheck` → `//nolint:revive`
- **#12 Godoc param trap** — added comment about height/width swap
- **#5 Hook abort silent success** — extracted `handleHookResult()` helper; default error on empty abort
- **#10 Duplicated hook handling** — both TTY and plain paths use shared helper
- **#3 Lipgloss boundary** — added `TableHeaderStyle`/`RenderFixedWidth()` to iostreams; removed lipgloss import from tableprinter
- **#7 Escape injection** — ANSI stripping in `buildkit/progress.go` log lines
- **#20 Log level** — `log.Error()` → `log.Warn()` for vertex errors
- **#16 Stale comment** — "sliding window" → "per-stage child window" in build_scenarios

### Medium Priority (Phase 3)
- **#8 Dead spinnerView parameter** — removed from all 10 render functions + tests
- **#9 viewFinished duplicate** — deleted method, View() handles both states
- **#11 Duplicated step line layout** — extracted `renderStepLineWithPrefix()` shared helper
- **#13 Inconsistent receiver** — `maxVisible()` now pointer receiver

### Low Priority (Phase 4)
- **#17 NewTUI nil guard** — panic on nil IOStreams
- **#18 FlagErrorWrap nil** — returns nil for nil input
- **#19 Fawker error path** — prints actual error before help hint
- **#21 Fawker stdin EOF** — handles io.EOF from Read
- **#22 isClosedConnectionError** — documented fragility of string matching
- **#24 Stale comment** — updated styles.go migration comment

### Test Coverage (Phase 5)
- ExitError: Error(), zero code, errors.As wrapping
- FlagErrorWrap(nil), FlagError usage trigger, SilentError distinction
- RunProgress: plain forced, auto fallback, unknown mode, empty channel, zero-value config
- processEvent: empty ID, unknown ID log, event after completion, cached, error

## Key Design Decisions
- **TUI is a Factory noun** (`*tui.TUI`) — pointer sharing fixes eager capture bugs
- **4-scenario output model**: static | static-interactive | live-display | live-interactive
- **zerolog for file logging only** — user output via `fmt.Fprintf` to IOStreams
- **Hooks fire AFTER BubbleTea exits**, BEFORE summary render
- **`internal/term` is sole `golang.org/x/term` gateway** — enforced in code-style.md
- **Channel closure = done signal** — no Done/BuildErr fields on events

## Testing Quick Reference
```bash
make test                                           # Unit tests
make fawker && ./bin/fawker image build             # Visual UAT
GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeed -v
GOLDEN_UPDATE=1 go test ./internal/tui/... -run TestProgressPlain_Golden -v
GOLDEN_UPDATE=1 go test ./internal/cmd/image/build/... -run TestBuildProgress_Golden -v
```

## IMPORTANT
Always check with the user before proceeding with any remaining todo item. If all work is done, ask the user if they want to delete this memory.