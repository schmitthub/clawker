## clawker rm

Remove one or more containers

### Synopsis

Removes one or more clawker containers.

By default, only stopped containers can be removed. Use --force to remove
running containers.

When --agent is provided, the container names are resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker rm [OPTIONS] CONTAINER [CONTAINER...] [flags]
```

### Aliases

`rm`, `rm`

### Examples

```
  # Remove a container using agent name
  clawker container remove --agent ralph

  # Remove a stopped container by full name
  clawker container remove clawker.myapp.ralph

  # Remove multiple containers
  clawker container rm clawker.myapp.ralph clawker.myapp.writer

  # Force remove a running container
  clawker container remove --force --agent ralph

  # Remove container and its volumes
  clawker container remove --volumes --agent ralph
```

### Options

```
      --agent     Treat arguments as agent names (resolves to clawker.<project>.<agent>)
  -f, --force     Force remove running containers
  -h, --help      help for rm
  -v, --volumes   Remove associated volumes
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
