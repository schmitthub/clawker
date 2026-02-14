## clawker container kill

Kill one or more running containers

### Synopsis

Kills one or more running clawker containers.

The main process inside the container is sent SIGKILL signal (default),
or the signal specified with the --signal option.

When --agent is provided, the container names are resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker container kill [CONTAINER...] [flags]
```

### Examples

```
  # Kill a container using agent name
  clawker container kill --agent dev

  # Kill a container by full name (SIGKILL)
  clawker container kill clawker.myapp.dev

  # Kill multiple containers
  clawker container kill clawker.myapp.dev clawker.myapp.writer

  # Send specific signal
  clawker container kill --signal SIGTERM --agent dev
  clawker container kill -s SIGINT clawker.myapp.dev
```

### Options

```
      --agent           Treat arguments as agent names (resolves to clawker.<project>.<agent>)
  -h, --help            help for kill
  -s, --signal string   Signal to send to the container (default "SIGKILL")
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker container](clawker_container.md) - Manage containers
