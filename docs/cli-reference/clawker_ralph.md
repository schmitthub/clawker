## clawker ralph

Run Claude Code in autonomous loops

### Synopsis

Commands for running Claude Code agents in autonomous loops.

The ralph command automates Claude Code execution using the "Ralph Wiggum"
technique: Claude runs repeatedly with --continue until signaling completion
via a RALPH_STATUS block in its output.

The agent must be configured to output a RALPH_STATUS block in its responses.
See the documentation for the expected format.

Available commands:
  run     Start the autonomous loop
  status  Show current session status
  reset   Reset the circuit breaker
  tui     Launch interactive dashboard

### Examples

```
  # Start a ralph loop with an initial prompt
  clawker ralph run --agent dev --prompt "Fix all failing tests"

  # Check the status of a ralph session
  clawker ralph status --agent dev

  # Reset the circuit breaker after stagnation
  clawker ralph reset --agent dev
```

### Subcommands

* [clawker ralph reset](clawker_ralph_reset.md) - Reset the circuit breaker for an agent
* [clawker ralph run](clawker_ralph_run.md) - Start an autonomous Claude Code loop
* [clawker ralph status](clawker_ralph_status.md) - Show current ralph session status
* [clawker ralph tui](clawker_ralph_tui.md) - Launch interactive TUI dashboard

### Options

```
  -h, --help   help for ralph
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
