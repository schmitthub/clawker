## clawker container pause

Pause all processes within one or more containers

### Synopsis

Pauses all processes within one or more clawker containers.

The container is suspended using the cgroups freezer.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker container pause [CONTAINER...] [flags]
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
      --agent string   Agent name (resolves to clawker.<project>.<agent>)
  -h, --help           help for pause
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker container](clawker_container.md) - Manage containers
