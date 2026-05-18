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

On 'monitor up', a one-shot clawker-opensearch-bootstrap container
preconfigures the cluster — index templates, ISM retention, and Dashboards
index patterns for claude-code, clawker-cli, clawker-cp, clawker-envoy,
and clawker-coredns — before the collector and Prometheus start. The
stack is intended to be throwaway (regenerate via 'monitor down --volumes
&& monitor up' to pick up template edits).

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

* [clawker](clawker) - Manage Claude Code in secure Docker containers with clawker
