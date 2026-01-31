# Monitor Package

Templates for the monitoring stack (Docker Compose, OTEL, Grafana, Prometheus).

## Template Constants

| Constant | File |
|----------|------|
| `ComposeFileName` | Docker Compose config |
| `OtelConfigFileName` | OpenTelemetry collector config |
| `GrafanaDatasourcesFileName` | Grafana datasource config |
| `PrometheusFileName` | Prometheus config |
| `GrafanaDashboardsFileName` | Grafana dashboard provisioning |
| `GrafanaDashboardFileName` | Grafana dashboard JSON |

## Template Variables

Each constant has a corresponding `*Template` variable (e.g., `ComposeTemplate`) containing Go `text/template` content for generating the monitoring configuration files.

## Usage

Templates are rendered by the `clawker monitor` commands to set up the observability stack.
