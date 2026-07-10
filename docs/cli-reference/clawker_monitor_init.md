---
title: "clawker monitor init"
---

## clawker monitor init

Scaffold monitoring configuration files

### Synopsis

Scaffolds the monitoring stack configuration files.

This command generates:
  - compose.yaml        Docker Compose stack definition
  - otel-config.yaml    OpenTelemetry Collector configuration
  - prometheus.yaml     Prometheus scrape configuration
  - opensearch-bootstrap/  index templates, ISM policies, and saved objects

The rendered collector config and bootstrap tree reflect this project's
selected monitoring extensions (`monitor.extensions`). 'monitor init' is
optional — 'monitor up' renders the same files itself — but it lets you inspect
or pre-generate the config without starting the stack.

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
