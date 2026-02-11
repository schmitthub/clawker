# Presentation Integration

## Branch & Status
**Branch**: `a/presentation-integration`
**Latest Commit**: `c070f80` — fix: address Copilot PR review findings + dangerous-cmd warning routing
**Status**: ARCHIVAL REFERENCE — retained until presentation layer rollout is complete. All phases complete.

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

## Prompter Integration → Wizard Migration (Complete)
**Branch**: `a/presentation-int-prompter`

### Phase 1: Factory-wired prompter (done)
Migrated `init` command to use Factory-wired prompter, Config gateway, and TUI progress display.

### Phase 2: Wizard TUI Components + Init Refactoring (done)
Created three reusable TUI component layers and refactored `init` to use them:

**Layer 1 — Field Models** (`internal/tui/fields.go`):
- `SelectField` — arrow-key selection wrapping ListModel; label+description compact view
- `TextField` — text input wrapping `bubbles/textinput` with validation (Required, Validator)
- `ConfirmField` — yes/no toggle with Left/Right/Tab/y/n keys
- `FieldOption` — shared {Label, Description} type
- All use value semantics (setters return copies); each works standalone or in a wizard

**Layer 2 — StepperBar** (`internal/tui/stepper.go`):
- `StepState` (Pending/Active/Complete/Skipped), `Step` struct
- `RenderStepperBar(steps, width)` — pure render: ✓/◉/○ icons, skipped hidden, truncation

**Layer 3 — WizardModel** (`internal/tui/wizard.go`):
- `WizardField`, `WizardFieldKind`, `WizardValues`, `WizardResult` public types
- `wizardModel` (unexported, pointer receivers for map mutation) — composes fields + stepper
- Navigation: Enter advances, Esc goes back, SkipIf predicates in both directions
- `filterQuit` prevents individual fields from quitting the wizard
- `TUI.RunWizard(fields)` — runs wizard via `RunProgram` with alt screen

**Init command refactoring** (`internal/cmd/init/init.go`):
- `Run` dispatches to `runInteractive` (wizard) or `runNonInteractive` (--yes path)
- `runInteractive` uses `TUI.RunWizard(fields)` with 3 wizard fields: build, flavor, confirm
- `performSetup` is the shared core logic (settings save + optional build) — testable without BubbleTea
- `buildWizardFields()` returns wizard field definitions with SkipIf for flavor
- `flavorFieldOptions()` converts `bundler.DefaultFlavorOptions()` to `[]tui.FieldOption`

**Tests**: 37+ new tests across 3 files:
- `fields_test.go` — 20 tests for all field types (navigation, confirm, validation, view, Ctrl+C)
- `stepper_test.go` — 9 tests (rendering, skipped hidden, truncation, edge cases)
- `wizard_test.go` — 8 tests (step navigation, conditional skip, cancel, Ctrl+C, submit values, nav bar, view, window size)
- `init_test.go` — refactored: `performSetup` direct tests, `buildWizardFields` assertions, `flavorFieldOptions` conversion tests

**Documentation**: Updated `internal/tui/CLAUDE.md` (Field Models, StepperBar, WizardModel sections) and `internal/cmd/init/CLAUDE.md` (wizard architecture)

Supporting changes from Phase 1:
- `internal/config/config.go` — `SetSettingsLoader(sl SettingsLoader)` (interface), `SettingsLoader()` returns interface
- `internal/config/settings_loader.go` — `SettingsLoader` interface + `FileSettingsLoader` struct; `NewSettingsLoaderForTest(dir) *FileSettingsLoader`
- `internal/config/configtest/inmemory_settings_loader.go` — `InMemorySettingsLoader` (no filesystem I/O)
- `cmd/fawker/factory.go` — Real prompter, Config with SettingsLoader for temp dir
- `cmd/fawker/root.go` — Added `initcmd.NewCmdInit(f, nil)` to fawker command tree
- `pkg/whail/whailtest/fake_client.go` — Added `CloseFn`/`Close()` to `FakeAPIClient`
- `internal/docker/dockertest/helpers.go` — Added `SetupLegacyBuild`/`SetupLegacyBuildError` helpers

### Phase 3: Wizard Wrapup — Review Fixes, Fawker Panic, Color Refresh (done)

Applied fixes from code-reviewer, test-analyzer, and silent-failure-hunter audits:

