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

'monitor up' renders the stack config from this project's selected monitoring
extensions before starting, and idempotently seeds them onto the running stack:
the collector config is regenerated over every extension ever seeded (across all
projects) so a teammate's routings survive, while this project's OpenSearch
artifacts are (re)applied by the bootstrap container. Agent containers send
telemetry to the stack automatically.

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
