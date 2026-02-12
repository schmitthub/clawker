## clawker monitor down

Stop the monitoring stack

### Synopsis

Stops the monitoring stack using Docker Compose.

This stops and removes all monitoring containers while preserving
the clawker-net Docker network for other clawker services.

```
clawker monitor down [flags]
```

### Examples

```
  # Stop the monitoring stack
  clawker monitor down

  # Stop and remove volumes
  clawker monitor down --volumes
```

### Options

```
  -h, --help      help for down
  -v, --volumes   Remove named volumes declared in compose.yaml
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker monitor](clawker_monitor.md) - Manage local observability stack
