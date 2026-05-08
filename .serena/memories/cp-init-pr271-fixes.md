# CP-Init PR #271 Review Findings — Remediation Initiative

**Branch:** `feat/agent-cp-init`
**Parent memory:** —
**PRD Reference:** —
**PR:** https://github.com/schmitthub/clawker/pull/271

Source: 5-agent comprehensive review of PR #271 produced 19 findings across Critical / Important / Suggestion. User directive: fix all, no deferrals. This initiative sequences the fixes across 4 fresh-context tasks.

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Critical recover hardening + degrade-path test | `complete` | claude-opus-4-7 |
| Task 2: Type-design — `Init` substruct + `step` seal + `runStep` tuple | `pending` | — |
| Task 3: Entrypoint loud-failure + timeout coupling | `pending` | — |
| Task 4: Test gaps + protocol fixes + doc/comment polish | `pending` | — |

## Key Learnings

### Task 1 (2026-05-08)

- **Layered recovers, layered concerns.** Rather than one big recover at the goroutine boundary, split into per-scope handlers: `Executor.Run` recovers and converts panic → error (preserves dialer.runInit's existing log+continue path and asymmetric trust). DialAgent goroutine recover catches anything else (publishes synthetic SessionFailed). Each layer cleans up its own bus pairings (InitStarted/InitFailed, Connecting/SessionFailed). Avoid re-panicking from inner recovers — they end up at the outer recover with no step/identity context.
- **Defer order is load-bearing.** In `DialAgent`, register dedup-cleanup defer FIRST so it runs LAST (LIFO). Recover defer is registered second so it runs first, catching panics including those that escape from the cleanup defer itself.
- **Use `context.Background()` in recover-side publishes.** `publishFailed` short-circuits on `ctx.Err() != nil`; the panic path needs the worldview transition to land even during shutdown. Direct `overseer.Publish(...)` bypasses the gate. Bus is no-op-on-closed (returns false), so it's safe.
- **Lookup registry inside the recover for identity.** Synthetic SessionFailed must carry agent/project from `d.agents.LookupByContainerID` — empty fields strand downstream metrics indexed by (project, agent) at exactly the failure mode that matters most.
- **Reset `currentIdx = -1` after each successful step.** Without reset, a panic between InitStepCompleted and the next iteration's reassignment publishes a misleading InitStepFailed for the just-completed step. The `-1` sentinel means "panic between steps — only InitFailed".
- **Test seams as package-level function vars.** `agentReadyOpener` (default `os.OpenFile`) and `stageReaperPanicHookForTest` (nil in production, fires between c.Wait and finalStageErrCh) are minimal injection points. The generic `withVar[T any](t, &target, v)` helper centralizes the prev-restore dance for any seam.
- **Code-simplifier introduced `withVar` generic** — replaces three duplicate `prev := X; X = v; t.Cleanup(func() { X = prev })` patterns. Use it for new test seams.
- **`stageExitResponse` nil-guard on `c.ProcessState` is load-bearing for recover callers.** A future refactor that drops the nil-check would create a recursive-panic source from the reaper recover path.
- **Pre-commit `golangci-lint-full` reformatted whitespace** — re-run pre-commit after first failure to apply formatting.
- **`make test` is flaky on `TestStartShellCommand_InitialStdinCloseStdinRace`** (timing-sensitive race-detector test, pre-existing). Re-run to confirm transient.

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents to review this task's changes, then fix any and all findings
5. Commit all changes from this task with a descriptive commit message
6. Present the handoff prompt from the task's Wrap Up section to the user
7. Wait for the user to start a new conversation with the handoff prompt

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Background

PR #271 (`feat/agent-cp-init`) moved container init from `entrypoint.sh` into a CP-driven dispatch loop over `ClawkerdService.Session`. ~2337 additions across 19 files. New `Executor` in `internal/controlplane/agent/init.go` runs a 7-step plan; new `Init` substruct on `overseer.Agent`; new typed init events; `AgentReady` RPC.

5-agent review found 3 Critical, 7 Important, 9 Suggestion-level findings. The Critical findings are direct violations of the **CP-crash-is-a-security-incident invariant** (root `CLAUDE.md`): a panic on the CP boot/serve path strands eBPF programs unsupervised, breaking the firewall enforcement boundary the user trusts to be intact.

### Key Files

- `internal/controlplane/agent/init.go` — Executor, plan, runStep, classifyErrorCode (NEW, 578 lines)
- `internal/controlplane/agent/init_test.go` (NEW, 593 lines)
- `internal/controlplane/agent/dialer.go` — DialAgent goroutine, runDial, runInit, dispatchAgentEvents
- `internal/controlplane/agent/events_agent.go` — typed init events + ApplyTo projections
- `internal/controlplane/overseer/state.go` — `Init` substruct, `InitFailureReason`, `Trust` (sibling pattern to mirror)
- `cmd/clawker-cp/main.go` — wires NewExecutor (lines ~700-727), degrade pattern
- `cmd/clawkerd/session.go` — handleAgentReady, stdinReady gate, stage reapers, runSender
- `cmd/clawkerd/session_test.go`
- `internal/bundler/assets/entrypoint.sh` — reduced from 340 → 50 lines
- `internal/consts/consts.go` — InitStepTimeout* constants
- `CLAUDE.md`, `internal/controlplane/CLAUDE.md`, `.claude/docs/DESIGN.md` — degrade-pattern docs with stale line numbers

### Design Patterns (project-specific)

- **CP rule:** every long-lived goroutine recovers; subsystem failures degrade, never cascade. Constructor returns `(nil, error)`; main logs structurally and sets the field to nil. Canonical templates: `agent.NewExecutor` degrade in `cmd/clawker-cp/main.go`, overseer stats heartbeat recover at `cmd/clawker-cp/main.go:592`.
- **Asymmetric trust:** CP-side dialer permissive; clawkerd-side listener strict. Init failure does NOT close the Session — CP must remain reachable for containment commands.
- **Trust idiom for invariants:** `overseer.Trust` (state.go ~lines 77-104) uses unexported fields + `Untrust(reason)` constructor making illegal states unrepresentable. Mirror this for `Init`.
- **Sealed sum via marker method:** Go idiom is unexported `isStep()` method on the interface; package-internal types implement it. Without it, the interface is "package-sealed" (anyone in `agent` package can implement) but not compile-time sealed.
- **Wire-contract tests:** `init_test.go` uses fake bidi stream + real overseer + real Executor, asserts every wire frame and worldview transition. Follow this pattern.

### Rules

- Read `CLAUDE.md`, `.claude/rules/code-style.md`, `.claude/rules/testing.md`, `internal/controlplane/CLAUDE.md`, and `internal/controlplane/agent/CLAUDE.md` before starting.
- Use Serena tools (`get_symbols_overview`, `find_symbol`, `find_referencing_symbols`) — read symbol bodies only when needed.
- All new code must compile; all tests pass under `-race`.
- **Do NOT run `go test ./...`** if `$CLAWKER_AGENT` is set (e2e tears down host CP). Use targeted `go test ./internal/controlplane/agent/...` etc.
- Mocks via `moq` `//go:generate` — never hand-edit. Regenerate with `go generate ./...` in the relevant package.
- File logging only via zerolog; user output via IOStreams (not relevant for this initiative — all internal code).
- TDD: write tests before code where the contract is clear.

---

## Task 1: Critical recover hardening + degrade-path test

**Creates/modifies:**
- `internal/controlplane/agent/dialer.go` (DialAgent goroutine recover)
- `cmd/clawkerd/session.go` (handleAgentReady recover; stage-reaper recovers)
- `cmd/clawker-cp/main.go` (extract `wireInitExecutor` for testability)
- `cmd/clawker-cp/main_test.go` (NEW or extend)
- `internal/controlplane/agent/dialer_test.go` (DialAgent panic test)
- `cmd/clawkerd/session_test.go` (handleAgentReady + reaper panic tests)

**Depends on:** none — start here.

### Findings addressed
- **Critical 1** — `DialAgent` goroutine no `recover()` (`dialer.go:213-220`). Panic in `runDial → runInit → Executor.Run` crashes CP, strands eBPF.
- **Critical 2** — `handleAgentReady` no `recover()` (`session.go:214-287`). Asymmetric with `handleRegisterRequired` at `:162-190`.
- **Critical 3** — Stage reaper goroutines no `recover()` (`session.go:776-789`). Panic in `s.send(...)` before channel send → worker deadlocks at `:817`, init plan halts.
- **Important 4** — `cmd/clawker-cp/main.go:707-714` degrade path untested. Regression that converts `if err != nil { initExec = nil }` back to `panic(err)` ships green.

### Implementation Phase

#### Step 1.1 — DialAgent goroutine recover (`dialer.go`)

Inside `DialAgent` (around line 213), the `go func()` that calls `d.runDial(ctx, containerID)` needs a panic recover. Existing defer for the dedup-cleanup must run AFTER recover (so put `recover` defer last → it executes first on unwind).

Required behavior on panic:
1. `d.log.Error().Interface("panic", r).Bytes("stack", debug.Stack()).Str("container_id", containerID).Str("event", "agentdial_runDial_panic").Msg("agent.dial: runDial panicked; abandoning this container's dial cycle, CP otherwise unaffected")`
2. Publish synthetic `SessionFailed` event with `Reason: SessionFailureReasonInternal` (or closest existing) and `Detail: "dial goroutine panicked"`. Worldview consumers must see a terminal outcome — without this they'd see "Connecting" forever.
3. Also publish synthetic `InitFailed` if an `InitStarted` was already published (best-effort). Track this with a small flag inside `runDial` accessible to the recover, OR have `runInit` set a `view.Init.Status == Running` check via overseer Snapshot before publishing InitFailed.
4. Dedup-cleanup defer fires regardless.

Pattern reference: `cmd/clawker-cp/main.go:592` (overseer stats heartbeat).

#### Step 1.2 — `handleAgentReady` recover (`session.go`)

Mirror `handleRegisterRequired` (lines ~162-190). On panic:
- `s.log.Error().Interface("panic", r).Bytes("stack", debug.Stack()).Msg("agent_ready handler panicked")`
- Send `Error{Code: ERROR_CODE_IO_ERROR, Detail: "agent_ready: panic: <fmt.Sprint(r)>"}` so CP sees a terminal Response.
- Recover before the deferred `f.Close()` if the file was opened — the file handle leaks otherwise.

#### Step 1.3 — Stage reaper recovers (`session.go:776-789`)

Each reaper goroutine wraps body in `defer func(){ if r := recover(); r != nil { ... } }()`. On panic:
- Log at Error level with stack.
- For `isFinal == true`: ALWAYS send a sentinel error to `finalStageErrCh` (which is buffered cap 1) so the worker at `:817` unblocks. Sentinel: `fmt.Errorf("stage reaper panicked: %v", r)`.
- For non-final: emit a synthetic `StageExit{Code: -1, Signal: "panic"}` Response so CP sees a terminal stage outcome.
- `reapWG.Done()` runs via the existing outer defer.

Same pattern for the drain goroutines around the same area if they touch the channel.

#### Step 1.4 — Extract `wireInitExecutor` for testability (`cmd/clawker-cp/main.go`)

Current code at ~`:707-714`:
```go
initExec, err := agent.NewExecutor(bus, log.With("component", "agent.init"))
if err != nil {
    log.Error().Err(err).
        Str("event", "agent_init_executor_unavailable").
        Msg("agent.init: Executor construction failed; CP-driven init disabled — ...")
    initExec = nil
}
```

Extract to:
```go
func wireInitExecutor(bus overseer.Bus, log *logger.Logger) *agent.Executor {
    initExec, err := agent.NewExecutor(bus, log.With("component", "agent.init"))
    if err != nil {
        log.Error().Err(err).
            Str("event", "agent_init_executor_unavailable").
            Msg("agent.init: Executor construction failed; CP-driven init disabled; AdminService/firewall/registry continue.")
        return nil
    }
    return initExec
}
```

Call site stays one-liner.

#### Step 1.5 — Tests

In `cmd/clawker-cp/main_test.go` (create if absent):
- `TestWireInitExecutor_NilBusReturnsNil` — pass nil bus, assert returns nil and log captures `event=agent_init_executor_unavailable` line.
- `TestWireInitExecutor_HappyPath` — pass real bus, assert returns non-nil.

Use `iostreams.Test()` only if main_test.go uses iostreams; otherwise pass `logger.Nop()` and capture via `zerolog.New(buf)`.

In `internal/controlplane/agent/dialer_test.go`:
- `TestDialAgent_PanicInRunDial_DoesNotCrashCP_PublishesTerminal` — inject a fake dependency (e.g. an `Executor` that panics on Run, or a registry that panics on lookup) and assert: `recover` log line emitted, terminal `SessionFailed` published to bus, dedup map cleaned up.
- May require a `runDialFn` injection point on the Dialer struct (replace `d.runDial` with a closure field defaulting to the method value). Acceptable refactor.

In `cmd/clawkerd/session_test.go`:
- `TestHandleAgentReady_Panic_RepliesIOError` — inject a fault that causes the handler to panic (e.g. `s.send` field replaced with a function that panics on first call) and assert: terminal Error{IO_ERROR} response sent, no goroutine leak.
- `TestRunShellCommand_FinalStageReaperPanic_DoesNotDeadlock` — similar injection at the `s.send` boundary inside the reaper. Assert: worker at line ~817 returns within 100ms, terminal Done/Error fires.

### Acceptance Criteria

```bash
# Build clean
go build ./...

# Targeted tests pass under -race
go test -race ./internal/controlplane/agent/... -run 'Dial|Executor' -count=1
go test -race ./cmd/clawkerd/... -run 'AgentReady|Reaper|ShellCommand' -count=1
go test -race ./cmd/clawker-cp/... -run 'WireInitExecutor' -count=1

# Make test (no Docker)
make test

# Pre-commit (lint, semgrep, govulncheck, doc freshness)
make pre-commit
```

Manual verification:
- `grep -n 'recover()' internal/controlplane/agent/dialer.go cmd/clawkerd/session.go` shows recovers at the new sites.
- `grep -n 'wireInitExecutor' cmd/clawker-cp/` shows extracted function.

### Wrap Up

1. Update Progress Tracker: Task 1 → `complete`
2. Append key learnings (any non-obvious refactor like injection points)
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents on the diff. Fix all findings.
4. Commit all changes. Suggested message: `fix(cp,clawkerd): recover dial/agent-ready/reaper goroutines; test main.go init-executor degrade`
5. **STOP.** Inform user. Present handoff:

> **Next agent prompt:** "Continue the cp-init-pr271-fixes initiative. Read the Serena memory `cp-init-pr271-fixes` — Task 1 is complete. Begin Task 2: Type-design — `Init` substruct + `step` seal + `runStep` tuple."

---

## Task 2: Type-design — `Init` substruct + `step` seal + `runStep` tuple

**Creates/modifies:**
- `internal/controlplane/overseer/state.go` (`Init` substruct → `Trust` idiom; possibly `InitStatus` accessor changes)
- `internal/controlplane/agent/events_agent.go` (every `ApplyTo` for Init events updated to use new constructors)
- `internal/controlplane/agent/init.go` (`step` interface gets `isStep()` marker; `runStep` returns named struct)
- `internal/controlplane/agent/init_test.go` (worldview projection tests + new exhaustiveness test)
- `internal/controlplane/overseer/state_test.go` if exists (Init constructor tests)

**Depends on:** Task 1 complete (no shared edits but reduces churn).

### Findings addressed
- **Important 9** — `Init` substruct: `Trust`-style enforcement missing. Permits `Status=Completed ∧ LastError != ""`, `Status=Failed ∧ LastError == ""`, `CompletedAt < StartedAt`, `StepIndex >= StepCount`, out-of-order step events corrupting projection.
- **Important 10** — `step` sealed sum not actually sealed. Add `isStep()` marker.
- **Suggestion 11** — `runStep` 4-tuple `(exit, reason, detail, err)` → named struct `stepOutcome` with `Failed() bool`.
- **Suggestion 12** — `Run` owns `Recv` invariant doc-only. Add atomic `inUse` flag with error return on violation.

### Implementation Phase

#### Step 2.1 — `Init` follows `Trust` idiom

Read `overseer.Trust` (`state.go` ~lines 77-104) for the exact pattern. Apply to `Init`:
- All fields unexported: `status InitStatus`, `stepName string`, `stepIndex int`, `stepCount int`, `startedAt time.Time`, `completedAt time.Time`, `lastError string`.
- Public accessors: `Status() InitStatus`, `StepName() string`, `StepIndex() int`, `StepCount() int`, `StartedAt() time.Time`, `CompletedAt() time.Time`, `LastError() string`.
- Lifecycle constructors:
  - `InitUnknown() Init` — zero value (or just rely on `Init{}`)
  - `InitRunning(stepCount int, at time.Time) Init`
  - `InitStepProgress(prev Init, stepName string, stepIndex int) Init` — used by InitStepStarted ApplyTo
  - `InitCompleted(prev Init, at time.Time) Init` — clears lastError, sets CompletedAt
  - `InitFailed(prev Init, at time.Time, reason InitFailureReason, detail string) Init` — sets Status, CompletedAt, lastError, but does not need `reason` if it's not stored on Init (verify; currently failure reason lives on the event, not Init substruct — keep that split)
- All constructors validate: panic on illegal arg combinations is fine *inside the producer*, since these are called only from `events_agent.go` ApplyTo. But better: clamp StepIndex into [0, StepCount-1] and reject invalid transitions silently (logging a warn) so a stale event in production cannot corrupt the projection.
- Update every `ApplyTo` site in `events_agent.go` (Init events: InitStarted, InitStepStarted, InitStepCompleted, InitStepFailed, InitCompleted, InitFailed) to use the new constructors. None of them should write fields directly.

JSON marshalling: if `Init` is serialised anywhere (overseer Snapshot → JSON for AdminService?), add explicit `MarshalJSON` to preserve wire compatibility. Check `find_referencing_symbols` for `Init{` struct literals outside the package.

#### Step 2.2 — `step` marker method

In `internal/controlplane/agent/init.go`:

```go
type step interface {
    stepName() string
    command(commandID string) (*clawkerdv1.Command, bool)
    isStep() // sealing marker — package-internal only
}

func (shellStep) isStep()      {}
func (agentReadyStep) isStep() {}
```

Document the contract in a comment above the interface: "Sealed sum: only `shellStep` and `agentReadyStep` implement. Adding a third kind requires updating `runStep` and `plan()` simultaneously."

#### Step 2.3 — `runStep` named outcome

```go
type stepOutcome struct {
    ExitCode int32
    Reason   overseer.InitFailureReason
    Detail   string
}

func (o stepOutcome) Failed() bool {
    return o.Reason != overseer.InitFailureReasonNone
}

// runStep returns (outcome, transportErr).
// transportErr means halt the dispatch loop — Session is dead.
// outcome.Failed() means halt the plan but Session lives.
// outcome zero value means step succeeded.
func (e *Executor) runStep(...) (stepOutcome, error) { ... }
```

Update synthesis logic in `Run` (around lines 266-284) to consume the struct. The `if err != nil` branch is still transport halt; the `out.Failed()` branch publishes `InitStepFailed` + `InitFailed`.

#### Step 2.4 — `Run` owns `Recv` runtime guard

Add `inUse atomic.Bool` field to `Executor`. At top of `Run`:
```go
if !e.inUse.CompareAndSwap(false, true) {
    return fmt.Errorf("executor: concurrent Run on the same instance — Run owns stream.Recv exclusively")
}
defer e.inUse.Store(false)
```

This is paranoid but cheap. Add a unit test `TestExecutor_Run_RejectsConcurrent` that fires two `Run` calls on the same Executor and asserts the second returns the "concurrent" error.

NOTE: If multiple Sessions share an Executor (per-CP not per-container), this guard is wrong — verify ownership model first. Reading `cmd/clawker-cp/main.go` and `dialer.go:runInit`: `initExec` is shared across all dialed agents but `Run` is called serially via `runDial`. The guard catches a misuse, not a normal case. Document this in the constructor comment.

#### Step 2.5 — Tests

- `internal/controlplane/overseer/state_test.go` — table-driven test for each Init constructor that asserts pre/post invariants (Status correct, LastError correctly cleared on Completed, CompletedAt > StartedAt, StepIndex < StepCount).
- `internal/controlplane/agent/init_test.go`:
  - Update existing worldview projection tests to use accessors (`view.Init.Status()` not `view.Init.Status`).
  - Add `TestExecutor_Run_RejectsConcurrent`.
  - Add `TestInitFailureReason_AllProduced` — exhaustiveness check that walks all defined reason constants and asserts each is reachable from `classifyErrorCode` or `Run` synthesis.
  - Add `TestExecutor_RunStep_OutcomeShape` if `stepOutcome` is exported; otherwise test through `Run`.

### Acceptance Criteria

```bash
go build ./...
go test -race ./internal/controlplane/... -count=1
make test
make pre-commit
```

Manual verification:
- `grep -rn 'view.Init\.\(Status\|StepName\|LastError\)' internal/` returns zero matches with old direct field access (caller migration done).
- `grep -n 'isStep' internal/controlplane/agent/init.go` shows the marker.
- `grep -n 'stepOutcome' internal/controlplane/agent/init.go` shows the struct.

### Wrap Up

1. Update Progress Tracker: Task 2 → `complete`
2. Append key learnings (esp. JSON marshalling, accessor migration pain points)
3. Run review subagents. Fix all findings.
4. Commit. Suggested: `refactor(overseer,agent): Init follows Trust idiom; seal step interface; named stepOutcome`
5. **STOP.** Handoff:

> **Next agent prompt:** "Continue the cp-init-pr271-fixes initiative. Read the Serena memory `cp-init-pr271-fixes` — Tasks 1-2 complete. Begin Task 3: Entrypoint loud-failure + timeout coupling."

---

## Task 3: Entrypoint loud-failure + timeout coupling

**Creates/modifies:**
- `internal/bundler/assets/entrypoint.sh` (restore guards; templatize timeout)
- `internal/bundler/` (the asset processor — add template rendering)
- `internal/consts/consts.go` (drop "currently 660 in entrypoint.sh" hedge comment, since drift is now closed)
- `internal/bundler/<entrypoint test>.go` (new or extend; assert rendered timeout matches consts)
- `.serena/memories/bug-tracker.md` (remove the entrypoint-timeout entry — fixed)

**Depends on:** Task 2 complete (no overlap, but sequential discipline).

### Findings addressed
- **Important 6** — Entrypoint lost loud-failure semantics. First-boot bootstrap failures hang 660s with no diagnostic.
- **Important 7** — Entrypoint 660s literal hand-coupled to `consts.InitStepTimeoutPostInitSeconds`. Templatize.

### Implementation Phase

#### Step 3.1 — Discover bundler asset pipeline

Read `internal/bundler/` to understand how `entrypoint.sh` is included in the image. If it's `go:embed`-ed as bytes, switch to `text/template` rendering at bundle time:

```go
//go:embed assets/entrypoint.sh.tmpl
var entrypointTemplate string

func renderEntrypoint() ([]byte, error) {
    t, err := template.New("entrypoint").Parse(entrypointTemplate)
    if err != nil { return nil, err }
    var buf bytes.Buffer
    err = t.Execute(&buf, struct {
        InitTimeoutSeconds int
    }{
        InitTimeoutSeconds: consts.InitStepTimeoutPostInitSeconds + 60, // slack
    })
    return buf.Bytes(), err
}
```

Rename `entrypoint.sh` → `entrypoint.sh.tmpl` and replace `660` with `{{ .InitTimeoutSeconds }}`.

If `go:embed` is the wrong abstraction (e.g. asset is post-processed by a content-hash function), find the existing rendering hook and slot in there.

#### Step 3.2 — Restore loud-failure guards

In the renamed `entrypoint.sh.tmpl`, after backgrounding clawkerd:

```bash
# Verify clawkerd binary present before backgrounding
if [ ! -x /usr/local/bin/clawkerd ]; then
    echo "[clawker] error component=clawkerd msg=binary missing or not executable" >&2
    exit 1
fi

/usr/local/bin/clawkerd &
clawkerd_pid=$!

# Sanity: still alive after 1 second (catches bootstrap-file / cert / port-bind failures)
sleep 1
if ! kill -0 "$clawkerd_pid" 2>/dev/null; then
    echo "[clawker] error component=clawkerd msg=daemon exited at startup pid=$clawkerd_pid" >&2
    echo "[clawker] check rotated logs at \$CLAWKER_LOGS_DIR/clawkerd.log for stack details" >&2
    exit 1
fi

# Now block on fifo with templated timeout
exec timeout {{ .InitTimeoutSeconds }}s cat /run/clawker/agent.fifo >/dev/null
```

Verify the `exec` form preserves PID 1 semantics.

#### Step 3.3 — Update consts comment

In `consts.go` ~line 267, replace `// currently 660 in entrypoint.sh` with a comment that reflects the new templated reality: `// Entrypoint script renders timeout = this value + 60s slack at bundle time.`

#### Step 3.4 — Bug tracker entry removal

Open `.serena/memories/bug-tracker.md`, find the entry about entrypoint hardcoding `660` / `text/template` rendering, remove it (since it's now fixed). Leave any unrelated entries.

#### Step 3.5 — Tests

In `internal/bundler/`:
- `TestRenderEntrypoint_TimeoutMatchesConsts` — render the template, grep for `timeout Ns`, parse N, assert `N == consts.InitStepTimeoutPostInitSeconds + 60`.
- `TestRenderEntrypoint_GuardsPresent` — render and assert presence of `kill -0 "$clawkerd_pid"` and `[ ! -x /usr/local/bin/clawkerd ]` lines.
- `TestRenderEntrypoint_Shellcheck` (optional, only if `shellcheck` is in the dev environment) — render to a temp file and run `shellcheck` over it.

### Acceptance Criteria

```bash
go build ./...
go test -race ./internal/bundler/... -count=1
make test
make pre-commit
```

Manual verification:
- Build a clawker image and inspect the rendered entrypoint: `docker run --rm <image> cat /usr/local/bin/clawker-entrypoint.sh | head -50` — verify `660` is now whatever consts says + 60.
- `grep -n 'kill -0' internal/bundler/assets/entrypoint.sh.tmpl` shows the guard.
- `grep -n '660' internal/bundler/assets/` returns zero matches.

### Wrap Up

1. Update Progress Tracker: Task 3 → `complete`
2. Append key learnings (esp. about bundler asset lifecycle if non-obvious)
3. Run review subagents. Fix findings.
4. Commit. Suggested: `fix(bundler,clawkerd): templatize entrypoint timeout from consts; restore startup guards`
5. **STOP.** Handoff:

> **Next agent prompt:** "Continue the cp-init-pr271-fixes initiative. Read the Serena memory `cp-init-pr271-fixes` — Tasks 1-3 complete. Begin Task 4: Test gaps + protocol fixes + doc/comment polish."

---

## Task 4: Test gaps + protocol fixes + doc/comment polish

**Creates/modifies:**
- `internal/controlplane/agent/init_test.go` (new tests: idempotency, ignore-and-continue Recv, StepName mid-run assertion)
- `internal/controlplane/agent/dialer.go` (Response_Error during register surfaced)
- `cmd/clawkerd/session.go` (runSender: terminal Responses dropped at Warn not Debug; EPIPE comment refinement; remove test-gap rationale comment)
- `internal/controlplane/agent/init.go` (move test-gap rationale comments out of source)
- `cmd/clawker-cp/main.go` (rename `agentdial_init_failed` → `agent_dialer_unavailable`; fix plan comment listing 5 of 7 steps)
- `CLAUDE.md` (replace stale main.go line refs with symbol names)
- `internal/controlplane/CLAUDE.md` (same)
- `.claude/docs/DESIGN.md` (same)

**Depends on:** Tasks 1-3 complete.

### Findings addressed
- **Important 5** — CLAUDE.md/DESIGN.md cite stale line numbers (`main.go:707-714`, `:718-721`). Replace with symbol names.
- **Important 8** — Plan idempotency / reconnect re-run untested.
- **Suggestion 13** — `dispatchAgentEvents` swallows non-RegisterDone errors during register (`dialer.go:1118-1131`). Surface as failure reason.
- **Suggestion 14** — `runSender` cancel after Send failure drops terminal Responses at Debug (`session.go:332-370`). Bump terminal Done/Error/RegisterDone to Warn.
- **Suggestion 15** — `EPIPE` in `handleAgentReady` ambiguous (`session.go:216-242`). Refine comment OR add marker file disambiguation. Pick refine-comment unless a fix is straightforward.
- **Suggestion 16** — `session.go:215-220` test-gap rationale comment belongs in test file.
- **Suggestion 17** — `main.go:701-704` plan comment lists 5 of 7 steps.
- **Suggestion 18** — `dialer = nil` log uses `agentdial_init_failed`, sibling at :707 uses `_unavailable`. Rename for grep consistency.
- **Suggestion 19** — Add `TestExecutor_Run_PlanIdempotent` + ignore-and-continue Recv path test + StepName mid-run assertion.

### Implementation Phase

#### Step 4.1 — Test additions

In `internal/controlplane/agent/init_test.go`:

- `TestExecutor_Run_PlanIdempotent` — drive `Run` twice on the same target with success on both cycles. Assert after each cycle: `view.Init.Status() == InitStatusCompleted`, `view.Init.LastError() == ""`. Use the same fake stream pattern existing tests use; reset between runs.

- `TestExecutor_RunStep_IgnoresUnknownAndMismatchedFrames` — push `Response_Started` (currently in the `continue` arm), then a frame with a different `command_id`, then real `Done{0}`. Assert `Run` returns nil. This pins the silent-no-op behavior for stray protocol noise.

- Mid-run StepName assertion in `TestExecutor_Run_StateProjection` (or new test) — at a known mid-run snapshot point, assert `view.Init.StepName() == "git"` (or whichever step is mid-flight). Locks the StepStarted ApplyTo.

#### Step 4.2 — Surface Response_Error during register

`dialer.go:1118-1131` (or wherever `driveRegister`'s recv-loop lives). Currently:

```go
if r.Payload.(*clawkerdv1.Response_RegisterDone) {
    ch <- ...
    return
}
// fallthrough: re-loop, ignoring everything else
```

Add a branch:
```go
if errPayload, ok := r.Payload.(*clawkerdv1.Response_Error); ok && r.CommandId == commandID {
    ch <- registerOutcome{ok: false, err: fmt.Errorf("clawkerd rejected RegisterRequired: %s: %s", errPayload.Error.Code, errPayload.Error.Detail)}
    return
}
```

Test: `TestDriveRegister_ResponseErrorSurfaces` — fake stream emits `Response_Error{INVALID_REQUEST}` with matching commandID, assert function returns < 1s with the error wrapped.

#### Step 4.3 — runSender Warn-on-terminal-drop

`session.go:332-370`. The code path that drops responses on context cancel currently logs at Debug. Differentiate by payload kind:

```go
if isTerminalPayload(resp) {
    s.log.Warn().Str("event","session_send_dropped_terminal").
        Str("payload", payloadKind(resp)).
        Str("command_id", resp.CommandId).
        Msg("terminal Response dropped after sender cancel — CP will see timeout instead of true outcome")
} else {
    s.log.Debug().Str("event","session_send_dropped_chunk")...
}
```

Where `isTerminalPayload` returns true for `Response_Done`, `Response_Error`, `Response_RegisterDone`.

Test: small unit test of `isTerminalPayload` covering each kind.

#### Step 4.4 — EPIPE comment refinement OR marker disambiguation

In `session.go:216-242`, the comment claims success on `EPIPE` is benign for reconnect-after-success. Refine:

```go
// EPIPE on write means the reader closed between open and write. For agent_ready
// this is the dominant case during CP reconnect after the entrypoint has already
// released — the byte is not needed because the entrypoint is no longer waiting.
// During FIRST init this would be a silent truncation, but the entrypoint blocks
// on the fifo for 660s in that scenario and a reader-close-during-write race is
// not reachable. Treat as Done{0}.
```

If you want to do better than a comment: touch a marker file (`/var/run/clawker/agent_ready_observed`) from the entrypoint after `cat` returns, and have `handleAgentReady` test for the marker on EPIPE → first-init = real failure, reconnect = Done{0}. Decide based on time budget.

#### Step 4.5 — Move test-gap rationale comments

`session.go:215-220` and `:243` — the in-source explanations of why write/close failure paths aren't unit-tested. Extract to:
- `cmd/clawkerd/session_test.go` — comment block above the closest related test explaining the deferred coverage.
- OR `.serena/memories/bug-tracker.md` — new entry.

Pick one location, not both.

Same treatment for any "Test gap:" comment block found in `init.go`.

#### Step 4.6 — `main.go:701-704` plan comment

Current comment lists 5 of 7 steps. Either remove enumeration entirely:
```go
// agent.NewExecutor: produces the per-CP Init plan dispatcher.
// Plan is defined in init.go and audited via TestExecutor_Plan_PrivilegeAndShape.
```

Or list all 7 in the same order as `plan()`. Prefer the first — DRY with the test.

#### Step 4.7 — `agentdial_init_failed` → `agent_dialer_unavailable`

`main.go` ~`:725-727`. Rename log event and message to match the `_unavailable` convention used by the sibling `agent_init_executor_unavailable` line.

```go
log.Error().Err(err).
    Str("event", "agent_dialer_unavailable").
    Msg("agent.dial: Dialer construction failed; CP→clawkerd command dispatch disabled; AdminService/firewall/registry continue.")
```

#### Step 4.8 — Stale line numbers in CLAUDE.md / DESIGN.md

Files:
- `CLAUDE.md` (root, ~line 63)
- `internal/controlplane/CLAUDE.md` (~lines 33-41)
- `.claude/docs/DESIGN.md` (~line 33)

Replace `cmd/clawker-cp/main.go:707-714` and `:718-721` line refs with symbol names like:
- "see `wireInitExecutor` in `cmd/clawker-cp/main.go` (the canonical degrade template)"
- "see the `agent.New(...)` dialer-degrade block in `cmd/clawker-cp/main.go`"

Also re-check `:592` reference (overseer stats heartbeat recover) — keep symbol-named: "see the overseer stats heartbeat recover in `cmd/clawker-cp/main.go`".

#### Step 4.9 — Run owns Recv comment update

If Task 2 added the runtime guard, update the `Run` doc to point at the guard rather than warning the reader to be careful.

### Acceptance Criteria

```bash
go build ./...
go test -race ./internal/controlplane/... ./cmd/clawkerd/... ./cmd/clawker-cp/... -count=1
make test
make pre-commit
# Doc freshness check (part of pre-commit but worth surfacing)
go run ./cmd/gen-docs --doc-path docs --markdown --website
```

Manual verification:
- `grep -n 'main.go:7[0-9]\{2\}' .claude/docs/DESIGN.md CLAUDE.md internal/controlplane/CLAUDE.md` returns zero matches.
- `grep -n 'agentdial_init_failed' cmd/` returns zero matches.
- `grep -n 'Test gap' cmd/clawkerd/session.go internal/controlplane/agent/init.go` returns zero matches (moved to test files).

### Wrap Up

1. Update Progress Tracker: Task 4 → `complete`
2. Append final key learnings
3. Run review subagents one last time. Fix findings.
4. Commit. Suggested: `polish(cp,clawkerd): test gaps, doc symbol-refs, log naming, register-error visibility`
5. Open or update PR #271 with a comment listing all four task commits and the corresponding finding numbers from the original review.
6. **STOP.** Inform user the initiative is complete; PR #271 ready for re-review.

---

## Original Findings Index (cross-reference)

For traceability against the 5-agent review:

- C1 → Task 1.1 (DialAgent recover)
- C2 → Task 1.2 (handleAgentReady recover)
- C3 → Task 1.3 (stage reaper recovers)
- I4 → Task 1.4-1.5 (wireInitExecutor + test)
- I5 → Task 4.8 (stale line numbers)
- I6 → Task 3.2 (entrypoint guards)
- I7 → Task 3.1 (templatize timeout)
- I8 → Task 4.1 (idempotency test)
- I9 → Task 2.1 (Init Trust idiom)
- I10 → Task 2.2 (step seal marker)
- S11 → Task 2.3 (stepOutcome struct)
- S12 → Task 2.4 (Run owns Recv guard)
- S13 → Task 4.2 (Response_Error during register)
- S14 → Task 4.3 (runSender Warn on terminal drop)
- S15 → Task 4.4 (EPIPE comment)
- S16 → Task 4.5 (move test-gap comments)
- S17 → Task 4.6 (plan comment 5/7)
- S18 → Task 4.7 (rename log event)
- S19 → Task 4.1 (ignore-and-continue test)

All 19 findings allocated. None deferred.
