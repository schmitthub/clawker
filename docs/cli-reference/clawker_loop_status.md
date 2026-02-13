## clawker loop status

Show current loop session status

### Synopsis

Display the current status of a loop session for an agent.

Shows information about:
  - Session state (started, updated, loops completed)
  - Circuit breaker state (tripped, no-progress count)
  - Cumulative statistics (tasks completed, files modified)

```
clawker loop status [flags]
```

### Examples

```
  # Show status for an agent
  clawker loop status --agent dev

  # Output as JSON
  clawker loop status --agent dev --json
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

* [clawker loop](clawker_loop.md) - Run Claude Code in autonomous loops
