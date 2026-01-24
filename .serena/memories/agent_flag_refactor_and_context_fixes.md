# Agent Flag Refactor and cmd.Context() Migration

**Branch:** `a/run-fixes`
**Last Updated:** 2025-01-22
**Status:** In Progress - Test Fixes Complete, Need Command Refactoring

## End Goal

Refactor all container commands to:
1. Use `cmd.Context()` instead of creating `context.Background()` internally
2. Change `--agent` flag from `string` (agent name) to `bool` (treat first positional arg as agent name)

## Background Context

The original design had `--agent <name>` as a string flag that bypassed positional argument requirements. This was a bad design choice. The new pattern is:
- `--agent` is a boolean flag
- When `--agent` is set, the first positional argument is treated as an agent name and resolved to `clawker.<project>.<agent>`
- Without `--agent`, the first positional argument is the full container name

## Commands Analysis

### Already Refactored (Agent bool + cmd.Context())
- `exec.go` ✅
- `stats.go` ✅  
- `stop.go` ✅
- `attach.go` ✅
- `create.go` ✅
- `inspect.go` ✅
- `rename.go` ✅
- `restart.go` ✅
- `run.go` ✅
- `start.go` ✅
- `update.go` ✅
- `wait.go` ✅

### Still Need Refactoring (Agent string + context.Background())
| Command | Agent Field | Context | Notes |
|---------|-------------|---------|-------|
| `kill.go` | `Agent string` | `ctx := context.Background()` | Needs both changes |
| `logs.go` | `Agent string` | `ctx := context.Background()` | Needs both changes |
| `remove.go` | `Agent string` | `ctx := context.Background()` | Needs both changes |
| `pause.go` | `Agent string` | `ctx := context.Background()` | Needs both changes |
| `cp.go` | `Agent string` | `ctx := context.Background()` | Needs both changes |
| `top.go` | `Agent string` | `ctx := context.Background()` | Needs both changes |
| `unpause.go` | `Agent string` | `ctx := context.Background()` | Needs both changes |
| `list.go` | No Agent | `ctx := context.Background()` | Only needs context change |

## Test Files Status

### All Fixed ✅
- `exec_test.go` ✅ - Changed `Agent: "ralph"` to `Agent: true`, `GetString("agent")` to `GetBool("agent")`
- `stats_test.go` ✅ - Same pattern, removed mutual exclusivity test
- `stop_test.go` ✅ - Changed `GetString("agent")` to `GetBool("agent")`
- `attach_test.go` ✅ - Updated Use string, fixed error message format (use Contains)
- `create_test.go` ✅ - Updated Use string, removed "no image (optional)" test case
- `inspect_test.go` ✅ - Updated Use string
- `rename_test.go` ✅ - Changed agent to bool, simplified test cases, updated Use string
- `restart_test.go` ✅ - Updated error message format (use Contains)

## Key Patterns for Test Fixes

### 1. Agent Flag Changes
```go
// OLD
output: StopOptions{Agent: "ralph", Timeout: 10},
cmdOpts.Agent, _ = cmd.Flags().GetString("agent")

// NEW  
output: StopOptions{Agent: true, Timeout: 10},
cmdOpts.Agent, _ = cmd.Flags().GetBool("agent")
```

### 2. Error Message Format Changes
```go
// OLD (custom validation)
wantErrMsg: "requires at least 1 container argument or --agent flag"

// NEW (cmdutil.RequiresMinArgs format)
wantErrMsg: "stop: 'stop' requires at least 1 argument\n\nUsage:  stop [CONTAINER...] [flags]\n\nSee 'stop --help' for more information"
```

### 3. Test Input Changes
```go
// OLD - agent name as flag value
input:  "--agent ralph",
args:   []string{},

// NEW - agent is boolean, name is positional arg
input:  "--agent",
args:   []string{"ralph"},
```

### 4. Removed Test Cases
- "agent and container mutually exclusive" tests should be removed (no longer applies)

## TODO Sequence

- [x] Identify all commands needing refactoring
- [x] Fix stop_test.go - change `GetString` to `GetBool`
- [x] Fix attach_test.go - Use string, error message format
- [x] Fix create_test.go - Use string, remove optional image test
- [x] Fix inspect_test.go - Use string
- [x] Fix rename_test.go - Agent bool, simplify tests, Use string
- [x] Fix restart_test.go - Error message format
- [x] Run full test suite `go test ./...` - ALL PASS ✅
- [ ] **NEXT** Refactor kill.go, logs.go, remove.go, pause.go, cp.go, top.go, unpause.go, list.go
- [ ] Run integration tests `go test -tags=integration ./internal/cmd/... -v -timeout 10m`

## Useful Commands

```bash
# Check what's failing
go test ./... 2>&1 | head -100

# Test specific package
go test -c ./internal/cmd/container/stop/

# Run all tests
go test ./...
```

---

**IMPERATIVE:** Before proceeding with each TODO item, check with the user to confirm they want to continue. When all work is complete, ask the user if they want to delete this memory.
