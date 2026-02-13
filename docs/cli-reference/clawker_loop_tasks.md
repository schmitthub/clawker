## clawker loop tasks

Run an agent loop driven by a task file

### Synopsis

Run Claude Code in an autonomous loop driven by a task file.

Each loop session gets an auto-generated agent name (e.g., loop-brave-turing).
A new container is created, hooks are injected, and the container is automatically
cleaned up when the loop exits. Each iteration, the agent reads the task file,
picks an open task, completes it, and marks it done. Clawker manages the loop —
the agent LLM handles task selection and completion.

The loop exits when:
  - All tasks are completed (agent signals via LOOP_STATUS)
  - The circuit breaker trips (stagnation, same error, output decline)
  - Maximum iterations reached
  - A timeout is hit

```
clawker loop tasks [flags]
```

### Examples

```
  # Run a task-driven loop
  clawker loop tasks --tasks todo.md

  # Run with a custom task prompt template
  clawker loop tasks --tasks todo.md --task-prompt-file instructions.md

  # Run with a custom inline task prompt
  clawker loop tasks --tasks backlog.md --task-prompt "Pick the highest priority task"

  # Use a specific image
  clawker loop tasks --tasks todo.md --image node:20-slim

  # Stream all agent output in real time
  clawker loop tasks --tasks todo.md --verbose

  # Output final result as JSON
  clawker loop tasks --tasks todo.md --json
```

### Options

```
      --append-system-prompt string       Additional system prompt instructions appended to the LOOP_STATUS default
      --calls-per-hour int                API call rate limit per hour (0 to disable) (default 100)
      --completion-threshold int          Completion indicators required for strict completion (default 2)
      --format string                     Output format: "json", "table", or a Go template
  -h, --help                              help for tasks
      --hooks-file string                 Path to hook configuration file (overrides default hooks)
      --image string                      Override container image (default: project config or user settings)
      --json                              Output as JSON (shorthand for --format json)
      --loop-delay int                    Seconds to wait between iterations (default 3)
      --max-loops int                     Maximum number of iterations (default 50)
      --max-test-loops int                Consecutive test-only iterations before circuit breaker trips (default 3)
      --output-decline-threshold int      Output size decline percentage before circuit breaker trips (default 70)
  -q, --quiet                             Only display IDs
      --reset-circuit                     Reset circuit breaker before starting
      --safety-completion-threshold int   Iterations with completion indicators but no exit signal before trip (default 5)
      --same-error-threshold int          Consecutive identical errors before circuit breaker trips (default 5)
      --skip-permissions                  Allow all tools without prompting
      --stagnation-threshold int          Iterations without progress before circuit breaker trips (default 3)
      --strict-completion                 Require both exit signal and completion indicators for completion
      --task-prompt string                Prompt template for task selection and execution
      --task-prompt-file string           Path to file containing the task prompt template
      --tasks string                      Path to the task file
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
