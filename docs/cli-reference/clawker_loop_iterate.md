## clawker loop iterate

Run an agent loop with a repeated prompt

### Synopsis

Run Claude Code in an autonomous loop, repeating the same prompt each iteration.

Each iteration starts a fresh Claude session (no conversation context carried
forward). The agent only sees the current codebase state from previous runs.

The loop exits when:
  - Claude signals completion via a LOOP_STATUS block
  - The circuit breaker trips (stagnation, same error, output decline)
  - Maximum iterations reached
  - A timeout is hit

Container lifecycle is managed automatically: a container is created at the
start and destroyed on completion.

```
clawker loop iterate [flags]
```

### Examples

```
  # Run a loop with a prompt
  clawker loop iterate --prompt "Fix all failing tests"

  # Run with a prompt from a file
  clawker loop iterate --prompt-file task.md

  # Run with custom loop limits
  clawker loop iterate --prompt "Refactor auth module" --max-loops 100
```

### Options

```
  -h, --help   help for iterate
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker loop](clawker_loop.md) - Run Claude Code in autonomous loops
