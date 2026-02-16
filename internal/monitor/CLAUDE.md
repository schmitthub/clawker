# Monitor Package

Templates for the monitoring stack (Docker Compose, OTEL, Grafana, Prometheus).

## Template Files

Template source files live in `templates/`. Files with `.tmpl` extension are Go `text/template` format and must be rendered via `RenderTemplate` before writing to disk. Files without `.tmpl` are static and written as-is.

| Embedded Variable | Source File | Templated |
|-------------------|-------------|:---------:|
| `ComposeTemplate` | `templates/compose.yaml.tmpl` | Yes |
| `OtelConfigTemplate` | `templates/otel-config.yaml.tmpl` | Yes |
| `GrafanaDatasourcesTemplate` | `templates/grafana-datasources.yaml.tmpl` | Yes |
| `PrometheusTemplate` | `templates/prometheus.yaml.tmpl` | Yes |
| `GrafanaDashboardsTemplate` | `templates/grafana-dashboards.yaml` | No |
| `GrafanaDashboardTemplate` | `templates/grafana-dashboard.json` | No |

## Filename Constants

Output filenames for writing rendered content to disk:

| Constant | File |
|----------|------|
| `ComposeFileName` | `compose.yaml` |
| `OtelConfigFileName` | `otel-config.yaml` |
| `GrafanaDatasourcesFileName` | `grafana-datasources.yaml` |
| `PrometheusFileName` | `prometheus.yaml` |
| `GrafanaDashboardsFileName` | `grafana-dashboards.yaml` |
| `GrafanaDashboardFileName` | `grafana-dashboard.json` |

## Template Rendering

### MonitorTemplateData

Struct providing values for `{{.FieldName}}` placeholders in `.tmpl` files:

| Field | Type | Description |
|-------|------|-------------|
| `OtelCollectorPort` | `int` | OTEL collector HTTP port |
| `OtelGRPCPort` | `int` | OTEL collector gRPC port (always `OtelCollectorPort - 1`) |
| `LokiPort` | `int` | Loki port |
| `PrometheusPort` | `int` | Prometheus port |
| `JaegerPort` | `int` | Jaeger UI port |
| `GrafanaPort` | `int` | Grafana port |
| `PrometheusMetricsPort` | `int` | Prometheus metrics scrape port |
| `OtelCollectorInternal` | `string` | Internal OTEL collector address |

### NewMonitorTemplateData(cfg *config.MonitoringConfig) MonitorTemplateData

Constructor that populates `MonitorTemplateData` from a `config.MonitoringConfig`. Derives `OtelGRPCPort` as `OtelCollectorPort - 1`.

### RenderTemplate(name, tmplContent string, data MonitorTemplateData) (string, error)

Renders a Go `text/template` string with the given `MonitorTemplateData`. Returns the rendered string or an error if parsing/execution fails.

## Usage

The `monitor init` command constructs `MonitorTemplateData` via `NewMonitorTemplateData`, then calls `RenderTemplate` for each `.tmpl` template before writing the rendered output to disk. Static templates (`GrafanaDashboardsTemplate`, `GrafanaDashboardTemplate`) are written directly without rendering.

All symbols are in `templates.go`.
