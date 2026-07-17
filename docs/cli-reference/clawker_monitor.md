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
  init        Scaffold monitoring configuration files
  up          Start the monitoring stack
  reload      Apply this project's monitoring extensions to the running stack
  down        Stop the monitoring stack
  status      Show monitoring stack status
  extensions  List resolvable monitoring extensions

Monitoring extensions are observability loadouts (OpenSearch index + ingest
pipelines + dashboards + collector routing). A project selects them by name in
its clawker.yaml (`monitor.extensions`); they resolve from the embedded
floor, a loose .clawker/monitoring/`<name>`/ directory, or an installed bundle, and
are seeded onto the stack by 'monitor up' (or applied to a running stack by
'monitor reload').

### Examples

```
  # Initialize monitoring configuration
  clawker monitor init

  # Start the monitoring stack (seeds this project's selected extensions)
  clawker monitor up

  # Check stack status
  clawker monitor status

  # Stop the stack
  clawker monitor down
```

### Subcommands

* [clawker monitor down](clawker_monitor_down) - Stop the monitoring stack
* [clawker monitor extensions](clawker_monitor_extensions) - List resolvable monitoring extensions and their provenance
* [clawker monitor init](clawker_monitor_init) - Scaffold monitoring configuration files
* [clawker monitor reload](clawker_monitor_reload) - Apply this project's monitoring extensions to the running stack
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
