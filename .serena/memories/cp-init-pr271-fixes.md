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
| Task 2: Type-design — `Init` substruct + `step` seal + `runStep` tuple | `complete` | claude-opus-4-7 |
| Task 3: Entrypoint loud-failure + timeout coupling | `complete` | claude-opus-4-7 |
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

### Task 2 (2026-05-08)

- **Trust idiom for value types: free constructor + methods, not method-only.** `InitRunning(stepCount, at)` is the entry point (no prev needed); `WithStep`/`WithStepError`/`Complete`/`Fail` are receiver methods on the previous value. Reads cleanly at call sites: `view.Init = view.Init.WithStep(name, idx)`. Free function for the entry transition mirrors `Untrust(reason)`; receiver methods for subsequent transitions avoid `func InitWithStep(prev Init, ...)` awkwardness. Don't try to do all-free-functions OR all-methods.
- **Drop magic-zero parameters from constructors.** First version of `WithStep` took `(name, idx, newStepCount int)` where `newStepCount > 0` overrode existing — type-design analyzer flagged this as the kind of "magic zero" the rest of the type avoids. Split: `WithStep(name, idx)` only; `StepCount` is captured once at `InitRunning` and not mutated mid-phase. Producer's `e.StepCount` field on `InitStepStarted` is dead-payload (kept for wire-schema streaming subscribers but ignored by ApplyTo).
- **Silent clamps are OK at the value-type layer when producers can't get a logger.** `overseer.Init` lives in a leaf package with no logger plumbing. Negative stepCount, out-of-range stepIndex, and `CompletedAt < StartedAt` all clamp silently. Defense-in-depth — production producer (init.go) iterates `range plan` so every input is in-range. Mirrors `Untrust(UntrustedReasonNone) → trusted Trust` precedent. Silent-failure-hunter flagged as MEDIUM but the Trust precedent answers it: emitting a structured log from a value method requires plumbing a logger; clamps are unreachable from the only producer in the tree.
- **`sync.Mutex.TryLock()` over `atomic.Bool` for "exclusive section" guards.** Type-design analyzer's recommendation: `atomic.Bool` invites `Load()` from tests (which the first version did, reaching into private state); `sync.Mutex` carries idiomatic semantics every reader recognizes, race detector understands it, defer pattern is identical. Test rewrites to subscribe to InitStarted on the bus to coordinate "first Run is past the gate" — pure observable behavior, no field-name coupling.
- **Constructor functions for typed values close doc-contract gaps.** `stepOutcome{Reason: ExitCode, ExitCode: 0}` was constructible (nonsense state). Adding `stepSucceeded()`, `stepFailedTransport(detail)`, `stepFailedExit(exit, detail)`, `stepFailedClassified(reason, detail)` makes Failed() true iff the constructor said so, enforcing the err/Reason pairing the doc-comment had previously claimed by convention.
- **Sealed sum via `isStep()` marker is package-external only.** Comment-analyzer flagged "forces audit" language as aspirational — in-package, a third type-switch arm is silent. Tighten to: "third implementer outside this package is rejected at compile time; package-internal additions still need a paired runStep / plan() update by convention."
- **Drop pure-getter docstrings.** `Status()`, `StepIndex()`, `StepCount()`, `StartedAt()`, `CompletedAt()` are self-evident. Keep docstrings only where the comment encodes something the name doesn't (e.g. `LastError` is distinct-from `Agent.LastError`; `StepName` is empty until `WithStep`).
- **Test-hunter delete list: 4 of 13 new tests were waste.** `TestInit_ZeroValue` (compiler-enforced), `TestInitRunning` (struct-literal-vs-getter tautology), `TestInit_TerminalIsSticky` (asserted unenforced behavior), `TestUntrust_EmptyReasonIsTrusted` (out of scope). Merged 3 `TestInit_WithStep*` variants into one table-driven test. Final state_test.go: 5 tests, each pinning a real branch (clamp / LastError clearing on Complete / CompletedAt floor / Fail field combo).
- **Elevate unknown-payload from Debug to Warn.** Pre-existing finding from silent-failure-hunter — the `runStep` switch `default:` at Debug means production never sees wire-vocabulary drift. Bump to Warn with command_id + step + payload_type fields. In-scope cleanup since the diff is in the area.
- **Pre-commit golangci-lint reformatted whitespace** (same as Task 1) — re-run after first failure.
- **LSP cache lag** — replacing `Init` struct shape made the LSP report ghost errors on accessor calls for many tool turns; `go build` is the source of truth, not LSP diagnostics. Don't be misled by stale LSP errors after a `replace_symbol_body`.
- **Package collision question for constructor naming.** `agent.InitCompleted` (event struct) vs hypothetical `overseer.InitCompleted` (constructor function) live in different packages — legal, but visually ambiguous in `events_agent.go`. Resolved by using verb-form method names (`Complete`/`Fail`) on the receiver instead of free functions named after the events.

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
- **Trust idiom for invariants:** `overseer.Trust` (state.go ~lines 77-104) uses unexported fields + `Untrust(reason)` constructor making illegal states unrepresentable. Mirror this for `Init` (Task 2 done) and any future invariant-bearing value type.
- **Sealed sum via marker method:** Go idiom is unexported `isStep()` method on the interface; package-internal types implement it. Without it, the interface is "package-sealed" (anyone in `agent` package can implement) but not compile-time sealed. (Task 2 done.)
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

