## clawker loop

Run Claude Code in autonomous loops

### Synopsis

Commands for running Claude Code agents in autonomous loops.

The loop command automates Claude Code execution: Claude runs repeatedly
until signaling completion via a LOOP_STATUS block in its output.

The agent must be configured to output a LOOP_STATUS block in its responses.
See the documentation for the expected format.

Available commands:
  run     Start the autonomous loop
  status  Show current session status
  reset   Reset the circuit breaker
  tui     Launch interactive dashboard

### Examples

```
  # Start a loop with an initial prompt
  clawker loop run --agent dev --prompt "Fix all failing tests"

  # Check the status of a loop session
  clawker loop status --agent dev

  # Reset the circuit breaker after stagnation
  clawker loop reset --agent dev
```

### Subcommands

* [clawker loop reset](clawker_loop_reset.md) - Reset the circuit breaker for an agent
* [clawker loop run](clawker_loop_run.md) - Start an autonomous Claude Code loop
* [clawker loop status](clawker_loop_status.md) - Show current loop session status
* [clawker loop tui](clawker_loop_tui.md) - Launch interactive TUI dashboard

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
