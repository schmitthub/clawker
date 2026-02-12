## clawker ralph status

Show current ralph session status

### Synopsis

Display the current status of a ralph session for an agent.

Shows information about:
  - Session state (started, updated, loops completed)
  - Circuit breaker state (tripped, no-progress count)
  - Cumulative statistics (tasks completed, files modified)

```
clawker ralph status [flags]
```

### Examples

```
  # Show status for an agent
  clawker ralph status --agent dev

  # Output as JSON
  clawker ralph status --agent dev --json
```

### Options

```
      --agent string   Agent name (required)
  -h, --help           help for status
      --json           Output as JSON
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker ralph](clawker_ralph.md) - Run Claude Code in autonomous loops
