## clawker container logs

Fetch the logs of a container

### Synopsis

Fetches the logs of a clawker container.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container name can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker container logs [CONTAINER] [flags]
```

### Examples

```
  # Show logs using agent name
  clawker container logs --agent ralph

  # Show logs by full container name
  clawker container logs clawker.myapp.ralph

  # Follow log output (like tail -f)
  clawker container logs --follow --agent ralph

  # Show last 50 lines
  clawker container logs --tail 50 --agent ralph

  # Show logs since a timestamp
  clawker container logs --since 2024-01-01T00:00:00Z --agent ralph

  # Show logs with timestamps
  clawker container logs --timestamps --agent ralph
```

### Options

```
      --agent          Treat argument as agent name (resolves to clawker.<project>.<agent>)
      --details        Show extra details provided to logs
  -f, --follow         Follow log output
  -h, --help           help for logs
      --since string   Show logs since timestamp (e.g., 2024-01-01T00:00:00Z) or relative (e.g., 42m)
      --tail string    Number of lines to show from the end (default: all) (default "all")
  -t, --timestamps     Show timestamps
      --until string   Show logs before timestamp (e.g., 2024-01-01T00:00:00Z) or relative (e.g., 42m)
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker container](clawker_container.md) - Manage containers
