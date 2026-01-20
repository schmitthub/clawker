## clawker container wait

Block until one or more containers stop, then print their exit codes

### Synopsis

Blocks until one or more clawker containers stop, then prints their exit codes.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker container wait [CONTAINER...] [flags]
```

### Examples

```
  # Wait for a container using agent name
  clawker container wait --agent ralph

  # Wait for a container by full name
  clawker container wait clawker.myapp.ralph

  # Wait for multiple containers
  clawker container wait clawker.myapp.ralph clawker.myapp.writer
```

### Options

```
      --agent string   Agent name (resolves to clawker.<project>.<agent>)
  -h, --help           help for wait
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker container](clawker_container.md) - Manage containers
