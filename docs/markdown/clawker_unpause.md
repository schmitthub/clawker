## clawker unpause

Unpause all processes within one or more containers

### Synopsis

Unpauses all processes within one or more paused clawker containers.

When --agent is provided, the container names are resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker unpause [OPTIONS] CONTAINER [CONTAINER...] [flags]
```

### Examples

```
  # Unpause a container using agent name
  clawker container unpause --agent ralph

  # Unpause a container by full name
  clawker container unpause clawker.myapp.ralph

  # Unpause multiple containers
  clawker container unpause clawker.myapp.ralph clawker.myapp.writer
```

### Options

```
      --agent   Treat arguments as agent names (resolves to clawker.<project>.<agent>)
  -h, --help    help for unpause
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
