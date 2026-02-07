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

## Remaining Work

### Output Interface (experimental, not started)
- `Output` interface — `HandleError(err)`, `PrintWarning(format, args...)`, `PrintSuccess(format, args...)`, `PrintNextSteps(steps...)`
- Replaces `cmdutil.Print*` free functions scattered across `cmdutil/output.go`
- Handles scenario 1 (non-interactive/static) in the 4-scenario model
- Design questions: wrap `IOStreams` or live alongside? Factory field or per-command?

### TTY Visual Bugs (Fixed & Remaining)
- **REPLACED: Sliding window with tree-based display** — The entire sliding window system (`visibleProgressSteps`, `collapseCompleteGroups`, `mergeCollapsed`, `renderStepSection`, `renderProgressViewport`) has been replaced with a tree-based stage display (`buildStageTree`, `renderTreeSection`, `renderStageNode`, `renderStageChildren`). Stages are parent nodes with tree-connected child steps. Active stages expand with `├─`/`└─` connectors and inline `⎿` log lines under running steps. Complete/pending/error stages collapse to single lines. Per-stage child window centers on running step. All previous sliding window bugs (duplicate collapsed headings, active group falsely collapsed, running step hidden, viewport border collapse from `\r`) are eliminated by the new design.
- **KEPT: High-water mark frame padding** — Prevents BubbleTea inline renderer cursor drift when stages collapse.
- **KEPT: `\r` stripping in `drainProgress()`** — Independent fix in `pkg/whail/buildkit/progress.go`.
- Summary/statusline sometimes duplicates (not blocking)
- Root causes for remaining: width floor, ANSI width miscounting

### Phase 7: Generic Completion Verb
Added `CompletionVerb` field to `ProgressDisplayConfig` (default: "Completed"). Build command sets `"Built"`. Success summary now uses `cfg.completionVerb()` instead of hardcoded `"Built %s"`. All tests pass without golden file regeneration.

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