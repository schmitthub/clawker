## clawker ralph reset

Reset the circuit breaker for an agent

### Synopsis

Reset the circuit breaker to allow ralph loops to continue.

The circuit breaker trips when an agent shows no progress for multiple
consecutive loops. Use this command to reset it and retry.

By default, only the circuit breaker is reset. Use --all to also clear
the session history.

```
clawker ralph reset [flags]
```

### Examples

```
  # Reset circuit breaker only
  clawker ralph reset --agent dev

  # Reset everything (circuit and session)
  clawker ralph reset --agent dev --all
```

### Options

```
      --agent string   Agent name (required)
      --all            Also clear session history
  -h, --help           help for reset
  -q, --quiet          Suppress output
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker ralph](clawker_ralph.md) - Run Claude Code in autonomous loops
