## clawker loop

Run Claude Code in autonomous loops

### Synopsis

Commands for running Claude Code agents in autonomous loops.

The loop command automates Claude Code execution: Claude runs repeatedly
until signaling completion via a LOOP_STATUS block in its output.

Two loop strategies are available:
  iterate  Same prompt repeated fresh each invocation
  tasks    Agent reads a task file, picks an open task, does it, marks it done

Container lifecycle is managed automatically — a fresh container is created for
each iteration and destroyed afterward. Workspace and config volumes persist
across iterations so the agent sees cumulative codebase changes.

Available commands:
  iterate  Run an agent loop with a repeated prompt
  tasks    Run an agent loop driven by a task file
  status   Show current session status
  reset    Reset the circuit breaker

### Examples

```
  # Run a loop with a repeated prompt
  clawker loop iterate --prompt "Fix all failing tests"

  # Run a task-driven loop
  clawker loop tasks --tasks todo.md

  # Check the status of a loop session
  clawker loop status --agent dev

  # Reset the circuit breaker after stagnation
  clawker loop reset --agent dev
```

### Subcommands

* [clawker loop iterate](clawker_loop_iterate.md) - Run an agent loop with a repeated prompt
* [clawker loop reset](clawker_loop_reset.md) - Reset the circuit breaker for an agent
* [clawker loop status](clawker_loop_status.md) - Show current loop session status
* [clawker loop tasks](clawker_loop_tasks.md) - Run an agent loop driven by a task file

### Options

```
  -h, --help   help for loop
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
