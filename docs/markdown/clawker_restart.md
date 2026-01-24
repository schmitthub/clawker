## clawker restart

Restart one or more containers

### Synopsis

Restarts one or more clawker containers.

The container is stopped with a timeout period (default 10s), then started again.
If --signal is specified, that signal is sent instead of SIGTERM.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker restart [OPTIONS] CONTAINER [CONTAINER...] [flags]
```

### Examples

```
  # Restart a container using agent name
  clawker container restart --agent ralph

  # Restart a container by full name (10s timeout)
  clawker container restart clawker.myapp.ralph

  # Restart multiple containers
  clawker container restart clawker.myapp.ralph clawker.myapp.writer

  # Restart with a custom timeout (20 seconds)
  clawker container restart --time 20 --agent ralph
```

### Options

```
      --agent           Treat arguments as agent names (resolves to clawker.<project>.<agent>)
  -h, --help            help for restart
  -s, --signal string   Signal to send (default: SIGTERM)
  -t, --time int        Seconds to wait before killing the container (default 10)
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
