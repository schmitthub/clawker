---
title: "clawker monitor init"
---

## clawker monitor init

Scaffold monitoring configuration files

### Synopsis

Scaffolds the monitoring stack configuration files in ~/.clawker/monitor/.

This command generates:
  - compose.yaml        Docker Compose stack definition
  - otel-config.yaml    OpenTelemetry Collector configuration
  - prometheus.yaml     Prometheus scrape configuration

The monitoring stack includes:
  - OpenTelemetry Collector
  - OpenSearch
  - OpenSearch Dashboards
  - Prometheus

```
clawker monitor init [flags]
```

### Examples

```
  # Initialize monitoring configuration
  clawker monitor init

  # Overwrite existing configuration
  clawker monitor init --force
```

### Options

```
  -f, --force   Overwrite existing configuration files
  -h, --help    help for init
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker monitor](clawker_monitor) - Manage local observability stack
