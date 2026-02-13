## clawker container start

Start one or more stopped containers

### Synopsis

Starts one or more stopped clawker containers.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker container start [OPTIONS] CONTAINER [CONTAINER...] [flags]
```

### Examples

```
  # Start a stopped container by full name
  clawker container start clawker.myapp.dev

  # Start a container using agent name (resolves via project config)
  clawker container start --agent dev

  # Start multiple containers
  clawker container start clawker.myapp.dev clawker.myapp.writer

  # Start and attach to container output
  clawker container start --attach clawker.myapp.dev
```

### Options

```
      --agent         Use agent name (resolves to clawker.<project>.<agent>)
  -a, --attach        Attach STDOUT/STDERR and forward signals
  -h, --help          help for start
  -i, --interactive   Attach container's STDIN
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker container](clawker_container.md) - Manage containers
