## clawker rename

Rename a container

### Synopsis

Renames a clawker container.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration, and only NEW_NAME is required.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker rename CONTAINER NEW_NAME [flags]
```

### Examples

```
  # Rename a container using agent name
  clawker container rename --agent ralph clawker.myapp.newname

  # Rename a container by full name
  clawker container rename clawker.myapp.ralph clawker.myapp.newname
```

### Options

```
      --agent   Treat first argument as agent name (resolves to clawker.<project>.<agent>)
  -h, --help    help for rename
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
