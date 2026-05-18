---
title: "clawker monitor up"
---

## clawker monitor up

Start the monitoring stack

### Synopsis

Starts the monitoring stack using Docker Compose.

This launches the following services:
  - OpenSearch (port 9200)
  - OpenSearch Dashboards (port 5601)
  - clawker-opensearch-bootstrap (one-shot)
  - OpenTelemetry Collector (ports 4317, 4318)
  - Prometheus (port 9090)

The clawker-opensearch-bootstrap container runs once after OpenSearch
reports healthy and applies index templates, ISM retention, and
Dashboards index-pattern saved objects for the five preconfigured
indices (claude-code, clawker-cli, clawker-cp, clawker-envoy,
clawker-coredns). The collector and Prometheus gate on its
service_completed_successfully, so a bootstrap failure leaves the stack
half-up by design — surfacing the problem instead of letting the
collector silently create wrong-mapped indices.

The stack connects to the clawker-net Docker network, allowing
Claude Code containers to send telemetry automatically.

```
clawker monitor up [flags]
```

### Examples

```
  # Start the monitoring stack (detached)
  clawker monitor up

  # Start in foreground (see logs)
  clawker monitor up --detach=false
```

### Options

```
      --detach   Run in detached mode (default true)
  -h, --help     help for up
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker monitor](clawker_monitor) - Manage local observability stack