**Bug fixes:**
- `filterQuit` — deferred cmd evaluation (was eagerly calling `cmd()` outside BubbleTea runtime)
- `RunWizard` — explicit error on unexpected model type + empty fields validation
- `init.go` — early return after build-failure "Next Steps" (was printing duplicate sections)
- `init.go` — user-visible warning on settings save failure (was file-log only)
- `init.go` — "Setup cancelled." message on wizard cancellation (was silent)
- `fawkerClient()` — `SetupLegacyBuild()` added (was panicking on nil `ImageBuildFn`)
- `fawkerFactory()` — `MkdirTemp` replaced with `InMemorySettingsLoader` + `sync.Once` (no temp dirs, no filesystem leak)
- `FakeAPIClient.Close()` — always records call regardless of `CloseFn` (was inconsistent)

**Color system refactor:**
- Two-layer architecture: Layer 1 (15 Named Colors) + Layer 2 (15 Semantic Theme aliases)
- `ColorPrimary` = `ColorBurntOrange` (#E8714A), `ColorSecondary` = `ColorDeepSkyBlue` (#00BFFF)
- `ColorBrandOrange` removed (merged into Primary), `BrandOrangeStyle` removed
- `BlueStyle` now uses `ColorDeepSkyBlue` (actual blue), `TablePrimaryColumnStyle` uses `ColorPrimary`
- `BrandOrange()/BrandOrangef()` deprecated, delegate to `Primary()/Primaryf()`
- Stepper uses `TitleStyle`, progress uses `cs.Primary()`

**New tests:** `TestWizard_SkipIfReevaluation`, `TestWizard_TextFieldInWizard`, `TestWizard_EmptyFields`

**All unit tests pass. Both binaries compile.**

### Container Init TUI Progress (Complete)
**Branch**: `a/pres-run-create-start`

Extracted ~200 lines of duplicated container initialization code from `run.go` and `create.go` into `shared/init.go` with TUI progress display.

**Key changes:**
- `shared.ContainerInitializer` Factory noun — constructed from `*cmdutil.Factory`, `Run()` performs 5-step progress-tracked init
- Three-phase command structure: Phase A (pre-progress), Phase B (`Initializer.Run()`), Phase C (post-progress)
- `run.go` and `create.go` refactored: removed ~200 lines each, replaced with `Initializer.Run()` call
- Fawker `cmd/fawker/container.go` override removed — uses same code path as production
- 10 new unit tests in `shared/init_test.go`

**Files modified:** `shared/init.go` (new), `run/run.go`, `create/create.go`, `cmd/fawker/container.go`, `cmd/fawker/root.go`, `run/run_test.go`, `create/create_test.go`, `shared/CLAUDE.md`, `container/CLAUDE.md`, CLAUDE.md

## Follow-Up Work

### TablePrinter Migration (Complete)
**Branch**: `a/presentation-layer-tables`
**Memory**: `tableprinter-migration`

Replaced `internal/tableprinter/` with `tui.TablePrinter` backed by `bubbles/table`. Migrated `image/list` as first adopter. 7 raw tabwriter commands remain for subsequent PRs. See `tableprinter-migration` memory for full details.

### Format/Filter Flags (Complete + PR Review Fixes Applied)
**Branch**: `a/presentation-layer-tables`

Reusable `--format`/`--json`/`--quiet`/`--filter` flag system in `cmdutil/`:
- `format.go` — `Format`, `ParseFormat`, `FormatFlags`, `AddFormatFlags` (PreRunE mutual exclusivity), `ToAny[T any]` generic, convenience delegates on `FormatFlags`
- `json.go` — `WriteJSON` (replaces deprecated `OutputJSON`, `SetEscapeHTML(false)`)
- `filter.go` — `Filter`, `ParseFilters`, `ValidateFilterKeys`, `FilterFlags`, `AddFilterFlags`
- `template.go` — `DefaultFuncMap` (json returns error, title unicode-safe, truncate handles negative), `ExecuteTemplate` (checks Fprintln error)

PR review fixes: `FormatFlags` convenience delegates (`IsJSON()`, `IsTemplate()`, `IsDefault()`, `IsTableTemplate()`, `Template()`) eliminate `opts.Format.Format.IsJSON()` stutter. `cmdutil.ToAny[T any]` replaces per-command `toAny` helpers. `ImageSummary`/`ImageListResult` re-exported through `internal/docker/types.go` — `image list` no longer imports `pkg/whail`.

`image list` migrated as proof-of-concept: JSON, template, table-template, quiet, and filter-by-reference modes. 6 remaining list commands (`container list`, `volume list`, `network list`, `worktree list`, `container top`, `container stats`) need migration in subsequent PRs.

## IMPORTANT
Always check with the user before proceeding with any remaining todo item. If all work is done, ask the user if they want to delete this memory.