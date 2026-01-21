# Ralph Script Debugging Session - 2026-01-20

## Summary
Debugging and fixing the ralph-loop.sh autonomous agent script.

## Fixes Applied

### 1. `clawker container ls --format` doesn't exist
**File:** `scripts/ralph/ralph-loop.sh`
**Fix:** Changed to use `clawker container inspect --agent NAME --format '{{.State.Status}}'` which does support `--format`.

**Implementation:** Added `--format` flag to both commands:

**`pkg/cmd/container/inspect/inspect.go`:**
- Added `text/template` and `github.com/moby/moby/client` imports
- Changed results slice to `[]client.ContainerInspectResult` for type safety
- Implemented `outputFormatted()` that executes templates against `.Container` (InspectResponse) for Docker CLI compatibility
- Templates like `{{.State.Status}}` now work

**`pkg/cmd/container/list/list.go`:**
- Added `Format string` to `ListOptions`
- Added `--format` flag
- Created `containerForFormat` wrapper with `.Names` alias for Docker CLI compat
- Implemented `outputFormatted()` for templated output

**`.claude/docs/CLI-VERBS.md`:**
- Added `--format` to Flag Conventions
- Added "The `--format` Flag" section with fields and examples

**`pkg/cmd/container/list/list_test.go`:**
- Added test cases for `--format` flag

### 2. Script path references after move
**Files:** All scripts in `scripts/ralph/`
**Fix:** Updated all references from `./scripts/ralph-*.sh` to `./scripts/ralph/ralph-*.sh`

### 3. `-p` flag parsed by clawker exec instead of claude
**File:** `scripts/ralph/ralph-loop.sh` line 189
**Fix:** Added `--` separator: `clawker exec -i --agent "$AGENT_NAME" -- claude -p`

### 4. exec returning immediately (not waiting for output)
**File:** `pkg/cmd/container/exec/exec.go`
**Bug:** When `-i` flag used with piped stdin, two goroutines wrote to same `errCh`. Stdin goroutine finished first (EOF), causing function to exit before output was read.
**Fix:** Changed to use dedicated `outputDone` channel for output goroutine only. Stdin goroutine no longer signals completion.

### 5. Simple detach pattern (not "worker container")
**Problem:** Originally tried "worker container" with `sleep infinity` but that skipped entrypoint.sh initialization.
**Solution:** Much simpler - just use detach:
1. `clawker run -it --agent ralph -- --dangerously-skip-permissions` (starts container with entrypoint → claude)
2. Authenticate via browser
3. Detach with Ctrl+P, Ctrl+Q (container stays running, original claude stays running)
4. `echo "task" | clawker exec -i --agent ralph -- claude -p` (new process, runs task, exits)

The original claude keeps the container alive. Each exec'd `claude -p` is a separate process that runs one task and exits - no zombies.

### 6. Claude CLI flag correction
**Problem:** Used `claude -p -` but `-p` is "print mode" flag, not prompt flag
**Fix:** Correct invocation is `echo "prompt" | claude -p` (reads from stdin in print mode)

### 7. Added `--dangerously-skip-permissions` to exec call
**File:** `scripts/ralph/ralph-loop.sh` line 196
**Fix:** Changed `claude -p` to `claude --dangerously-skip-permissions -p` since each exec spawns a new claude process that needs the flag.

Also updated comments in `ralph-setup.sh` lines 6 and 85 to reflect this.

### 8. Don't override entrypoint - use detach instead
**Problem:** Various attempts to create "worker containers" with `--entrypoint sleep` or `sleep infinity` command either skipped initialization or were unnecessarily complex.
**Solution:** Don't try to keep container alive artificially. Just:
1. Start container normally with claude as the default command
2. Authenticate
3. Detach (Ctrl+P, Ctrl+Q) - container stays running with claude
4. Exec `claude -p` for tasks

## Current State

### Files Modified
- `scripts/ralph/ralph-loop.sh` - Fixed inspect, exec syntax, claude invocation
- `scripts/ralph/ralph-setup.sh` - Worker container pattern with explicit image
- `scripts/ralph/ralph-all.sh` - Path updates
- `scripts/ralph/ralph-status.sh` - Path updates  
- `scripts/ralph/README.md` - Path updates
- `pkg/cmd/container/exec/exec.go` - Fixed output wait logic
- `README.md` - Fixed claude -p examples

### Rebuilt Binary
`./bin/clawker` has been rebuilt with the exec fix.

## Status

**Updated 2026-01-20:** All fixes completed:
- Simplified to detach pattern instead of worker container:
- Setup runs claude interactively, user authenticates, detaches with Ctrl+P Ctrl+Q
- Loop execs `claude -p` for each task iteration
- Checks for `<promise>DONE</promise>` marker to detect completion

## Testing
1. `./scripts/ralph/ralph-setup.sh ralph` - starts claude interactively for auth, user detaches
2. `./scripts/ralph/ralph-loop.sh 1 ralph` - execs `claude -p`, waits for output
3. Verify `<promise>DONE</promise>` detection marks task complete

## Key Learnings
- `clawker container ls` and `clawker container inspect` both support `--format` flag for Go template output
- `clawker exec` needs `--` to separate its flags from container command flags
- For piped input with exec, must wait specifically for OUTPUT goroutine, not just any goroutine
- **Detach pattern is simpler than worker containers** - just run claude interactively, auth, detach with Ctrl+P Ctrl+Q, then exec `claude -p` for tasks
- Claude CLI: `-p/--print` = non-interactive mode that reads from stdin
- Ralph Wiggum pattern: Prompt tells agent to output `<promise>DONE</promise>` when task complete; loop checks for this marker
