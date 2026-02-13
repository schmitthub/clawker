## clawker loop run

Start an autonomous Claude Code loop

### Synopsis

Run Claude Code in an autonomous loop until completion or stagnation.

The agent will run Claude Code repeatedly with --continue, parsing each
iteration's output for a LOOP_STATUS block. The loop exits when:

  - Claude signals EXIT_SIGNAL: true with sufficient completion indicators
  - The circuit breaker trips (no progress, same error, output decline)
  - Maximum loops reached
  - An error occurs
  - Claude's API rate limit is hit

The container must already be running. Use 'clawker start' first.

```
clawker loop run [flags]
```

### Examples

```
  # Start with an initial prompt
  clawker loop run --agent dev --prompt "Fix all failing tests"

  # Start with a prompt from a file
  clawker loop run --agent dev --prompt-file task.md

  # Continue an existing session
  clawker loop run --agent dev

  # Reset circuit breaker and retry
  clawker loop run --agent dev --reset-circuit

  # Run with custom limits
  clawker loop run --agent dev --max-loops 100 --stagnation-threshold 5

  # Run with live monitoring
  clawker loop run --agent dev --monitor

  # Run with rate limiting (5 calls per hour)
  clawker loop run --agent dev --calls 5

  # Run with verbose output
  clawker loop run --agent dev -v

  # Run in YOLO mode (skip all permission prompts)
  clawker loop run --agent dev --skip-permissions
```

### Options

```
      --agent string                   Agent name (required)
      --calls int                      Rate limit: max calls per hour (0 to disable) (default 100)
  -h, --help                           help for run
      --json                           Output result as JSON
      --loop-delay int                 Seconds to wait between loop iterations (default 3)
      --max-loops int                  Maximum number of loops (default 50)
      --max-test-loops int             Consecutive test-only loops before circuit trips (default 3)
      --monitor                        Enable live monitoring output
      --output-decline-threshold int   Output decline percentage that triggers trip (default 70)
  -p, --prompt string                  Initial prompt for the first loop
      --prompt-file string             File containing the initial prompt
  -q, --quiet                          Suppress progress output
      --reset-circuit                  Reset circuit breaker before starting
      --same-error-threshold int       Same error repetitions before circuit trips (default 5)
      --skip-permissions               Pass --dangerously-skip-permissions to claude
      --stagnation-threshold int       Loops without progress before circuit trips (default 3)
      --strict-completion              Require both EXIT_SIGNAL and completion indicators
      --timeout duration               Timeout per loop iteration (default 15m0s)
  -v, --verbose                        Enable verbose output
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker loop](clawker_loop.md) - Run Claude Code in autonomous loops
