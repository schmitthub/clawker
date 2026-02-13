## clawker loop tasks

Run an agent loop driven by a task file

### Synopsis

Run Claude Code in an autonomous loop driven by a task file.

Each iteration, the agent reads the task file, picks an open task, completes
it, and marks it done. Clawker manages the loop — the agent LLM handles task
selection and completion.

The loop exits when:
  - All tasks are completed (agent signals via LOOP_STATUS)
  - The circuit breaker trips (stagnation, same error, output decline)
  - Maximum iterations reached
  - A timeout is hit

Container lifecycle is managed automatically: a container is created at the
start and destroyed on completion.

```
clawker loop tasks [flags]
```

### Examples

```
  # Run a task-driven loop
  clawker loop tasks --tasks todo.md

  # Run with a custom task prompt template
  clawker loop tasks --tasks todo.md --task-prompt-file instructions.md
```

### Options

```
  -h, --help   help for tasks
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker loop](clawker_loop.md) - Run Claude Code in autonomous loops
