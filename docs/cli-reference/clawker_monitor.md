---
title: "clawker monitor"
---

## clawker monitor

Manage local observability stack

### Synopsis

Commands for managing the local observability stack.

The monitoring stack provides local telemetry visualization for Claude Code
sessions using OpenTelemetry Collector + OpenSearch (logs + traces) +
OpenSearch Dashboards + Prometheus (metrics).

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

* [clawker monitor down](clawker_monitor_down) - Stop the monitoring stack
* [clawker monitor init](clawker_monitor_init) - Scaffold monitoring configuration files
* [clawker monitor status](clawker_monitor_status) - Show monitoring stack status
* [clawker monitor up](clawker_monitor_up) - Start the monitoring stack

### Options

```
  -h, --help   help for monitor
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Run coding agents in secure Docker containers with clawker
