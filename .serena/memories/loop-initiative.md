# Loop Initiative: ralph → loop Overhaul

**Branch:** `a/loop`
**Parent memory:** `project-overview`

---

## Progress Tracker

| Phase | Task | Status | Agent |
|-------|------|--------|-------|
| 1 | Task 1: Rename ralph → loop (command skeleton) | `complete` | opus |
| 1 | Task 2: Rename ralph → loop (internal package) | `complete` | opus |
| 1 | Task 3: Rename ralph → loop (config, docs, references) | `complete` (a2980b0) | opus |
| 2 | Task 4: New command structure (iterate + tasks subcommands) | `complete` | opus |
| 2 | Task 5: Flag definitions and option structs | `complete` | opus |
| 2 | Task 6: Unit tests for command layer | `complete` | opus |
| 3 | Task 7: claude -p execution engine | `complete` | opus |
| 3 | Task 8: stream-json parser | `complete` | opus |
| 3 | Task 9: LOOP_STATUS block (rename + integration) | `complete` | opus |
| 4 | Task 10: Hook injection system | `pending` | — |
| 4 | Task 11: Default hook set + user override mechanism | `pending` | — |
| 5 | Task 12: Container lifecycle integration | `pending` | — |
| 5 | Task 13: Auto agent naming | `pending` | — |
| 5 | Task 14: Session concurrency detection + worktree support | `pending` | — |
| 6 | Task 15: TUI dashboard (default view) | `pending` | — |
| 6 | Task 16: TUI detach → minimal mode | `pending` | — |
| 6 | Task 17: Output mode switching (verbose, json, quiet) | `pending` | — |
| 7 | Task 18: Config migration (ralph: → loop:) | `pending` | — |
| 7 | Task 19: Integration tests | `pending` | — |
| 7 | Task 20: Documentation update (CLAUDE.md, CLI-VERBS, memories) | `pending` | — |

## Key Learnings

(Agents append here as they complete tasks)

**Task 9:**
- RALPH_STATUS → LOOP_STATUS rename was already completed in Task 2. Task 9 focused on integration: system prompt, stream-aware analysis, and options wiring.
- Created `internal/loop/prompt.go` with `LoopStatusInstructions` constant — the default `--append-system-prompt` content that instructs the agent to output a structured LOOP_STATUS block. Contains a parseable example block; `TestLoopStatusInstructions_ExampleIsParseable` is a contract test ensuring the prompt stays in sync with `ParseStatus()`.
- `BuildSystemPrompt(additional string) string` — combines default LOOP_STATUS instructions with optional user instructions (from `--append-system-prompt` flag). Empty additional returns default only; non-empty appended after double newline with whitespace trimmed.
- Created `AnalyzeStreamResult(text string, result *ResultEvent) *AnalysisResult` — stream-json analysis path. Combines text analysis (ParseStatus, completion indicators, error signature, rate limit detection) with ResultEvent metadata. Maps `error_max_budget_usd` subtype to `RateLimitHit`. Captures `NumTurns`, `TotalCostUSD`, `DurationMS` from ResultEvent.
- Added three new fields to `AnalysisResult`: `NumTurns` (int), `TotalCostUSD` (float64), `DurationMS` (int). Zero when using `AnalyzeOutput` (raw stdout path). Populated only by `AnalyzeStreamResult`.
- Added `SystemPrompt` field to `loop.Options` — built by `BuildSystemPrompt()` which is called in `BuildRunnerOptions()`. Not yet consumed in `Runner.Run()` (the runner still builds claude commands without `--append-system-prompt`). Will be wired when the runner is refactored to use `claude -p` with structured output.
- `BuildRunnerOptions` now calls `loop.BuildSystemPrompt(loopOpts.AppendSystemPrompt)` to map the CLI `--append-system-prompt` flag through to `opts.SystemPrompt`.
- `TestAnalyzeStreamResult_CompatibleWithCircuitBreaker` verifies that stream-analyzed output feeds correctly into the circuit breaker's `UpdateWithAnalysis` method — the key integration point.
- Code reviewer found zero issues.
- Test count: 3851 → 3873 (+22 net). 9 new in prompt_test.go, 11 new in analyzer_test.go (AnalyzeStreamResult), 2 new in resolve_test.go (SystemPrompt wiring). All pass.

**Task 8:**
- Created `internal/loop/stream.go` — NDJSON parser for Claude Code's `--output-format stream-json` format. Defines Go types for all event kinds (system, assistant, user, result) plus content block types (text, tool_use, tool_result, thinking).
- `ParseStream(ctx, r, handler)` reads line-by-line, dispatches typed events via `StreamHandler` callbacks (OnSystem, OnAssistant, OnUser, OnResult — all optional), returns final `ResultEvent`. Malformed lines and unknown event types silently skipped for forward compatibility; malformed result events return error since they're terminal.
- `AssistantMessage` struct with `ExtractText()` (concatenates text blocks) and `ToolUseBlocks()` helpers. `ContentBlock` uses `json.RawMessage` for polymorphic `Input` (tool_use) and `Content` (tool_result) fields.
- `ContentBlock.ToolResultText()` handles both string and array-of-blocks forms of tool result content, with raw JSON fallback.
- `TextAccumulator` — convenience handler that collects assistant text + tool call count. Wired via `NewTextAccumulator()` which returns `(*TextAccumulator, *StreamHandler)`. Integrates directly with existing `AnalyzeOutput()` for LOOP_STATUS parsing.
- `ResultEvent` tracks success/error via Subtype field with helpers: `IsSuccess()`, `CombinedText()`. Error subtypes: `error_max_turns`, `error_during_execution`, `error_max_budget_usd`.
- `TokenUsage.Total()` nil-safe (returns 0 for nil receiver). Doc comment clarifies that cache tokens are not added separately because Anthropic API's `input_tokens` already accounts for cache reads.
- Scanner buffer: 64KB initial, 10MB max (handles large tool results from file reads/search results).
- No `stream_event` (token-level) support yet — only message-level events. Token streaming requires `--include-partial-messages` flag and will be added in Phase 6 if TUI needs real-time text display.
- Code reviewer found 3 issues: (1) `TokenUsage.Total()` nil pointer risk — fixed with nil guard, (2) `Total()` semantics unclear re cache tokens — fixed with doc comment, (3) empty Errors array on error result — added test to document behavior.
- Test count: 3798 → 3851 (+53 net). 43 new tests in stream_test.go (20 ParseStream + 23 helper/constant tests). All pass.

