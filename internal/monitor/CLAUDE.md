# Monitor Package

Templates for the monitoring stack (Docker Compose, OTEL, Grafana, Prometheus).

## Filename Constants

| Constant | File |
|----------|------|
| `ComposeFileName` | Docker Compose config |
| `OtelConfigFileName` | OpenTelemetry collector config |
| `GrafanaDatasourcesFileName` | Grafana datasource config |
| `PrometheusFileName` | Prometheus config |
| `GrafanaDashboardsFileName` | Grafana dashboard provisioning |
| `GrafanaDashboardFileName` | Grafana dashboard JSON |

## Template Variables

Each filename constant has a corresponding Go `text/template` variable:

- `ComposeTemplate` — Docker Compose stack definition
- `OtelConfigTemplate` — OpenTelemetry collector pipeline config
- `GrafanaDatasourcesTemplate` — Grafana datasource provisioning
- `PrometheusTemplate` — Prometheus scrape config
- `GrafanaDashboardsTemplate` — Grafana dashboard provisioning config
- `GrafanaDashboardTemplate` — Grafana dashboard JSON definition

## Usage

Templates are rendered by the `clawker monitor` commands to set up the observability stack. All symbols are in `templates.go`.
