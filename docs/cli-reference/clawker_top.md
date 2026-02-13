## clawker top

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
clawker top [OPTIONS] CONTAINER [flags]
```

### Examples

```
  # Show processes using agent name
  clawker container top --agent dev

  # Show processes by full container name
  clawker container top clawker.myapp.dev

  # Show processes with custom ps options
  clawker container top --agent dev aux

  # Show all processes with extended info
  clawker container top --agent dev -ef
```

### Options

```
      --agent   Treat first argument as agent name (resolves to clawker.<project>.<agent>)
  -h, --help    help for top
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
