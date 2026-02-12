## clawker ps

List containers

### Synopsis

Lists all containers created by clawker.

By default, shows only running containers. Use -a to show all containers.

Note: Use 'clawker monitor status' for monitoring stack containers.

```
clawker ps [OPTIONS] [flags]
```

### Aliases

`ps`, `ls`, `ps`

### Examples

```
  # List running containers
  clawker container list

  # List all containers (including stopped)
  clawker container ls -a

  # List containers for a specific project
  clawker container list -p myproject

  # List container names only
  clawker container ls -q

  # Output as JSON
  clawker container ls --json

  # Custom Go template
  clawker container ls --format '{{.Name}} {{.Status}}'

  # Filter by status
  clawker container ls -a --filter status=running

  # Filter by agent name
  clawker container ls --filter agent=ralph
```

### Options

```
  -a, --all                  Show all containers (including stopped)
      --filter stringArray   Filter output (key=value, repeatable)
      --format string        Output format: "json", "table", or a Go template
  -h, --help                 help for ps
      --json                 Output as JSON (shorthand for --format json)
  -p, --project string       Filter by project name
  -q, --quiet                Only display IDs
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
