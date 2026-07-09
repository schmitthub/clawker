---
title: "clawker monitor"
---

## clawker monitor

Manage local observability stack

### Synopsis

Commands for managing the local observability stack.

The monitoring stack provides local telemetry visualization for coding-agent
harness sessions using OpenTelemetry Collector + OpenSearch (logs + traces) +
OpenSearch Dashboards + Prometheus (metrics).

Available commands:
  init      Scaffold monitoring configuration files
  up        Start the monitoring stack
  down      Stop the monitoring stack
  status    Show monitoring stack status
  register  Register a monitoring unit directory
  remove    Remove a monitoring unit registration
  list      List monitoring units
  enable    Activate a monitoring unit
  disable   Deactivate a monitoring unit

Monitoring units are observability loadouts (OpenSearch index + ingest
pipelines + dashboards + collector routing) shipped by harness bundles or
registered by path. Only enabled units are seeded into the stack.

### Examples

```
  # Initialize monitoring configuration
  clawker monitor init

  # Start the monitoring stack
  clawker monitor up

  # Seed Claude Code telemetry (opt-in)
  clawker monitor enable claude-code
  clawker monitor init && clawker monitor up

  # Check stack status
  clawker monitor status

  # Stop the stack
  clawker monitor down
```

### Subcommands

* [clawker monitor disable](clawker_monitor_disable) - Deactivate a monitoring unit
* [clawker monitor down](clawker_monitor_down) - Stop the monitoring stack
* [clawker monitor enable](clawker_monitor_enable) - Activate a monitoring unit
* [clawker monitor init](clawker_monitor_init) - Scaffold monitoring configuration files
* [clawker monitor list](clawker_monitor_list) - List monitoring units
* [clawker monitor register](clawker_monitor_register) - Register a monitoring unit directory
* [clawker monitor remove](clawker_monitor_remove) - Remove a monitoring unit registration
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
