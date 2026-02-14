## clawker container stop

Stop one or more running containers

### Synopsis

Stops one or more running clawker containers.

The container is sent a SIGTERM signal, then after a timeout period (default 10s),
it is sent SIGKILL if still running.

When --agent is provided, the container names are resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker container stop [CONTAINER...] [flags]
```

### Examples

```
  # Stop a container using agent name (resolves via project config)
  clawker container stop --agent dev

  # Stop a container by full name (10s timeout)
  clawker container stop clawker.myapp.dev

  # Stop multiple containers
  clawker container stop clawker.myapp.dev clawker.myapp.writer

  # Stop with a custom timeout (20 seconds)
  clawker container stop --time 20 --agent dev
```

### Options

```
      --agent           Treat arguments as agent name (resolves to clawker.<project>.<agent>)
  -h, --help            help for stop
  -s, --signal string   Signal to send (default: SIGTERM)
  -t, --time int        Seconds to wait before killing the container (default 10)
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker container](clawker_container.md) - Manage containers
