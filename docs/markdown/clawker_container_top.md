## clawker container top

Display the running processes of a container

### Synopsis

Display the running processes of a clawker container.

Additional arguments are passed directly to ps as options.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container name can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker container top [CONTAINER] [ps OPTIONS] [flags]
```

### Examples

```
  # Show processes using agent name
  clawker container top --agent ralph

  # Show processes by full container name
  clawker container top clawker.myapp.ralph

  # Show processes with custom ps options
  clawker container top --agent ralph aux

  # Show all processes with extended info
  clawker container top --agent ralph -ef
```

### Options

```
      --agent string   Agent name (resolves to clawker.<project>.<agent>)
  -h, --help           help for top
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker container](clawker_container.md) - Manage containers