(complete — see commit `dbc49aed`)

---

## Task 2: Type-design — `Init` substruct + `step` seal + `runStep` tuple

(complete — see Task 2 key learnings above)

**Final shape applied:**
- `overseer.Init`: unexported fields + accessors + `InitRunning(stepCount, at)` constructor + receiver methods `WithStep(name, idx)`, `WithStepError(detail)`, `Complete(at)`, `Fail(at, detail)`. CompletedAt floored at StartedAt; StepIndex clamped to `[0, StepCount-1]`; Complete clears LastError. `WithStep` does NOT take StepCount (initial design did with magic-zero override; dropped after type-design review).
- `agent.step`: `isStep()` marker method makes the interface sealed against external implementers.
- `agent.stepOutcome`: 3-field struct + `Failed()` predicate + four constructors `stepSucceeded()`, `stepFailedTransport(detail)`, `stepFailedExit(exit, detail)`, `stepFailedClassified(reason, detail)`. Closes the err/Reason coherence gap that a doc-comment-only invariant would leave open.
- `agent.Executor.runMu sync.Mutex`: `TryLock()`-guarded Run, returns descriptive error on concurrent reuse. (Initial design used `atomic.Bool`; switched to `sync.Mutex` after type-design review — race detector understands it idiomatically and tests can't reach into private state via `Load()`.)
- `internal/controlplane/agent/init.go` runStep `default:` switch arm: `Debug` → `Warn` (pre-existing fix in-scope).
- Tests: `state_test.go` is 5 tests pinning real branches (down from 13 in v1; deleted compiler-enforced and tautological tests after test-hunter audit). `init_test.go` adds `TestExecutor_Run_RejectsConcurrent` (uses bus subscription, no private-state probe) and `TestExecutor_Run_PlanIdempotent`.

---

## Task 3: Entrypoint loud-failure + timeout coupling

(complete — see commit on this branch)

### Task 3 Final Shape

- `internal/bundler/assets/entrypoint.sh` renamed to `entrypoint.sh.tmpl`. Hand-coupled `660`, `/run/clawker/agent.fifo`, `/var/run/clawker/ready` replaced with `{{ .InitTimeoutSeconds }}`, `{{ .AgentReadyFifo }}`, `{{ .ReadyMarkerPath }}`. Dirs derived via `filepath.Dir`.
- `internal/bundler/dockerfile.go` adds `mustRenderEntrypoint()` package-init render, replaces raw `EntrypointScript` go:embed with `var EntrypointScript = mustRenderEntrypoint()` (keeps the public string-typed API stable for the 3 existing call sites).
- `renderedAssetByName` map substitutes rendered output for `.tmpl` keys in `EmbeddedScripts()` so a `consts.InitStepTimeoutPostInitSeconds` bump correctly invalidates the image content hash.
- Bash ERR trap added to tag every uncaught nonzero exit with `[clawker] error component=entrypoint cmd=$BASH_COMMAND status=$?`. Closes silent-failure-hunter MEDIUM on the pre-launch setup commands (`mkdir -p`, `mkfifo`).
- Pre-launch + post-launch loud-failure guards restored: `[ ! -x /usr/local/bin/clawkerd ]` blocks before backgrounding; `kill -0 "${clawkerd_pid}"` after `sleep 1` catches deterministic startup failures (bad bootstrap material, cert chain, port-bind) before they hide behind the init timeout.
- `EmbeddedScripts()` `_ =`-discarded `fs.ReadDir` / `fs.ReadFile` errors converted to panics. Pre-existing pattern, but the new code added one more discard (silent-failure-hunter HIGH); fixed at the root in one stroke. Embed.FS read failures only happen on a corrupt binary — same loud-failure rationale as `mustRenderEntrypoint`.
- `consts.go` Init-phase ceiling block comment updated: drops "currently 660 in entrypoint.sh" hand-coupling hedge; reflects bundler-render single-source-of-truth.
- Bug-tracker memory entry removed (the entry called out both the fifo-path hardcode and the 660 timeout hardcode — both are addressed by templating).

### Task 3 Key Learnings (2026-05-08)

- **Init-time render via `var EntrypointScript = mustRenderEntrypoint()` keeps the public API as `string`.** Three callers were `[]byte(EntrypointScript)` — preserving the type means zero downstream churn. Alternative was a `RenderEntrypoint() ([]byte, error)` function, which would have required error plumbing at every call site for an error case that can only happen via a malformed compile-in template.
- **Package-init panic is safe per CP no-panic rule.** `cmd/clawker-cp` does import `internal/bundler` (verified: `go list -deps ./cmd/clawker-cp | grep bundler` → top-level package present). But the panic fires at Go's package-init phase, before `main()`, before any orchestrator code, before any eBPF program is loaded. The CP no-panic rule scopes to "post-SetReady code paths where eBPF would strand"; a binary that refuses to start is operationally identical to one that fails to compile. Documented this in the `EntrypointScript` doc comment so the next reader doesn't second-guess.
- **`renderedAssetByName` discriminator: init-rendered vs runtime-rendered.** `Dockerfile.tmpl` is also an `assets/*.tmpl`, but it takes per-call data (version/variant/OTEL config) and its raw template bytes ARE the right hash input. Only init-rendered (compile-time-only data) templates need `renderedAssetByName` entries. The drift test (`TestEntrypoint_AllTemplatesRegistered`) skips a `runtimeRendered` allowlist to enforce the convention without false positives.
- **Hash-input substitution closes a real cache-poisoning bug.** Without the map: bumping `consts.InitStepTimeoutPostInitSeconds` would change rendered `EntrypointScript` (so the image's actual entrypoint differs) but `EmbeddedScripts()` would still hash the unchanged raw template bytes — Docker would reuse the stale layer with the OLD timeout. Confirmed by the silent-failure-hunter pass.
- **Bash `set -e` plus ERR trap is idiomatic for tagged-stderr coverage.** Cheaper than wrapping each command in `|| { echo ...; exit 1; }`. The trap fires ONCE per uncaught nonzero exit, prints `${BASH_COMMAND}` + `$?`, then `set -e` exits. Explicit guards (`if [ ! -x ... ]`) still own their own bespoke diagnostic strings — operators can grep for either pattern.
- **`{{ .InitTimeoutSeconds }}` inside `${VAR:-default}` works.** Go's `text/template` tokenizes by `{{` `}}` only; trailing `}}}` parses as `{{ ... }}` plus literal `}`. Verified by render output (`CLAWKER_INIT_TIMEOUT:-660`). No template-engine escape needed.
- **Rendered output substitution in EmbeddedScripts() must come BEFORE the raw read.** First version tried to read raw + override; cleaner is a `if rendered, ok := ...; ok { append; continue }` short-circuit. Avoids reading the raw bytes only to discard them, and makes the intent obvious.
- **comment-analyzer caught a stranded "consts.X." sigil in bash.** First-version comment lead with `# consts.ReadyMarkerPath.` — useless to a bash reader, looked like an editing remnant. Code-simplifier already trimmed during its pass; pattern: don't reference Go symbols in bash comments unless they explain an invariant the bash code doesn't.
- **test-hunter approved all 4 tests.** Each pins a distinct contract: timeout math, guard literals, path templating + leak check, registration drift. The substring-match style is borderline-tautological in shape but the substrings ARE the operator-facing contract (the loud-failure stderr lines), not internal error wrapping — so they're load-bearing.
- **Linter formatting:** golangci-lint-full required two pre-commit passes on Tasks 1 + 2; this task ran clean on first invocation. Diff size and code-simplifier's pre-pass likely the difference.

---

## Task 4: Test gaps + protocol fixes + doc/comment polish

(see full task plan in `.serena/memories` history if needed — unchanged from initial draft. Note: Step 4.9 "Run owns Recv comment update" is already done in Task 2.)

---

## Original Findings Index (cross-reference)

For traceability against the 5-agent review:

- C1 → Task 1.1 (DialAgent recover) — ✓ done
- C2 → Task 1.2 (handleAgentReady recover) — ✓ done
- C3 → Task 1.3 (stage reaper recovers) — ✓ done
- I4 → Task 1.4-1.5 (wireInitExecutor + test) — ✓ done
- I5 → Task 4.8 (stale line numbers) — pending
- I6 → Task 3.2 (entrypoint guards) — ✓ done
- I7 → Task 3.1 (templatize timeout) — ✓ done
- I8 → Task 4.1 (idempotency test) — ✓ done as part of Task 2 (TestExecutor_Run_PlanIdempotent)
- I9 → Task 2.1 (Init Trust idiom) — ✓ done
- I10 → Task 2.2 (step seal marker) — ✓ done
- S11 → Task 2.3 (stepOutcome struct) — ✓ done
- S12 → Task 2.4 (Run owns Recv guard) — ✓ done (sync.Mutex.TryLock, not atomic.Bool)
- S13 → Task 4.2 (Response_Error during register) — pending
- S14 → Task 4.3 (runSender Warn on terminal drop) — pending
- S15 → Task 4.4 (EPIPE comment) — pending
- S16 → Task 4.5 (move test-gap comments) — pending
- S17 → Task 4.6 (plan comment 5/7) — pending
- S18 → Task 4.7 (rename log event) — pending
- S19 → Task 4.1 (ignore-and-continue test) — pending (NOT covered by Task 2 — Task 2 added idempotency only)

All 19 findings allocated. None deferred.
