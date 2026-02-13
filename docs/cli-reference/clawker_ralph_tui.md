## clawker ralph tui

Launch interactive TUI dashboard

### Synopsis

Launch an interactive terminal dashboard for monitoring ralph agents.

The TUI provides a real-time view of all ralph agents in the current project,
including their status, loop progress, and recent log output.

Features:
  - Live agent discovery and status updates
  - Log streaming from active agents
  - Quick actions (stop, reset circuit breaker)
  - Session history and statistics

```
clawker ralph tui [flags]
```

### Examples

```
  # Launch TUI for current project
  clawker ralph tui
```

### Options

```
  -h, --help   help for tui
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker ralph](clawker_ralph.md) - Run Claude Code in autonomous loops
