# Outstanding Loop Code Review Findings

All actionable items from the `a/loop` PR review have been addressed.

## Completed (2026-02-13)

### Medium Priority — All Fixed

1. **History recording: repeated_error event** — Added `"repeated_error"` history entry after 3+ consecutive same-error loops in `loop.go`. Makes failures visible in `loop status` before circuit trips.
2. **sendEvent: log event kind as string** — Added `String()` method on `LoopDashEventKind` in `loopdash.go`. Updated `sendEvent` in `dashboard.go` to use `.Str("event_kind", ev.Kind.String())`.
3. **ResolveTasksPrompt: validate empty template** — Already implemented at `resolve.go:56-59` (no change needed).
4. **Double-close risk in ExecCapture** — Replaced `defer hijacked.Close()` + explicit close with `sync.Once`-guarded `closeConn()` in `loop.go`.
5. **ContentBlock typed accessors** — Deferred. Low priority, current flat struct works for internal serialization use case.
6. **HookConfig: add Validate() method** — Added `Validate() error` on `HookConfig` in `hooks.go`. Called in `ResolveHooks` for user-provided hooks files.
7. **CircuitBreaker doc: safety completion trip** — Updated `CircuitBreaker` struct doc and `UpdateWithAnalysis` godoc in `circuit.go` to list all trip conditions.
8. **CircuitBreaker: granular tests** — Added 5 edge case tests in `circuit_test.go`: exactly-at-threshold, alternating progress/no-progress, same-error reset on different error, first-trip-condition-wins, safety completion alternating.

### Comment Improvements — All Fixed

- `stream.go` ParseStream godoc: Updated from "silently skipped" to describe debug-log/warn-log behavior.
- `hooks.go` stop-check.js catch block: Updated to reference specific failure modes.
- `dashboard.go` sendEvent godoc: Updated to mention event kind name in drop warning.

### Type Design Improvements — All Fixed

- `LoopDashEventKind.String()`: Added with all enum names + unknown fallback.
- `ResultEvent.Errors as []ErrorDetail`: Deferred — current `[]string` works.
- `Session.Update()`: Documented full contract (nil status, non-nil error behavior, counter updates).

## Deferred Items

- **ContentBlock typed accessors** (Finding #5): Low priority. Internal to stream parser.
- **ResultEvent.Errors as []ErrorDetail**: Would change serialization. Current `[]string` sufficient.

## Tests Added

- `TestLoopDashEventKind_String` — all enum values + unknown fallback
- `TestHookConfig_Validate_*` — 9 test cases (valid, empty event, invalid type, missing command/prompt, negative timeout, empty hooks, agent type, resolve integration)
- `TestCircuitBreaker_ExactlyAtThreshold` — verifies trip at exactly threshold count
- `TestCircuitBreaker_AlternatingProgressNoProgress` — 10 cycles, never trips
- `TestCircuitBreaker_SameErrorResetOnDifferentError` — interleaved errors reset counter
- `TestCircuitBreaker_FirstTripConditionWins` — dual conditions, first wins
- `TestCircuitBreaker_SafetyCompletionAlternating` — 10 alternating cycles, never trips
- `TestRunnerRun_RepeatedErrorHistoryEntry` — verifies repeated_error in history after 3+ same-error loops
