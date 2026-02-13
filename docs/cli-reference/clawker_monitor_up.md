## clawker monitor up

Start the monitoring stack

### Synopsis

Starts the monitoring stack using Docker Compose.

This launches the following services:
  - OpenTelemetry Collector (ports 4317, 4318)
  - Jaeger UI (port 16686)
  - Prometheus (port 9090)
  - Grafana (port 3000)

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

* [clawker monitor](clawker_monitor.md) - Manage local observability stack
