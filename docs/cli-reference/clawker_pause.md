## clawker pause

Pause all processes within one or more containers

### Synopsis

Pauses all processes within one or more clawker containers.

The container is suspended using the cgroups freezer.

When --agent is provided, the container names are resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker pause [OPTIONS] CONTAINER [CONTAINER...] [flags]
```

### Examples

```
  # Pause a container using agent name
  clawker container pause --agent ralph

  # Pause a container by full name
  clawker container pause clawker.myapp.ralph

  # Pause multiple containers
  clawker container pause clawker.myapp.ralph clawker.myapp.writer
```

### Options

```
      --agent   Treat arguments as agent names (resolves to clawker.<project>.<agent>)
  -h, --help    help for pause
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
