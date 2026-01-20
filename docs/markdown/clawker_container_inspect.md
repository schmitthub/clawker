## clawker container inspect

Display detailed information on one or more containers

### Synopsis

Returns low-level information about clawker containers.

By default, outputs JSON. Use --format to extract specific fields.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker container inspect [CONTAINER...] [flags]
```

### Examples

```
  # Inspect a container using agent name
  clawker container inspect --agent ralph

  # Inspect a container by full name
  clawker container inspect clawker.myapp.ralph

  # Inspect multiple containers
  clawker container inspect clawker.myapp.ralph clawker.myapp.writer

  # Get specific field using Go template
  clawker container inspect --format '{{.State.Status}}' --agent ralph

  # Get container IP address
  clawker container inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' --agent ralph
```

### Options

```
      --agent string    Agent name (resolves to clawker.<project>.<agent>)
  -f, --format string   Format output using a Go template
  -h, --help            help for inspect
  -s, --size            Display total file sizes
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker container](clawker_container.md) - Manage containers