**Task 6:**
- Modernized `status.go`: removed deprecated `cmdutil.PrintError` (3 calls) and `cmdutil.PrintNextSteps` (1 call). Errors now use `return fmt.Errorf("context: %w", err)` for centralized rendering. Next-steps output uses `cs.InfoIcon()` inline. Replaced manual `json.MarshalIndent` with `cmdutil.WriteJSON`. Removed `encoding/json` import.
- Modernized `reset.go`: removed deprecated `cmdutil.PrintError` (3 calls). Errors now use `return fmt.Errorf("context: %w", err)`. Success output uses `cs.SuccessIcon()` for styled confirmation.
- Rewrote `status_test.go`: 6 tests following iterate/tasks pattern — `testFactory` helper, `runF` capture, command properties, required flags, JSON flag, defaults, flag existence, DI wiring.
- Rewrote `reset_test.go`: 9 tests following iterate/tasks pattern — `testFactory` helper, `runF` capture, command properties, required flags, `--all`, `--quiet`, `-q` shorthand, defaults, all-flags round-trip, flag existence, DI wiring. Removed `shlex.Split` dependency.
- Enhanced `loop_test.go`: split into 2 tests — `TestNewCmdLoop` (properties, no RunE) and `TestNewCmdLoop_Subcommands` (4 subcommands verified with Short descriptions and RunE).
- Status and reset `testFactory` omit TUI field (correct — non-interactive commands). Iterate/tasks include it (correct — they use TUI).
- Test count: 3774 → 3777 (+3 net). All 38 loop command tests pass.
- Code reviewer found zero issues.

**Task 7:**
- Scope diverged from original plan: instead of creating `internal/loop/exec.go` with `claude -p` command building, implemented the command→runner bridge — connecting the iterate/tasks command layer to the existing `loop.Runner` via shared helpers.
- Created `shared/resolve.go` with `ResolvePrompt`, `ResolveTasksPrompt`, `BuildRunnerOptions`. `ResolveTasksPrompt` uses `strings.Replace` (not `fmt.Sprintf`) to avoid format string injection from user-supplied templates.
- Created `shared/result.go` with `ResultOutput`, `NewResultOutput`, `WriteResult` for JSON/quiet/default output modes via `cmdutil.FormatFlags` and `cmdutil.WriteJSON`.
- Added `--agent` flag to shared `LoopOptions` in `AddLoopFlags`. Each subcommand marks it required independently.
- `BuildRunnerOptions` implements the config-override pattern: `flags.Changed("flag-name")` detects explicit CLI flags; config values only apply when flag wasn't explicitly set. `SessionExpirationHours` is config-only (no CLI flag).
- `SafetyCompletionThreshold` was missing from `loop.Options` — had to add it and wire into `CircuitBreakerConfig` in `Runner.Run`. This was a gap between the shared options layer and the core loop engine.
- StubRun tests (nil runF) broke because real implementations now need Docker/Config. Replaced with `RealRunNeedsDocker` tests using `testFactoryWithConfig` helper that wires Config + a Client returning "docker not available" error.
- `iterateRun` and `tasksRun` follow identical 12-step flow (resolve→config→docker→verify→runner→options→monitor→verbose→message→run→result→error). Steps 2-12 are duplicated — noted for future extraction into shared helper when the commands diverge enough to confirm the right abstraction boundary.
- Code reviewer found 4 issues: (1) stale Long description claiming auto container lifecycle — fixed, (2) `fmt.Sprintf` format string injection — fixed with `strings.Replace`, (3) orphaned `session-expiration-hours` flag guard — fixed to config-only application, (4) duplication between run functions — noted for future work.
- Test count: 3777 → 3798 (+21 net). All loop command tests pass.

**Task 5:**
- Created `internal/cmd/loop/shared/options.go` with `LoopOptions` struct, `NewLoopOptions()`, `AddLoopFlags(cmd, opts)`, and `MarkVerboseExclusive(cmd)`.
- Followed the `internal/cmd/container/shared/` pattern: plain struct with `NewContainerOptions()`/`AddFlags()` pair. `LoopOptions` is the loop equivalent.
- Flag defaults sourced from `loop.Default*` constants (single source of truth). Config file overrides will be resolved in the run function (not during flag parsing) — the pattern is `if !cmd.Flags().Changed("flag") && cfg != nil` layering.
- `IterateOptions` embeds `*shared.LoopOptions` + adds `--prompt`/`-p` and `--prompt-file` (mutually exclusive via Cobra, one required via `MarkFlagsOneRequired`).
- `TasksOptions` embeds `*shared.LoopOptions` + adds `--tasks` (required via `MarkFlagRequired`), `--task-prompt`/`--task-prompt-file` (mutually exclusive, both optional).
- Both commands wire full Factory DI: IOStreams, TUI, Client, Config, GitManager, Prompter — forward-looking for container lifecycle (Task 12).
- `FormatFlags` (`--json`/`--quiet`/`--format`) handled per-command via `cmdutil.AddFormatFlags(cmd)`, not in shared. `--verbose` registered in shared, exclusivity with format flags via `MarkVerboseExclusive(cmd)`.
- Cobra v1.8.1 supports `MarkFlagsOneRequired` and `MarkFlagsMutuallyExclusive` — no manual PreRunE validation needed.
- Tests are comprehensive: 11 tests for iterate, 10 for tasks. Cover flag parsing, mutual exclusivity, required flags, defaults vs `loop.Default*` constants, all-flags round-trip, format flag integration, verbose exclusivity. Test count went from 3747 to 3774.
- Updated `internal/cmd/loop/CLAUDE.md` with shared options reference, flag table, and testing section.
- Regenerated CLI reference docs: `clawker_loop_iterate.md` and `clawker_loop_tasks.md` now show all flags in `--help`.
- Code reviewer found zero issues.

