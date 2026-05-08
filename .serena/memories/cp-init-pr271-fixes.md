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
| Task 4: Test gaps + protocol fixes + doc/comment polish | `complete` | claude-opus-4-7 |

## Initiative complete

All 19 findings landed across 4 commits on `feat/agent-cp-init`. PR #271 ready for re-review.

## Key Learnings

### Task 1 (2026-05-08)

(retained — see prior commit dbc49aed)

### Task 2 (2026-05-08)

(retained — see prior commit d64b9eda)

### Task 3 (2026-05-08)

(retained — see prior commit 9724b54f)

### Task 4 (2026-05-08)

- **Asymmetry of trust applies inside `driveRegister` too.** The new `Response_Error` short-circuit does NOT close the Session — it only resolves the register handshake's wait early with an error wrapped through `publishRegisterFailure`. Subscribers to `AgentRegistered{Ok:false}` and `AgentUntrusted` get the typed `ErrorCode + message` instead of an opaque "RegisterDone timeout"; the dialer keeps the channel up so CP can still dispatch containment.
- **3-way drop classifier vs 2-way predicate.** First version was `isTerminalPayload(*Response) bool`. silent-failure-hunter (correctly) flagged the silent-downgrade trap: a future proto variant returns false → drops at Debug → operator triaging "RegisterDone timeout" loses the breadcrumb. Refactored to `classifyDropPayload(*Response) payloadClass` with `payloadClassUnknown` as the default arm; `send()` logs `event=session_send_dropped_unknown` at Warn for drift. Test covers nil + unset oneof + every current variant. Generalizes the pattern: when a switch's default behavior matters for operator visibility, return an enum, not a bool — bool collapses "unknown" into one of the named buckets.
- **Pin the in-flight `StepName` after failure.** `WithStepError` and `Fail` both preserve `StepName` from the prior `WithStep` — without a test, a future projection refactor that zeros it on terminal could ship and only surface as "init failed" UX with no step name. The assertion uses `expectedInitStepNames[1]` (derived from production plan) rather than a literal — locks the `(name, idx)` pairing through ApplyTo.
- **"Test gap:" prose belongs in `bug-tracker.md`, not source.** Removed the pre-existing rationale block from `handleAgentReady` (write/close failure paths). Migration condition baked into the bug-tracker entry: "revisit if `agentReadyWriter` / `agentReadyCloser` seams are added for other reasons" — so a future refactor that adds the seams brings the test coverage with it.
- **Symbol-named refs > line numbers in CLAUDE.md/DESIGN.md.** Replaced 5 stale `cmd/clawker-cp/main.go:NNN` refs with `wireInitExecutor` / "the `agent.New(...)` block that degrades to `event=agent_dialer_unavailable`" / "search 'overseer stats heartbeat'". Symbols don't drift; line numbers always do.
- **Log-event grep convention `_unavailable`.** Renamed `agentdial_init_failed` → `agent_dialer_unavailable` (matches sibling `agent_init_executor_unavailable`). Operators search structured logs by the suffix; uniform suffix is a load-bearing convention now documented in root `CLAUDE.md` rule 4.
- **Plan comments point at tests, not at step lists.** Earlier draft enumerated 5 of 7 plan steps in a comment block — list rotted as `plan()` evolved. New comment pins the contract via `agent.Executor.plan()` + `TestExecutor_Plan_PrivilegeAndShape` so drift is caught by the test suite, not by humans noticing comment skew.
- **Subagent reviews ran clean except for the silent-failure 3-way classifier finding** — which became the most valuable change in the task. Run silent-failure-hunter on any predicate that gates log severity; default-arm trap is its specialty.
- **`require` over `assert` for the gate of dependent assertions.** dispatch_register_test's `Ok==false` is the precondition for the `Reason` Contains assertions; `assert.False` would let downstream `Contains` fire noisy secondary failures on a regression. Project convention: first check in a causal chain is `require`.

---

## Context Window Management

(applies to in-progress tasks; this initiative is complete)

---

## Context for All Agents

(retained for historical context — see prior commits)

### Background

PR #271 (`feat/agent-cp-init`) moved container init from `entrypoint.sh` into a CP-driven dispatch loop over `ClawkerdService.Session`. ~2337 additions across 19 files. New `Executor` in `internal/controlplane/agent/init.go` runs a 7-step plan; new `Init` substruct on `overseer.Agent`; new typed init events; `AgentReady` RPC.

5-agent review found 3 Critical, 7 Important, 9 Suggestion-level findings.

---

## Original Findings Index (cross-reference)

For traceability against the 5-agent review:

- C1 → Task 1.1 (DialAgent recover) — ✓ done
- C2 → Task 1.2 (handleAgentReady recover) — ✓ done
- C3 → Task 1.3 (stage reaper recovers) — ✓ done
- I4 → Task 1.4-1.5 (wireInitExecutor + test) — ✓ done
- I5 → Task 4.8 (stale line numbers) — ✓ done
- I6 → Task 3.2 (entrypoint guards) — ✓ done
- I7 → Task 3.1 (templatize timeout) — ✓ done
- I8 → Task 4.1 (idempotency test) — ✓ done as part of Task 2
- I9 → Task 2.1 (Init Trust idiom) — ✓ done
- I10 → Task 2.2 (step seal marker) — ✓ done
- S11 → Task 2.3 (stepOutcome struct) — ✓ done
- S12 → Task 2.4 (Run owns Recv guard) — ✓ done
- S13 → Task 4.2 (Response_Error during register) — ✓ done
- S14 → Task 4.3 (runSender Warn on terminal drop) — ✓ done (with 3-way classifier)
- S15 → Task 4.4 (EPIPE comment) — ✓ done
- S16 → Task 4.5 (move test-gap comments) — ✓ done
- S17 → Task 4.6 (plan comment 5/7) — ✓ done
- S18 → Task 4.7 (rename log event) — ✓ done
- S19 → Task 4.1 (ignore-and-continue test) — ✓ done

All 19 findings allocated. None deferred.
