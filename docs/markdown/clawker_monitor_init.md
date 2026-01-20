## clawker monitor init

Scaffold monitoring configuration files

### Synopsis

Scaffolds the monitoring stack configuration files in ~/.clawker/monitor/.

This command generates:
  - compose.yaml        Docker Compose stack definition
  - otel-config.yaml    OpenTelemetry Collector configuration
  - prometheus.yaml     Prometheus scrape configuration
  - grafana-datasources.yaml  Pre-configured Grafana datasources

The monitoring stack includes:
  - OpenTelemetry Collector (receives traces/metrics from Claude Code)
  - Jaeger (trace visualization)
  - Prometheus (metrics storage)
  - Grafana (unified dashboard)

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
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker monitor](clawker_monitor.md) - Manage local observability stack
