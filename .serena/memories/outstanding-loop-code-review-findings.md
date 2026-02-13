# Outstanding Loop Code Review Findings

Remaining suggestions and medium-severity items from the `a/loop` PR review.
These are not critical/important but should be addressed in future work.

## Medium Priority

### 1. History recording: surface after N consecutive failures
- **File**: `internal/loop/loop.go` (main loop body)
- **Issue**: When the same error occurs N times in a row, history entries just accumulate identical "updated" rows. Consider adding a distinct "repeated_error" event after consecutive failures so `loop status` can surface it prominently.

### 2. sendEvent: log event kind as string, not raw int
- **File**: `internal/cmd/loop/shared/dashboard.go:256`
- **Issue**: `logger.Warn().Int("event_kind", int(ev.Kind))` logs the enum as a raw integer. When reading logs, `3` is meaningless. Add a `String()` method to `LoopDashEventKind` or use a switch to log the name.

### 3. ResolveTasksPrompt: validate empty template
- **File**: `internal/cmd/loop/shared/resolve.go`
- **Issue**: If `tasksFile` reads successfully but is empty (0 bytes), the default template wraps an empty string in `<tasks></tasks>`, sending a meaningless prompt. Add an early check: `if strings.TrimSpace(tasksContent) == "" { return "", fmt.Errorf("tasks file is empty") }`.

### 4. Double-close risk in ExecCapture
- **File**: `internal/loop/loop.go:556-584`
- **Issue**: `defer hijacked.Close()` at line 556, but the `<-ctx.Done()` branch at line 582 also calls `hijacked.Close()`. The Docker hijacked connection's `Close()` is idempotent in practice (calls `net.Conn.Close`), but this is fragile. Consider using `sync.Once` or removing the defer in favor of explicit close in both paths.

### 5. ContentBlock: anemic union type
- **File**: `internal/loop/stream.go:112-131`
- **Issue**: `ContentBlock` is a flat struct with all fields for all variants. This works but provides no compile-time enforcement. Consider adding typed accessor methods like `AsToolUse() (*ToolUseBlock, bool)` that return nil/false for wrong variants. Low priority — the current approach works fine for the serialization use case.

### 6. HookConfig: add Validate() method
- **File**: `internal/loop/hooks.go:49`
- **Issue**: `HookConfig` is a `map[string][]HookMatcherGroup` with no validation. A `Validate()` method could check: non-empty event names, valid handler types, non-empty commands for command handlers, reasonable timeouts. This would catch malformed `--hooks-file` input earlier.

### 7. CircuitBreaker doc comment: document safety completion trip
- **File**: `internal/loop/circuit.go`
- **Issue**: The circuit breaker's `Update`/`UpdateWithAnalysis` godoc doesn't mention the safety completion trip condition (N consecutive loops with completion indicators but no EXIT_SIGNAL). Add this to the doc comment so callers understand all trip conditions.

### 8. CircuitBreaker UpdateWithAnalysis: more granular tests
- **File**: `internal/loop/circuit_test.go`
- **Issue**: Tests cover the main paths but could benefit from edge case coverage: exactly-at-threshold trips, alternating progress/no-progress sequences, interaction between multiple trip conditions firing simultaneously.

## Comment Improvements

- `stream.go:246-248`: The `ParseStream` godoc says "silently skipped" for malformed lines — update to reflect the new `logger.Debug` logging.
- `hooks.go:218-221`: The catch block comment `"JSON parse error or unexpected issue"` should be updated to `"Unexpected error — logged to stderr"`.
- `dashboard.go:249-252`: The `sendEvent` godoc should mention that dropped events are logged with the event kind.

## Type Design Improvements

- `LoopDashEventKind`: Consider adding a `String()` method for debug/log readability.
- `ResultEvent`: The `Errors []string` field could be `[]ErrorDetail` with structured info (code, source, message) for richer error reporting.
- `Session.Update()`: Method takes `(*Status, error)` but doesn't validate nil Status + non-nil error consistency. Document the contract.

## Created
2026-02-13 — from PR review of `a/loop` branch
