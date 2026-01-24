# Agent Flag Bool Refactor - Session 2

**Branch:** `a/run-fixes`
**Last Updated:** 2025-01-22
**Status:** ✅ COMPLETE - All code refactoring and test fixes done, unit tests passing

## End Goal

Refactor all container commands to:
1. Change `--agent` flag from `string` (agent name) to `bool` (treat positional args as agent names)
2. Use `cmd.Context()` instead of creating `context.Background()` internally

## Completed Code Refactoring

All 8 command files have been refactored:

| Command | Status | Notes |
|---------|--------|-------|
| `kill.go` | ✅ Done | Agent bool, cmd.Context() |
| `logs.go` | ✅ Done | Agent bool, cmd.Context(), uses `cobra.ExactArgs(1)` |
| `remove.go` | ✅ Done | Agent bool, cmd.Context() |
| `pause.go` | ✅ Done | Agent bool, cmd.Context() |
| `unpause.go` | ✅ Done | Agent bool, cmd.Context() |
| `top.go` | ✅ Done | Agent bool, cmd.Context(), special ps args handling |
| `cp.go` | ✅ Done | Agent bool, cmd.Context(), **Option B chosen: dropped `:PATH` syntax** |
| `list.go` | ✅ Done | cmd.Context() only (no Agent flag) |

## cp.go Design Decision

User chose **Option B**: Make `--agent` boolean, drop `:PATH` syntax.

New behavior:
- `--agent` is boolean flag
- When set, container names in `CONTAINER:PATH` are resolved as agent names
- `:PATH` syntax (empty container name) no longer works - requires `name:PATH`

Example: `clawker cp --agent ralph:/app/config.json ./local`

## Test Fixes Remaining

Build passes. All tests now pass.

### Fixed:
- `kill_test.go` ✅ - Updated Agent to bool, added agent flag test case, fixed error message
- `logs_test.go` ✅ - Agent bool, error messages
- `pause_test.go` ✅ - Error message changed to `"requires at least 1 argument"`
- `remove_test.go` ✅ - Error message changed to `"requires at least 1 argument"`
- `unpause_test.go` ✅ - Error message changed to `"requires at least 1 argument"`
- `top_test.go` ✅ - Agent bool, error message, test expectations, GetBool
- `cp_test.go` ✅ - Agent string→bool, GetString→GetBool, removed `:PATH` syntax tests

### Test Fix Pattern

```go
// OLD
output: KillOptions{Agent: "ralph", Signal: "SIGKILL"},
cmdOpts.Agent, _ = cmd.Flags().GetString("agent")
wantErrMsg: "requires at least 1 container argument or --agent flag"

// NEW
output: KillOptions{Agent: true, Signal: "SIGKILL"},
cmdOpts.Agent, _ = cmd.Flags().GetBool("agent")
wantErrMsg: "requires at least 1 argument"
```

## TODO Sequence

- [x] Refactor kill.go
- [x] Refactor logs.go
- [x] Refactor remove.go
- [x] Refactor pause.go
- [x] Refactor unpause.go
- [x] Refactor top.go
- [x] Refactor cp.go (Option B)
- [x] Refactor list.go
- [x] Fix kill_test.go
- [x] Fix logs_test.go
- [x] Fix pause_test.go
- [x] Fix remove_test.go
- [x] Fix unpause_test.go
- [x] Fix top_test.go
- [x] Fix cp_test.go
- [x] Run `go test ./...` - verify all pass ✅
- [ ] Update memory `agent_flag_refactor_and_context_fixes.md` or delete it
- [ ] Run integration tests if unit tests pass

## Useful Commands

```bash
go build ./...
go test ./... 2>&1 | head -100
go test ./internal/cmd/container/pause/...
```

## Related Memory

See also: `agent_flag_refactor_and_context_fixes.md` (original memory from earlier session)

---

**IMPERATIVE:** Before proceeding with each TODO item, check with the user to confirm they want to continue. When all work is complete, ask the user if they want to delete this memory and the original memory.
