## clawker loop iterate

Run an agent loop with a repeated prompt

### Synopsis

Run Claude Code in an autonomous loop, repeating the same prompt each iteration.

A new container is created for the loop session, hooks are injected, and the
container is automatically cleaned up when the loop exits. Each iteration starts
a fresh Claude session (no conversation context carried forward). The agent only
sees the current codebase state from previous runs.

The loop exits when:
  - Claude signals completion via a LOOP_STATUS block
  - The circuit breaker trips (stagnation, same error, output decline)
  - Maximum iterations reached
  - A timeout is hit

```
clawker loop iterate [flags]
```

### Examples

```
  # Run a loop with a prompt
  clawker loop iterate --agent dev --prompt "Fix all failing tests"

  # Run with a prompt from a file
  clawker loop iterate --agent dev --prompt-file task.md

  # Run with custom loop limits
  clawker loop iterate --agent dev --prompt "Refactor auth module" --max-loops 100

  # Stream all agent output in real time
  clawker loop iterate --agent dev --prompt "Add tests" --verbose

  # Run in a git worktree for isolation
  clawker loop iterate --agent dev --prompt "Refactor auth" --worktree feature/auth

  # Use a specific image
  clawker loop iterate --agent dev --prompt "Fix tests" --image node:20-slim

  # Output final result as JSON
  clawker loop iterate --agent dev --prompt "Fix tests" --json
```

### Options

```
      --agent string                      Agent name (identifies container and session)
      --append-system-prompt string       Additional system prompt instructions appended to the LOOP_STATUS default
      --calls-per-hour int                API call rate limit per hour (0 to disable) (default 100)
      --completion-threshold int          Completion indicators required for strict completion (default 2)
      --format string                     Output format: "json", "table", or a Go template
  -h, --help                              help for iterate
      --hooks-file string                 Path to hook configuration file (overrides default hooks)
      --image string                      Override container image (default: project config or user settings)
      --json                              Output as JSON (shorthand for --format json)
      --loop-delay int                    Seconds to wait between iterations (default 3)
      --max-loops int                     Maximum number of iterations (default 50)
      --max-test-loops int                Consecutive test-only iterations before circuit breaker trips (default 3)
      --output-decline-threshold int      Output size decline percentage before circuit breaker trips (default 70)
  -p, --prompt string                     Prompt to repeat each iteration
      --prompt-file string                Path to file containing the prompt
  -q, --quiet                             Only display IDs
      --reset-circuit                     Reset circuit breaker before starting
      --safety-completion-threshold int   Iterations with completion indicators but no exit signal before trip (default 5)
      --same-error-threshold int          Consecutive identical errors before circuit breaker trips (default 5)
      --skip-permissions                  Allow all tools without prompting
      --stagnation-threshold int          Iterations without progress before circuit breaker trips (default 3)
      --strict-completion                 Require both exit signal and completion indicators for completion
      --timeout int                       Per-iteration timeout in minutes (default 15)
  -v, --verbose                           Stream all agent output in real time (non-interactive)
      --worktree string                   Run in a git worktree (optional branch[:base] spec, empty for auto-generated)
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker loop](clawker_loop.md) - Run Claude Code in autonomous loops
