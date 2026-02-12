## clawker monitor

Manage local observability stack

### Synopsis

Commands for managing the local observability stack.

The monitoring stack provides local telemetry visualization for Claude Code
sessions using OpenTelemetry, Jaeger, Prometheus, and Grafana.

Available commands:
  init    Scaffold monitoring configuration files
  up      Start the monitoring stack
  down    Stop the monitoring stack
  status  Show monitoring stack status

### Examples

```
  # Initialize monitoring configuration
  clawker monitor init

  # Start the monitoring stack
  clawker monitor up

  # Check stack status
  clawker monitor status

  # Stop the stack
  clawker monitor down
```

### Subcommands

* [clawker monitor down](clawker_monitor_down.md) - Stop the monitoring stack
* [clawker monitor init](clawker_monitor_init.md) - Scaffold monitoring configuration files
* [clawker monitor status](clawker_monitor_status.md) - Show monitoring stack status
* [clawker monitor up](clawker_monitor_up.md) - Start the monitoring stack

### Options

```
  -h, --help   help for monitor
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
