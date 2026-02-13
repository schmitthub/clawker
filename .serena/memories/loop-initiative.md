# Loop Initiative: ralph → loop Overhaul

**Branch:** `a/loop`
**Parent memory:** `project-overview`

---

## Progress Tracker

| Phase | Task | Status | Agent |
|-------|------|--------|-------|
| 1 | Task 1: Rename ralph → loop (command skeleton) | `complete` | opus |
| 1 | Task 2: Rename ralph → loop (internal package) | `pending` | — |
| 1 | Task 3: Rename ralph → loop (config, docs, references) | `pending` | — |
| 2 | Task 4: New command structure (iterate + tasks subcommands) | `pending` | — |
| 2 | Task 5: Flag definitions and option structs | `pending` | — |
| 2 | Task 6: Unit tests for command layer | `pending` | — |
| 3 | Task 7: claude -p execution engine | `pending` | — |
| 3 | Task 8: stream-json parser | `pending` | — |
| 3 | Task 9: LOOP_STATUS block (rename + integration) | `pending` | — |
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