## clawker container list

List containers

### Synopsis

Lists all containers created by clawker.

By default, shows only running containers. Use -a to show all containers.

Note: Use 'clawker monitor status' for monitoring stack containers.

```
clawker container list [flags]
```

### Aliases

`list`, `ls`, `ps`

### Examples

```
  # List running containers
  clawker container list

  # List all containers (including stopped)
  clawker container ls -a

  # List containers for a specific project
  clawker container list -p myproject

  # List container names only
  clawker container ls -a --format '{{.Names}}'

  # Custom format showing name and status
  clawker container ls -a --format '{{.Name}} {{.Status}}'
```

### Options

```
  -a, --all              Show all containers (including stopped)
      --format string    Format output using a Go template
  -h, --help             help for list
  -p, --project string   Filter by project name
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker container](clawker_container.md) - Manage containers