**Task 4:**
- The previous agent had already created stub files for `iterate/iterate.go` and `tasks/tasks.go` with proper `NewCmd(f, runF)` pattern and stub `RunE` implementations.
- Updated `loop.go` to import iterate+tasks and remove run+tui imports. Updated Long description and Example to reflect the two loop strategies.
- Updated `loop_test.go` to verify 4 subcommands: iterate, tasks, status, reset (replacing run and tui).
- Deleted `internal/cmd/loop/run/` and `internal/cmd/loop/tui/` directories entirely (rm -rf).
- The `tui/tui.go` package had an import boundary violation (importing `bubbletea` directly + `internal/loop/tui`) — this violation is now removed by deleting the package.
- Regenerated `docs/cli-reference/` via `go run ./cmd/gen-docs` — deleted stale `clawker_loop_run.md` and `clawker_loop_tui.md` first.
- Updated CLAUDE.md top-level shortcuts reference from `loop run/status/reset` to `loop iterate/tasks/status/reset`.
- Status and reset commands still use deprecated helpers (`cmdutil.PrintError`, `cmdutil.PrintNextSteps`, `cmdutil.HandleError`) — these will be modernized in later tasks.
- Test count dropped from 3756 to 3747 due to removing run_test.go (4 tests) and tui_test.go (1 test) from the old packages.
- All 3747 unit tests pass.

**Task 3:**
- Used Serena `rename_symbol` for `RalphConfig` → `LoopConfig` (3 changes) and `Project/Ralph` → `Project/Loop` (4 changes) — handled type rename and field rename across all callers automatically.
- Yaml/mapstructure tags required manual edit since rename_symbol only renames the Go symbol, not struct tag content.
- `WithRalph` → `WithLoop` method rename was manual (not in symbol index) — only 1 caller in `project_builder_test.go`.
- Bulk sed `s/ralph/dev/g` on Go files replaced ~400 occurrences of "ralph" used as test agent name across 53 files. This caused 6 test failures in `container/list` where tests relied on "ralph" and "dev" being *distinct* agents for filter testing. Fixed by using "worker" as the contrasting agent name.
- CLI help `Example` strings in ~20 command files updated (ralph → dev as agent name).
- `TestRalph` → `TestLoop` and `runTestCategory(t, "ralph")` → `runTestCategory(t, "loop")` in CLI acceptance tests.
- Moved `test/cli/testdata/ralph/` → `test/cli/testdata/loop/` via `git mv`, updated txtar file content (ralph → loop for commands).
- Regenerated `docs/cli-reference/` via `go run ./cmd/gen-docs` after deleting old `clawker_ralph_*` files.
- Renamed `.claude/memories/RALPH-TUI-PRD.md` → `LOOP-TUI-PRD.md` and `RALPH-DESIGN.md` → `LOOP-DESIGN.md`.
- Updated 15+ markdown files (CLAUDE.md, CLI-VERBS.md, ARCHITECTURE.md, config/CLAUDE.md, etc.) with systematic sed passes.
- Archive files (`archive/`) intentionally left unchanged — they represent historical documentation.
- All 3756 unit tests pass.

**Task 2:**
- Used `git mv internal/ralph internal/loop` to preserve git history cleanly.
- Serena `rename_symbol` tool handled `LoopOptions→Options` and `LoopResult→Result` renames across the codebase automatically (2 changes and 1 change respectively).
- `RALPH_STATUS` → `LOOP_STATUS` was a bulk text replacement across analyzer.go, analyzer_test.go, circuit.go, circuit_test.go (also updated `END_RALPH_STATUS` → `END_LOOP_STATUS` via the same replacement).
- The TUI model_test.go had a hardcoded "RALPH DASHBOARD" assertion that failed initially — needed to update to "LOOP DASHBOARD".
- `DefaultSessionStore()` and `DefaultHistoryStore()` now use `~/.local/clawker/loop/` path instead of `~/.local/clawker/ralph/`.
- `cfg.Ralph` config field references in run.go are intentionally left as-is — they reference the config schema struct field, which is Task 3's responsibility.
- All 3756 unit tests pass.

**Task 1:**
- The `internal/cmd/ralph/` package had 4 subcommands: run, status, reset, tui. Each follows the `NewCmd(f, runF)` pattern with test trapdoor.
- `run/run.go` still references `internal/ralph` (the core logic package) — this will be renamed in Task 2.
- `tui/tui.go` imports `internal/ralph/tui` directly + `bubbletea` — this violates the import boundary rule (only `internal/tui` should import bubbletea). This needs to be addressed when the TUI is rewritten in Phase 6.
- No top-level aliases exist for ralph in `aliases.go`.
- Root.go registration is a simple `cmd.AddCommand` — no special wiring needed.
- The old ralph directory was cleanly deleted after creating the new loop directory. All 3756 unit tests pass.

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Run a single `code-reviewer` subagent to review this task's changes, then fix any findings
5. Commit all changes from this task with a descriptive commit message
6. Present the handoff prompt from the task's Wrap Up section to the user
7. Wait for the user to start a new conversation with the handoff prompt

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Background

Clawker is a CLI tool for managing Docker-based development containers with Claude Code integration. The `loop` command group orchestrates autonomous AI agent loops — running Claude Code repeatedly inside containers with circuit breaker protection, session persistence, and real-time monitoring.

The `ralph` command group is being rewritten as `clawker loop`. "Ralph Wiggum" is a buzzword for what is fundamentally an agentic AI loop mechanism. The overhaul replaces the current implementation with a cleaner architecture that leverages Claude Code's headless mode (`claude -p`), hook system, and streaming output instead of raw `docker exec` with manual stdout parsing.

### Requirements (from interview)

**Two loop strategies:**
1. **Iterate** (`loop iterate`): Same prompt repeated fresh each invocation. Agent only sees codebase state from previous runs, no conversation context carried forward.
2. **Tasks** (`loop tasks`): Agent reads a task file, picks an open task, does it, marks it done. Clawker is the "dumb loop" — it doesn't parse the task file, the agent LLM handles that.

**Execution model:**
- Each iteration runs `claude -p` inside the container (not raw `docker exec`)
- Fresh session each iteration (no `--continue`/`--resume`)
- `--output-format stream-json` for real-time monitoring
- `--append-system-prompt` to inject LOOP_STATUS block instructions
- `--allowedTools` for permission control

