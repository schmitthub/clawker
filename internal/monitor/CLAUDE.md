# Monitor Package

Templates for the monitoring stack (Docker Compose + OTEL Collector pipeline + Prometheus). Backed by OpenSearch (logs + traces) and Prometheus (metrics). OpenSearch Dashboards is the UI for logs/traces; Prometheus has its own UI for metrics.

Service hostnames (`otel-collector`, `prometheus`, `opensearch-node`, `opensearch-dashboards`) live in `internal/consts/consts.go` as `MonitoringServiceHostnames` and are also wired into CoreDNS's `internalHosts` forward zones — single source of truth across the compose plane and the firewall plane.

## Template Files

Template source files live in `templates/`. Every file is a Go `text/template` (`.tmpl`) rendered via `RenderTemplate` before being written to disk.

| Embedded Variable | Source File |
|-------------------|-------------|
| `ComposeTemplate` | `templates/compose.yaml.tmpl` |
| `OtelConfigTemplate` | `templates/otel-config.yaml.tmpl` |
| `PrometheusTemplate` | `templates/prometheus.yaml.tmpl` |

## Filename Constants

Output filenames for writing rendered content to disk:

| Constant | File |
|----------|------|
| `ComposeFileName` | `compose.yaml` |
| `OtelConfigFileName` | `otel-config.yaml` |
| `PrometheusFileName` | `prometheus.yaml` |

## Image Constants

All pinned to a multi-arch manifest list digest (`@sha256:`). Verify with `docker buildx imagetools inspect <pin>` before bumping.

| Constant | Image |
|----------|-------|
| `OtelCollectorImage` | `otel/opentelemetry-collector-contrib:0.148.0` |
| `PrometheusImage` | `prom/prometheus:v3.10.0` |
| `OpenSearchImage` | `opensearchproject/opensearch:3.6.0` |
| `OpenSearchDashboardsImage` | `opensearchproject/opensearch-dashboards:3.6.0` |

## Template Rendering

### MonitorTemplateData

Struct providing values for `{{.FieldName}}` placeholders. Service hostname fields (`OtelCollectorService`, `PrometheusService`, `OpenSearchNodeService`, `OpenSearchDashboardsService`) are populated from `consts.MonitoringService*` so renaming a service in consts propagates everywhere.

| Field | Type | Description |
|-------|------|-------------|
| `OtelCollectorPort` | `int` | OTEL collector HTTP port (default 4318) |
| `OtelGRPCPort` | `int` | OTEL collector gRPC port (default 4317; independent of HTTP) |
| `OtelCPPort` | `int` | mTLS-gated host-loopback receiver port for clawker-cp push |
| `PrometheusPort` | `int` | Prometheus UI port (default 9090) |
| `PrometheusMetricsPort` | `int` | Prometheus scrape port the collector exposes (default 8889) |
| `OpenSearchPort` | `int` | OpenSearch REST API port (default 9200) |
| `OpenSearchDashboardsPort` | `int` | OpenSearch Dashboards UI port (default 5601) |
| `OpenSearchHeapMB` | `int` | JVM `-Xms`/`-Xmx` for the OpenSearch node (default 512) |
| `OtelCollectorService` | `string` | Hostname for OTEL collector — from `consts.MonitoringServiceOtelCollector` |
| `PrometheusService` | `string` | Hostname for Prometheus — from `consts.MonitoringServicePrometheus` |
| `OpenSearchNodeService` | `string` | Hostname for OpenSearch node — from `consts.MonitoringServiceOpenSearchNode` |
| `OpenSearchDashboardsService` | `string` | Hostname for OpenSearch Dashboards — from `consts.MonitoringServiceOpenSearchDashboards` |
| `OtelServerCertHostPath` / `OtelServerKeyHostPath` / `OtelCAHostPath` | `string` | Host paths to CLI-issued mTLS material gating the CP-only OTLP receiver (empty disables) |
| `HostFilesystem` | `string` | Host path mounted at `/hostfs` for the hostmetrics receiver. Hardcoded `/` |
| `DockerSocketPath` | `string` | Host path to the Docker daemon socket; mounted at `/var/run/docker.sock` for the docker_stats receiver. Sourced from `Settings.Docker.Socket` |
| `OtelCollectorImage` / `PrometheusImage` / `OpenSearchImage` / `OpenSearchDashboardsImage` | `string` | Pinned image refs |

### NewMonitorTemplateData(s *config.Settings) MonitorTemplateData

Constructor that populates `MonitorTemplateData` from full `config.Settings`. Service hostnames come from the consts package; ports + heap come from `s.Monitoring`; `DockerSocketPath` comes from `s.Docker.Socket`; `HostFilesystem` is hardcoded `/`.

### RenderTemplate(name, tmplContent string, data MonitorTemplateData) (string, error)

Renders a Go `text/template` string with the given `MonitorTemplateData`. Returns the rendered string or an error if parsing/execution fails.

## Usage

The `monitor init` command constructs `MonitorTemplateData` via `NewMonitorTemplateData`, then calls `RenderTemplate` for each `.tmpl` template before writing the rendered output to disk.

All symbols are in `templates.go`.

## OTEL Pipelines (otel-config.yaml.tmpl)

| Pipeline | Receivers | Exporters |
|----------|-----------|-----------|
| `traces` | `otlp` | `opensearch/traces` (SS4O — dataset=traces, namespace=clawker), `spanmetrics` (→ metrics pipeline), `debug` |
| `metrics` | `otlp`, `prometheus/self` (collector self-scrape on :8888), `docker_stats` (unix socket), `hostmetrics` (`/hostfs`), `spanmetrics` (RED from traces) | `prometheus` (scrape endpoint on `PrometheusMetricsPort`), `debug` |
| `logs/agents` | `otlp` (resource/agent stamps `ingest_source=agent`) | `opensearch/logs` (index `clawker-logs`), `debug` |
| `logs/cp` (conditional on `OtelCPPort`) | `otlp/cp` (mTLS-gated; resource/cp stamps `service.name=clawker-cp` + `ingest_source=cp`) | `opensearch/logs`, `debug` |

`docker_stats` reads container metrics from the Docker daemon socket bind-mounted RO at `/var/run/docker.sock` (host path from `Settings.Docker.Socket`). `hostmetrics` reads cpu/disk/load/filesystem/memory/network/paging/process metrics from `/hostfs` (host path hardcoded to `/`; the Linux VM root on Docker Desktop).

`spanmetrics` is a connector — traces flow through it and re-emerge as RED (rate / errors / duration) metrics on the metrics pipeline. `prometheus/self` scrapes the collector's own telemetry endpoint so operational metrics for the collector itself land in Prometheus alongside agent telemetry. `debug` writes every batch to the collector's stdout — surfaces in `docker logs clawker-otel-collector`, verbose by design.

The `otlp` HTTP receiver has CORS allow-all wildcards (`http://*`, `https://*`) so browser-based UIs (OpenSearch Dashboards, future SPAs) can POST OTLP/HTTP directly.

OpenSearch's security plugin is disabled in the compose template (`DISABLE_SECURITY_PLUGIN=true`) so the collector talks plain HTTP to it on the docker network. OpenSearch Dashboards runs with its security plugin disabled too — no login required for local development.
