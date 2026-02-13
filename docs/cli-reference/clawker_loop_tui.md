## clawker loop tui

Launch interactive TUI dashboard

### Synopsis

Launch an interactive terminal dashboard for monitoring loop agents.

The TUI provides a real-time view of all loop agents in the current project,
including their status, loop progress, and recent log output.

Features:
  - Live agent discovery and status updates
  - Log streaming from active agents
  - Quick actions (stop, reset circuit breaker)
  - Session history and statistics

```
clawker loop tui [flags]
```

### Examples

```
  # Launch TUI for current project
  clawker loop tui
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

* [clawker loop](clawker_loop.md) - Run Claude Code in autonomous loops