**Hook system:**
- Clawker provides default hooks OR user overrides via flag/config — no merging between the two
- Hooks injected into container's `.claude/settings.json` before loop starts

**Container lifecycle:**
- `loop iterate/tasks` handles full lifecycle: create container, run loop, destroy on completion
- Auto-generated agent names (user doesn't supply them)
- Optional `--worktree` flag for worktree-based isolation
- Warning when starting a loop in a directory with an active session (suggest worktree)

**Output model (foreground process):**
1. **Default: TUI dashboard** (BubbleTea) — launched automatically
2. User can exit TUI → drops to minimal event output, loop continues
3. `--verbose`/`--stream` flag: Non-interactive stream-json forwarding
4. `--json`: Machine-readable structured output
5. `--quiet`: Errors only

**Session/circuit:**
- RALPH_STATUS → LOOP_STATUS (same format, renamed)
- All existing circuit breaker conditions kept (stagnation, same-error, output decline, test-only loops, safety completion)
- Sessions tracked by project directory or worktree
- `loop status` and `loop reset` subcommands kept (renamed from ralph)

**Cross-terminal TUI (tabled for future):**
- `clawker tui` session picker requires daemon or IPC — out of scope for now

### Key Files

**Command layer (being rewritten):**
- `internal/cmd/ralph/ralph.go` → `internal/cmd/loop/loop.go`
- `internal/cmd/ralph/run/run.go` → split into `iterate/` and `tasks/`
- `internal/cmd/ralph/status/status.go` → `internal/cmd/loop/status/`
- `internal/cmd/ralph/reset/reset.go` → `internal/cmd/loop/reset/`
- `internal/cmd/ralph/tui/tui.go` → integrated into iterate/tasks default

**Core logic (being rewritten):**
- `internal/ralph/loop.go` → `internal/loop/loop.go` (switch to claude -p)
- `internal/ralph/circuit.go` → `internal/loop/circuit.go` (keep all conditions)
- `internal/ralph/analyzer.go` → `internal/loop/analyzer.go` (LOOP_STATUS)
- `internal/ralph/session.go` → `internal/loop/session.go`
- `internal/ralph/monitor.go` → `internal/loop/monitor.go`
- `internal/ralph/config.go` → `internal/loop/config.go`
- `internal/ralph/ratelimit.go` → `internal/loop/ratelimit.go`
- `internal/ralph/history.go` → `internal/loop/history.go`
- `internal/ralph/tui/` → `internal/loop/tui/` or integrated into `internal/tui/`

**Config:**
- `internal/config/schema.go` — `RalphConfig` → `LoopConfig`
- `clawker.yaml` — `ralph:` → `loop:` section

**Container lifecycle (reused):**
- `internal/cmd/container/shared/` — `CreateContainer()`, flag types
- `internal/docker/` — Client, ContainerName, container lifecycle
- `internal/cmd/factory/default.go` — Factory wiring

**Existing patterns to follow:**
- `internal/cmdutil/format.go` — `FormatFlags` for --format/--json/--quiet
- `internal/cmdutil/json.go` — `WriteJSON` for structured output
- `internal/tui/` — TUI layer (only package that imports bubbletea/bubbles)
- `internal/iostreams/` — ColorScheme, styles, spinner

### Design Patterns

- **Factory DI**: Commands receive `*cmdutil.Factory`, store closure fields on Options struct
- **NewCmd(f, runF)**: Cobra command constructor with test trapdoor
- **FormatFlags**: Reusable `--format`/`--json`/`--quiet` flag handling
- **WriteJSON**: Pretty-printed JSON output (replaces manual json.Marshal)
- **CreateContainer()**: Single entry point for container creation in `shared/`
- **FakeClient**: `dockertest.NewFakeClient()` for command unit tests
- **Code style**: No deprecated helpers (`PrintError`, `HandleError`, `PrintNextSteps`)

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and package `CLAUDE.md` before starting
- Use Serena tools for code exploration — read symbol bodies only when needed
- All new code must compile and tests must pass
- Follow existing test patterns in the package
- No `bubbletea`/`bubbles` imports outside `internal/tui/`
- No deprecated helpers in new code
- Use `FormatFlags` + `WriteJSON` for output formatting

---

## Phase 1: Rename ralph → loop

### Task 1: Rename command package (ralph → loop)

**Creates/modifies:** `internal/cmd/loop/` (new), delete `internal/cmd/ralph/`
**Depends on:** nothing

#### Implementation

1. Create `internal/cmd/loop/` directory structure mirroring current ralph
2. Copy `internal/cmd/ralph/ralph.go` → `internal/cmd/loop/loop.go`
3. Rename package, command Use field to "loop", update descriptions (remove "Ralph Wiggum" buzzword)
4. Copy subcommand packages (run/, status/, reset/, tui/) preserving functionality for now
5. Update `internal/cmd/root/root.go` to register `loop.NewCmdLoop(f)` instead of `ralph.NewCmdRalph(f)`
6. Add `loop` as top-level shortcut in root if `ralph` had one
7. Update all import paths

#### Acceptance Criteria

```bash
go build ./cmd/clawker
go test ./internal/cmd/loop/...
# Verify `clawker loop run`, `clawker loop status`, `clawker loop reset` all parse correctly
```

#### Wrap Up

1. Update Progress Tracker: Task 1 -> `complete`
2. Append key learnings
3. Run `code-reviewer` subagent, fix findings
4. Commit: `refactor(cmd): rename ralph command group to loop`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Task 1 is complete. Begin Task 2: Rename internal ralph package to loop."

---

### Task 2: Rename internal package (ralph → loop)

**Creates/modifies:** `internal/loop/` (new), delete `internal/ralph/`
**Depends on:** Task 1

#### Implementation

1. Create `internal/loop/` directory
2. Move all files from `internal/ralph/` → `internal/loop/`
3. Rename package declarations from `ralph` to `loop`
4. Update all import paths across the codebase (cmd/loop/, test/agents/, etc.)
5. Rename exported types/constants:
   - `ralph.Runner` → `loop.Runner`
   - `ralph.LoopOptions` → `loop.Options` (or `loop.RunOptions`)
   - `ralph.LoopResult` → `loop.Result`
   - `ralph.Monitor` → `loop.Monitor`
   - `ralph.DefaultMaxLoops` → `loop.DefaultMaxLoops` (etc.)
   - `RALPH_STATUS` → `LOOP_STATUS` in analyzer.go
6. Update `internal/loop/CLAUDE.md`
7. Move `internal/ralph/tui/` → `internal/loop/tui/`

#### Acceptance Criteria

```bash
go build ./cmd/clawker
go test ./internal/loop/...
go test ./internal/cmd/loop/...
make test
```

#### Wrap Up

1. Update Progress Tracker: Task 2 -> `complete`
2. Append key learnings
3. Run `code-reviewer` subagent, fix findings
4. Commit: `refactor(loop): rename internal ralph package to loop`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Task 2 is complete. Begin Task 3: Rename config, docs, and remaining references."

---

### Task 3: Config, docs, and remaining references

**Creates/modifies:** `internal/config/schema.go`, `.claude/docs/CLI-VERBS.md`, `CLAUDE.md`, memories
**Depends on:** Task 2

#### Implementation

1. `internal/config/schema.go`: Rename `RalphConfig` → `LoopConfig`, `ralph:` → `loop:` yaml tag
2. Update all references to `cfg.Ralph` → `cfg.Loop` across codebase
3. Update `clawker.yaml` template in `templates/`
4. Update `.claude/docs/CLI-VERBS.md` — ralph section → loop section
5. Update root `CLAUDE.md` — ralph references → loop
6. Update `internal/cmd/loop/CLAUDE.md`
7. Update `internal/loop/CLAUDE.md`
8. Grep for any remaining "ralph" references and update
9. Update Serena memories that reference ralph

#### Acceptance Criteria

```bash
go build ./cmd/clawker
make test
# Grep for "ralph" should return 0 results (except git history)
grep -ri "ralph" --include="*.go" --include="*.md" --include="*.yaml" . | grep -v ".git/" | grep -v "vendor/"
```

#### Wrap Up

1. Update Progress Tracker: Task 3 -> `complete`
2. Append key learnings
3. Run `code-reviewer` subagent, fix findings
4. Commit: `refactor(config): rename ralph config to loop, update all docs and references`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Phase 1 (Tasks 1-3) is complete. All ralph→loop renaming done. Begin Phase 2, Task 4: New command structure with iterate and tasks subcommands."

---

## Phase 2: New Command Structure

### Task 4: iterate + tasks subcommands

**Creates/modifies:** `internal/cmd/loop/iterate/`, `internal/cmd/loop/tasks/`
**Depends on:** Phase 1

#### Implementation

1. Replace `internal/cmd/loop/run/` with two new subcommand packages:
   - `internal/cmd/loop/iterate/iterate.go` — `NewCmdIterate(f, runF)`
   - `internal/cmd/loop/tasks/tasks.go` — `NewCmdTasks(f, runF)`
2. Update `internal/cmd/loop/loop.go` to register both:
   ```go
   cmd.AddCommand(iterate.NewCmdIterate(f, nil))
   cmd.AddCommand(tasks.NewCmdTasks(f, nil))
   cmd.AddCommand(status.NewCmdStatus(f, nil))
   cmd.AddCommand(reset.NewCmdReset(f, nil))
   ```
3. Remove old `run/` subcommand package
4. Remove `tui/` subcommand (TUI is now integrated into iterate/tasks as default output)
5. Design command descriptions:
   - `loop iterate` — "Run an agent loop with a repeated prompt"
   - `loop tasks` — "Run an agent loop driven by a task file"
6. Stub `RunE` implementations (return "not yet implemented" or delegate to old loop engine temporarily)

#### Acceptance Criteria

```bash
go build ./cmd/clawker
go test ./internal/cmd/loop/...
# Verify: clawker loop iterate --help, clawker loop tasks --help
```

#### Wrap Up

1. Update Progress Tracker: Task 4 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `feat(loop): add iterate and tasks subcommands, remove run/tui`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Task 4 is complete. Begin Task 5: Flag definitions and option structs for iterate and tasks."

---

### Task 5: Flag definitions and option structs

**Creates/modifies:** `internal/cmd/loop/iterate/iterate.go`, `internal/cmd/loop/tasks/tasks.go`, potentially `internal/cmd/loop/shared/`
**Depends on:** Task 4

#### Implementation

1. Design `IterateOptions` struct:
   - **Factory DI**: `IOStreams`, `Client`, `Config`, `TUI`
   - **Prompt source** (mutually exclusive): `--prompt` / `--prompt-file`
   - **Loop control**: `--max-loops`, `--stagnation-threshold`, `--timeout`, `--loop-delay`
   - **Circuit breaker tuning**: `--same-error-threshold`, `--output-decline-threshold`, `--max-test-loops`, `--strict-completion`
   - **Execution**: `--skip-permissions`, `--calls-per-hour`, `--reset-circuit`
   - **Hooks**: `--hooks-file` (path to override hook config; omit = use defaults)
   - **System prompt**: `--append-system-prompt` (additional instructions beyond LOOP_STATUS default)
   - **Container**: `--worktree` (create in worktree), `--image` (override), other container creation flags as needed
   - **Output**: Use `FormatFlags` for `--json`/`--quiet`, plus `--verbose` for stream mode
   - **NO `--agent` flag** — auto-generated

2. Design `TasksOptions` struct:
   - Everything from `IterateOptions` EXCEPT `--prompt`/`--prompt-file`
   - **PLUS**: `--tasks` (path to task file, required), `--task-prompt` / `--task-prompt-file` (template for how to instruct the agent about the task file)

3. Extract shared flags into `internal/cmd/loop/shared/` if there's significant overlap

4. Use `FormatFlags` (from `cmdutil`) instead of manual `--json`/`--quiet` handling

5. Register all flags on Cobra commands with proper:
   - Mutual exclusivity groups
   - Required flags
   - Default values from `loop.Default*` constants

6. Modernize: No deprecated helpers. Use `cmdutil.WriteJSON`, `FormatFlags`, `cs.WarningIcon()` patterns.

#### Acceptance Criteria

```bash
go build ./cmd/clawker
go test ./internal/cmd/loop/...
# Verify: flag parsing tests pass for both iterate and tasks
# Verify: --prompt and --prompt-file are mutually exclusive
# Verify: --tasks is required for tasks subcommand
```

#### Wrap Up

1. Update Progress Tracker: Task 5 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `feat(loop): define flag and option structs for iterate and tasks`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Task 5 is complete. Begin Task 6: Unit tests for command layer."

---

### Task 6: Unit tests for command layer

**Creates/modifies:** `internal/cmd/loop/loop_test.go`, `iterate/iterate_test.go`, `tasks/tasks_test.go`, `status/status_test.go`, `reset/reset_test.go`
**Depends on:** Task 5

#### Implementation

1. Test parent command structure (`loop_test.go`):
   - Verify 4 subcommands (iterate, tasks, status, reset)
   - Verify command names, descriptions

2. Test iterate flag parsing (`iterate/iterate_test.go`):
   - All expected flags exist with correct defaults
   - Required flags enforced (prompt or prompt-file)
   - Mutual exclusivity (prompt vs prompt-file)
   - Shorthand flags work
   - All combinations parse correctly
   - Uses `runF` capture pattern

3. Test tasks flag parsing (`tasks/tasks_test.go`):
   - Same as iterate tests + `--tasks` required flag
   - `--task-prompt` / `--task-prompt-file` parsing

4. Update/rewrite status and reset tests for modernized implementations

5. Follow existing test patterns: `shlex.Split`, callback capture, no Docker deps

#### Acceptance Criteria

```bash
go test ./internal/cmd/loop/... -v
# All tests pass, good coverage of flag parsing edge cases
```

#### Wrap Up

1. Update Progress Tracker: Task 6 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `test(loop): comprehensive unit tests for loop command layer`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Phase 2 (Tasks 4-6) complete. Command structure defined and tested. Begin Phase 3, Task 7: claude -p execution engine."

---

## Phase 3: Core Loop Engine Rewrite

### Task 7: claude -p execution engine

**Creates/modifies:** `internal/loop/exec.go` (new), `internal/loop/loop.go`
**Depends on:** Phase 2

#### Implementation

1. Create `internal/loop/exec.go` with a new execution function that:
   - Builds the `claude -p` command with appropriate flags:
     - `claude -p "<prompt>"` (base invocation)
     - `--output-format stream-json` (for streaming) or `--output-format json` (for simple)
     - `--append-system-prompt "<LOOP_STATUS instructions>"`
     - `--allowedTools "<tools>"` (from skip-permissions or explicit tool list)
   - Runs via `docker exec` (non-TTY) inside the target container
   - Returns structured result from JSON output

2. Replace `Runner.ExecCapture()` with new execution function
   - Old: raw docker exec, manual stdout/stderr split via stdcopy
   - New: docker exec running `claude -p`, structured JSON output

3. Keep the existing hijacked connection + context cancellation pattern (stdcopy doesn't respect ctx)

4. Define execution result type that maps to claude -p JSON output

#### Acceptance Criteria

```bash
go build ./cmd/clawker
go test ./internal/loop/... -v
```

#### Wrap Up

1. Update Progress Tracker: Task 7 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `feat(loop): claude -p execution engine replacing raw docker exec`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Task 7 complete. Begin Task 8: stream-json parser for real-time monitoring."

---

### Task 8: stream-json parser

**Creates/modifies:** `internal/loop/stream.go` (new), `internal/loop/stream_test.go`
**Depends on:** Task 7

#### Implementation

1. Create `internal/loop/stream.go`:
   - NDJSON line parser for Claude Code's `--output-format stream-json` format
   - Event types: text_delta, tool_use, tool_result, etc.
   - Callback-based architecture for feeding events to TUI or verbose output

2. Define stream event types matching Claude Code's stream-json schema:
   - Investigate the actual stream-json output format from Claude Code docs
   - Create Go types for each event kind

3. Create parser that reads line-by-line from a reader, deserializes, and dispatches callbacks

4. Unit tests with fixture data

#### Acceptance Criteria

```bash
go test ./internal/loop/stream_test.go -v
# Parser correctly handles all event types from stream-json
```

#### Wrap Up

1. Update Progress Tracker: Task 8 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `feat(loop): stream-json parser for real-time agent monitoring`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Task 8 complete. Begin Task 9: LOOP_STATUS block rename and integration."

---

### Task 9: LOOP_STATUS block integration

**Creates/modifies:** `internal/loop/analyzer.go`, `internal/loop/analyzer_test.go`
**Depends on:** Task 7

#### Implementation

1. Rename all `RALPH_STATUS` references to `LOOP_STATUS` in analyzer.go
2. Update `ParseStatus()` to extract from claude -p JSON output (not raw stdout)
3. Ensure the `--append-system-prompt` instructions tell the agent to output LOOP_STATUS
4. Update all test fixtures
5. Verify circuit breaker still receives correct Status objects

#### Acceptance Criteria

```bash
go test ./internal/loop/analyzer_test.go -v
go test ./internal/loop/circuit_test.go -v
# No references to RALPH_STATUS remain
```

#### Wrap Up

1. Update Progress Tracker: Task 9 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `refactor(loop): rename RALPH_STATUS to LOOP_STATUS`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Phase 3 (Tasks 7-9) complete. Core engine uses claude -p with streaming and LOOP_STATUS. Begin Phase 4, Task 10: Hook injection system."

---

## Phase 4: Hook System

### Task 10: Hook injection system

**Creates/modifies:** `internal/loop/hooks.go` (new), integration with container setup
**Depends on:** Phase 3

#### Implementation

1. Create `internal/loop/hooks.go`:
   - Define default hook configuration as Go struct → JSON
   - Default hooks to include:
     - `Stop` hook: enforce LOOP_STATUS block output
     - `SessionStart` (compact matcher): re-inject critical context after compaction
   - Function to serialize hooks to Claude Code settings.json format

2. Integration point: inject hooks into container's `.claude/settings.json` during container creation (before loop starts)
   - Use existing `containerfs` / `shared.InitContainerConfig` patterns
   - Or inject via docker exec + file write after container start

3. Define the hook override mechanism:
   - If user provides `--hooks-file`, use that file's content entirely (no merging with defaults)
   - If `loop.hooks` exists in `clawker.yaml`, use that (no merging)
   - Otherwise, use clawker's default hooks

#### Acceptance Criteria

```bash
go test ./internal/loop/hooks_test.go -v
# Default hooks serialize to valid Claude Code settings.json format
# Override mechanism works correctly (replaces, doesn't merge)
```

#### Wrap Up

1. Update Progress Tracker: Task 10 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `feat(loop): hook injection system with default hooks and user override`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Task 10 complete. Begin Task 11: Define default hook set and finalize override mechanism."

---

### Task 11: Default hook set + user override

**Creates/modifies:** `internal/loop/hooks.go`, `internal/config/schema.go`
**Depends on:** Task 10

#### Implementation

1. Finalize the default hook set (research what hooks are most useful for the loop):
   - `Stop` hook with prompt-based check for LOOP_STATUS completeness
   - `SessionStart` (compact) for context re-injection
   - Potentially: `PostToolUse` for activity logging

2. Add `loop.hooks` section to config schema:
   ```yaml
   loop:
     hooks:
       file: "path/to/hooks.json"  # Optional: override default hooks entirely
   ```

3. Wire the `--hooks-file` flag through to the hook injection system

4. Test: default hooks used when no override; override completely replaces defaults

#### Acceptance Criteria

```bash
go test ./internal/loop/... -v
# Hooks system works end-to-end in unit tests
```

#### Wrap Up

1. Update Progress Tracker: Task 11 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `feat(loop): default hook configuration and override mechanism`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Phase 4 (Tasks 10-11) complete. Hook system ready. Begin Phase 5, Task 12: Container lifecycle integration."

---

## Phase 5: Container Lifecycle

### Task 12: Container lifecycle integration

**Creates/modifies:** `internal/cmd/loop/iterate/iterate.go`, `internal/cmd/loop/tasks/tasks.go`, shared helpers
**Depends on:** Phase 3, Phase 4

#### Implementation

1. Wire `loop iterate` to handle full container lifecycle:
   - Create container using `shared.CreateContainer()` from container/shared
   - Start container
   - Wait for ready
   - Inject hooks
   - Run loop iterations
   - On exit: stop and remove container (with proper cleanup via defer)

2. Same for `loop tasks`

3. Share container lifecycle code between iterate and tasks (extract to `internal/cmd/loop/shared/` if needed)

4. Handle Ctrl+C gracefully:
   - Catch SIGINT
   - Stop loop cleanly
   - Clean up container
   - Report final status

5. Wire container creation flags (image, worktree, etc.) through from options

#### Acceptance Criteria

```bash
go build ./cmd/clawker
# Manual test: clawker loop iterate --prompt "echo hello" creates container, runs, destroys
```

#### Wrap Up

1. Update Progress Tracker: Task 12 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `feat(loop): full container lifecycle management in iterate and tasks`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Task 12 complete. Begin Task 13: Auto agent naming."

---

### Task 13: Auto agent naming

**Creates/modifies:** `internal/loop/naming.go` (new), integrate with iterate/tasks
**Depends on:** Task 12

#### Implementation

1. Create `internal/loop/naming.go`:
   - Generate unique agent names for loop sessions (e.g., `loop-<short-hash>` or `loop-<timestamp>`)
   - Ensure names are valid Docker container name components
   - Ensure uniqueness within project

2. Remove `--agent` flag from iterate and tasks (auto-generated only)

3. Wire auto-generated name into container creation and session tracking

4. Display the generated name in TUI/output so user can reference it

#### Acceptance Criteria

```bash
go test ./internal/loop/naming_test.go -v
# Names are valid, unique, and deterministic-enough for identification
```

#### Wrap Up

1. Update Progress Tracker: Task 13 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `feat(loop): auto-generated agent names for loop sessions`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Task 13 complete. Begin Task 14: Session concurrency detection and worktree support."

---

### Task 14: Session concurrency + worktree support

**Creates/modifies:** `internal/loop/session.go`, iterate/tasks command files
**Depends on:** Task 13

#### Implementation

1. Session tracking by project directory (or worktree path):
   - Session store keyed by directory path, not project+agent
   - Detect active sessions in the same directory

2. Concurrency warning:
   - When starting a loop, check for active sessions in the same directory
   - Warn user: "An active loop session exists in this directory. Create a worktree instead?"
   - Use prompter for interactive confirmation (non-interactive: proceed with warning)
   - User can proceed (accepts concurrency risk) or abort

3. `--worktree` flag:
   - Creates a git worktree for the loop
   - Uses existing `git.GitManager` worktree operations
   - Container bind-mounts the worktree path instead of project root

#### Acceptance Criteria

```bash
go test ./internal/loop/session_test.go -v
# Concurrency detection works
# Worktree flag creates worktree and uses it for container
```

#### Wrap Up

1. Update Progress Tracker: Task 14 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `feat(loop): session concurrency detection and worktree support`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Phase 5 (Tasks 12-14) complete. Container lifecycle, naming, and concurrency ready. Begin Phase 6, Task 15: TUI dashboard."

---

## Phase 6: TUI & Output Modes

### Task 15: TUI dashboard (default view)

**Creates/modifies:** `internal/tui/loop/` (new TUI models), wire into iterate/tasks
**Depends on:** Phase 3 (stream parser), Phase 5 (container lifecycle)

#### Implementation

1. Create TUI models in `internal/tui/` (not internal/loop/tui — respect import boundary):
   - Loop dashboard model: shows current iteration, agent status, recent activity
   - Feed from stream-json events via channel
   - Show: iteration count, elapsed time, current status, last LOOP_STATUS, circuit breaker state

2. Wire into `loop iterate` and `loop tasks` as default output mode:
   - When TTY detected and no `--json`/`--quiet`/`--verbose`: launch TUI
   - TUI receives stream events from the loop engine

3. Route through `f.TUI` (Factory noun) per code style rules
   - NO direct bubbletea imports in command packages

4. Handle TUI launch via `tui.RunProgram()` pattern

#### Acceptance Criteria

```bash
go build ./cmd/clawker
go test ./internal/tui/... -v
# TUI renders correctly (golden file tests if applicable)
```

#### Wrap Up

1. Update Progress Tracker: Task 15 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `feat(tui): loop dashboard model for real-time monitoring`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Task 15 complete. Begin Task 16: TUI detach to minimal mode."

---

### Task 16: TUI detach → minimal mode

**Creates/modifies:** TUI models, iterate/tasks command files
**Depends on:** Task 15

#### Implementation

1. Add key binding to TUI dashboard for detaching (e.g., `q` or `Esc`):
   - Exits TUI (restores terminal)
   - Loop continues running in foreground
   - Switches to minimal event output (spinners + status lines)

2. Minimal mode output:
   - "Iteration 1: agent running..." (spinner)
   - "Iteration 1: completed (3 tasks, 5 files)"
   - "Iteration 2: agent running..." (spinner)
   - Circuit breaker events, warnings

3. Terminal state management:
   - Clean TUI exit (alternate screen off, cursor visible)
   - Seamless transition to line-by-line output

#### Acceptance Criteria

```bash
# Manual test: launch TUI, press q, verify loop continues with minimal output
```

#### Wrap Up

1. Update Progress Tracker: Task 16 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `feat(loop): TUI detach to minimal output mode`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Task 16 complete. Begin Task 17: Output mode switching."

---

### Task 17: Output mode switching

**Creates/modifies:** iterate/tasks command files, output formatting
**Depends on:** Task 16

#### Implementation

1. Wire `FormatFlags` into iterate and tasks options
2. Implement all output modes:
   - **TUI** (default, TTY): Full dashboard
   - **Verbose** (`--verbose`): Forward stream-json events as formatted text
   - **JSON** (`--json` / `--format json`): Machine-readable result via `WriteJSON`
   - **Quiet** (`--quiet`): Errors only
3. Non-TTY default: minimal event output (not TUI)
4. Proper mutual exclusivity between output modes
5. Remove all deprecated helper usage from status/reset commands too

#### Acceptance Criteria

```bash
go test ./internal/cmd/loop/... -v
# All output modes produce correct output
# FormatFlags integration works
```

#### Wrap Up

1. Update Progress Tracker: Task 17 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `feat(loop): complete output mode system with FormatFlags`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Phase 6 (Tasks 15-17) complete. All output modes working. Begin Phase 7, Task 18: Config migration."

---

## Phase 7: Integration & Polish

### Task 18: Config migration (ralph: → loop:)

**Creates/modifies:** `internal/config/schema.go`, validation, migration logic
**Depends on:** All previous phases

#### Implementation

1. If not already done in Task 3: finalize `LoopConfig` struct with any new fields added during development:
   - `hooks.file` (string, optional)
   - Any other new config fields discovered during implementation

2. Add config validation for new fields

3. Consider backward compatibility: should `ralph:` in existing clawker.yaml files still work with a deprecation warning?
   - If yes, add migration/alias logic in config loader
   - If no (pre-release, zero users), just rename

4. Update config template in `templates/`

#### Acceptance Criteria

```bash
go test ./internal/config/... -v
# LoopConfig loads correctly from clawker.yaml
```

#### Wrap Up

1. Update Progress Tracker: Task 18 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `feat(config): finalize loop config with new fields`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Task 18 complete. Begin Task 19: Integration tests."

---

### Task 19: Integration tests

**Creates/modifies:** `test/agents/loop_test.go` (rename from ralph_test.go), potentially new test files
**Depends on:** All previous phases

#### Implementation

1. Rename `test/agents/ralph_test.go` → `test/agents/loop_test.go`
2. Update existing integration tests for new command names and behavior
3. Add integration tests for:
   - `loop iterate` full lifecycle (create container, run 1-2 iterations, exit)
   - Session persistence across iterations
   - Circuit breaker trip on stagnation
   - Hook injection verification
   - Worktree mode (if feasible in test environment)
   - LOOP_STATUS block parsing from claude -p output

4. Follow test harness patterns (`harness.SkipIfNoDocker`, `harness.NewTestClient`, etc.)

#### Acceptance Criteria

```bash
go test ./test/agents/... -v -timeout 15m
# All loop integration tests pass
```

#### Wrap Up

1. Update Progress Tracker: Task 19 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `test(loop): integration tests for loop iterate and tasks`
5. **STOP.** Handoff prompt:

> "Continue the Loop Initiative. Read the Serena memory `loop-initiative` — Task 19 complete. Begin Task 20: Documentation update."

---

### Task 20: Documentation update

**Creates/modifies:** Multiple CLAUDE.md files, CLI-VERBS.md, Serena memories
**Depends on:** All previous phases

#### Implementation

1. Update `CLAUDE.md` (root):
   - Replace all ralph references with loop
   - Update key concepts table
   - Update CLI commands section
   - Update config section

2. Update `.claude/docs/CLI-VERBS.md`:
   - Replace ralph section with loop section
   - Document iterate and tasks subcommands
   - Document all new flags
   - Document LOOP_STATUS block format

3. Update/create package CLAUDE.md files:
   - `internal/cmd/loop/CLAUDE.md`
   - `internal/loop/CLAUDE.md`

4. Update `.claude/rules/` if any ralph-specific rules exist

5. Update Serena memories:
   - Update `project-overview` memory
   - Update any other memories referencing ralph

6. Run `bash scripts/check-claude-freshness.sh` to verify no stale docs

#### Acceptance Criteria

```bash
bash scripts/check-claude-freshness.sh
# No stale CLAUDE.md files
grep -ri "ralph" --include="*.md" . | grep -v ".git/" | grep -v "vendor/" | grep -v "CHANGELOG"
# No remaining ralph references (except changelog/history)
```

#### Wrap Up

1. Update Progress Tracker: Task 20 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, fix findings
4. Commit: `docs: complete documentation update for loop overhaul`
5. **STOP.** Initiative complete! Present summary to user.

> "The Loop Initiative is complete. All 20 tasks across 7 phases have been implemented. The ralph command group has been fully replaced with loop, featuring iterate and tasks modes, claude -p execution, hook injection, TUI dashboard, and auto agent naming."