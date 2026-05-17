# Monitor Package

Templates for the monitoring stack (Docker Compose + OTEL Collector pipeline + Prometheus). Backed by OpenSearch (logs + traces) and Prometheus (metrics). OpenSearch Dashboards is the UI for logs/traces; Prometheus has its own UI for metrics.

Service hostnames live in `internal/consts/monitoring.go`. `MonitoringServiceHostnames` (`otel-collector`, `prometheus`) is the subset wired into CoreDNS's `internalHosts` forward zones — only services agent containers legitimately need to dial. `opensearch-node` and `opensearch-dashboards` are intentionally omitted: agents never query indices directly, so those containers reach each other via Docker's embedded resolver without going through CoreDNS.

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

Each port field drives **both** sides of the host:container publish mapping AND the container's own listener config (Prometheus `--web.listen-address`, OpenSearch `http.port` env, Dashboards `SERVER_PORT` env, otel-collector receiver endpoints in `otel-config.yaml.tmpl`). Changing one setting moves host + internal together so users can shuffle ports to avoid host conflicts without diverging from the image's listener.

| Field | Type | Description |
|-------|------|-------------|
| `OtelCollectorPort` | `int` | OTEL collector OTLP/HTTP port (default 4318) |
| `OtelGRPCPort` | `int` | OTEL collector gRPC port (default 4317; independent of HTTP) |
| `OtelInfraPort` | `int` | mTLS-gated host-loopback receiver port for trusted infra push (clawker-cp + firewall Envoy + CoreDNS — all share this lane) |
| `PrometheusPort` | `int` | Prometheus UI port (default 9090) — wired into `--web.listen-address` |
| `PrometheusMetricsPort` | `int` | Prometheus scrape port the collector exposes (default 8889) |
| `OpenSearchPort` | `int` | OpenSearch REST API port (default 9200) — wired into `http.port` env |
| `OpenSearchDashboardsPort` | `int` | OpenSearch Dashboards UI port (default 5601) — wired into `SERVER_PORT` env |
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

**Metrics default path: OTLP push → otel-collector → Prometheus scrape**. Clients targeting `cfg.OtelMetricsEndpoint()` hit the collector's OTLP/HTTP receiver. The collector's `transform/metrics` processor copies resource attrs (project, agent) to datapoint attributes, then the `prometheus` exporter exposes a scrape endpoint on `PrometheusMetricsPort` that Prometheus scrapes. Prometheus' native OTLP receiver is also enabled (`--web.enable-otlp-receiver`, `--enable-feature=otlp-deltatocumulative`, `otlp.promote_resource_attributes` in prometheus.yaml) and `cfg.PrometheusURL() + Telemetry.PrometheusOTLPPath` returns its URL — direct OTLP push works and saves a hop, but Prometheus' `/api/v1/metadata` excludes anything ingested via OTLP/remote-write (upstream limitation), so consumers depending on metric metadata (OpenSearch Dashboards' Observability Metrics catalog, etc.) silently miss those metrics. Route via the collector unless metadata-blindness is acceptable.

| Pipeline | Receivers | Exporters |
|----------|-----------|-----------|
| `traces` | `otlp` | `opensearch/traces` (SS4O — dataset=traces, namespace=clawker), `spanmetrics` (→ metrics pipeline), `debug` |
| `metrics` | `otlp` (default path for Claude Code + CP metrics push; direct Prometheus OTLP push is the documented alternate), `prometheus/self` (collector self-scrape on :8888), `docker_stats` (unix socket), `hostmetrics` (`/hostfs`), `spanmetrics` (RED from traces) | `prometheus` (scrape endpoint on `PrometheusMetricsPort` — Prometheus scrapes this), `debug` |
| `logs/in_untrusted` | `otlp` (no client auth) | `routing/untrusted` connector — only `service.name=claude-code` and `service.name=clawker-cli` route downstream; everything else is dropped |
| `logs/claude-code` | `routing/untrusted` (when `service.name=claude-code`) | `opensearch/logs_claude_code` (index `claude-code`), `debug` |
| `logs/clawker-cli` | `routing/untrusted` (when `service.name=clawker-cli` — stamped by `internal/logger.newOtelProvider` Resource when called with `OtelOptions.ServiceName="clawker-cli"`; wired in `internal/cmd/factory/default.go`) | `opensearch/logs_clawker_cli` (index `clawker-cli`), `debug` |
| `logs/in_trusted` *(trusted block, see note)* | `otlp/infra` (mTLS-gated; infra intermediate CA is `client_ca_file` — agent leaves chained to the CLI root cannot complete the handshake) | `routing/trusted` connector — dispatches by sender-declared `service.name` |
| `logs/cp` *(trusted block, see note)* | `routing/trusted` (when `service.name=clawker-cp`); `resource/cp` stamps `ingest_source=cp` post-routing | `opensearch/logs_cp` (index `clawker-cp`), `debug` |
| `logs/envoy` *(trusted block, see note)* | `routing/trusted` (when `service.name=envoy` — stamped by Envoy ALS via the `otel_collector_als` upstream cluster's mTLS path); `resource/envoy` stamps `ingest_source=envoy` post-routing | `opensearch/logs_envoy` (index `clawker-envoy`), `debug` |
| `logs/coredns` *(trusted block, see note)* | `routing/trusted` (when `service.name=coredns` — stamped by the in-tree CoreDNS `otel` plugin's OTLP/gRPC + mTLS push); `resource/coredns` stamps `ingest_source=coredns` post-routing | `opensearch/logs_coredns` (index `clawker-coredns`), `debug` |

> **Trusted block conditionality**: the `otlp/infra` receiver + all four trusted pipelines (`logs/in_trusted`, `logs/cp`, `logs/envoy`, `logs/coredns`) are **always rendered** into `otel-config.yaml` — the template has no `{{ if }}` gate and `monitor init` always mints + mounts the collector server cert at `/etc/otel/tls/`. Degradation is sender-side only: when the infra issuer isn't available or per-sender cert minting fails, the receiver keeps listening but no trusted client can complete the mTLS handshake. Envoy drops the OTel access-log sink + `otel_collector_als` cluster (gated at the sender via `als.MTLS` in `buildHTTPAccessLog` / `buildTCPAccessLog` / `buildClusters`), keeping only the stdout JSON sink for `docker logs clawker-envoy` triage; the CoreDNS otel plugin installs `noopEmitter` (no `CLAWKER_COREDNS_OTEL_ENDPOINT` env var); the CP's mTLS log shipper stays cold. Infra services never push OTLP across the untrusted `otel-collector:4317` lane reserved for agent containers — the infra ingestion path is closed end-to-end at the sender.

**Index split rationale**: clawker's zerolog pattern (`Str("event", "name")`) emits `attributes.event` as a scalar string. Claude Code follows OTEL semantic conventions and emits `attributes.event.name` (nested). Envoy ALS attributes carry HTTP request fields (method, path, response_code) flat on the record. CoreDNS query records (emitted by the in-tree `otel` plugin, see below) carry DNS-specific attributes (`query_name`, `qtype`, `rcode`, `answer_count`, `duration_ms`, `answers`) flat on the record. OpenSearch dynamic mapping locks the first-seen shape per field, so sharing one index would reject whichever source loses the race. Splitting by source keeps each schema clean. The `ingest_source` resource attribute is stamped on every record for cross-index queries via the pattern `clawker-cp,claude-code,clawker-envoy,clawker-coredns`.

**Envoy access logs**: Envoy ships records via the native `envoy.access_loggers.open_telemetry` sink to the collector's mTLS-gated `otlp/infra` receiver. Resource attribute `service.name=envoy` is set on the Envoy side (see `firewall/envoy_config.go::otelAccessLogEntry`), and the cluster `otel_collector_als` (parameterized by `ALSConfig` — `MTLS=true` dials `OtelInfraPort` with an upstream TLS transport_socket using the CLI-CA-chained leaf bind-mounted under `/etc/envoy/otel-tls/`; `MTLS=false` causes the OTel access-log sink AND the `otel_collector_als` cluster to be omitted entirely at the sender — gated in `buildHTTPAccessLog` / `buildTCPAccessLog` / `buildClusters` — so infra services never cross into the untrusted `otel-collector:4317` lane reserved for agent containers) handles the gRPC connection. The legacy `envoy.access_loggers.stdout` sink is kept alongside for `docker logs clawker-envoy` triage when the monitoring stack is down (and is the sole access-log sink in degraded mode).

**CoreDNS query logs**: ships via the in-tree `otel` CoreDNS plugin (`cmd/coredns-clawker/plugins/otel/`) which emits one structured `dns.query` OTLP log record per query (OTLP/gRPC + mTLS) to the collector's `otlp/infra` receiver. The plugin is the **first** directive in every server block (set in `cmd/coredns-clawker/main.go`) so it observes the final rcode + answer set after `forward`/`template`/etc. Endpoint host:port is wired by `firewall.Stack` via `CLAWKER_COREDNS_OTEL_ENDPOINT`; CLI-CA-chained leaf is bind-mounted at `/etc/clawker/auth/coredns/client.{pem,key}` + the CA at `/etc/clawker/auth/coredns/ca.pem`. Leaves are issued + rotated by `internal/controlplane/infracerts`; `tls.Config.GetClientCertificate` re-reads the leaf on every handshake so rotation requires no container restart. Each record carries `event.name=dns.query` plus attributes `source=coredns`, `client_ip`, `zone`, `query_name`, `qtype`, `rcode`, `answer_count`, `duration_ms`, and (when non-empty) `answers`. NXDOMAIN comes through with `rcode=NXDOMAIN`; resolver errors set `record.SetErr(...)` with `rcode=SERVFAIL`. The stdout `log` plugin is kept alongside for `docker logs clawker-coredns` triage when the monitoring stack is down — it is no longer scraped into OpenSearch.

`docker_stats` reads container metrics from the Docker daemon socket bind-mounted RO at `/var/run/docker.sock` (host path from `Settings.Docker.Socket`). `hostmetrics` reads cpu/disk/load/filesystem/memory/network/paging/process metrics from `/hostfs` (host path hardcoded to `/`; the Linux VM root on Docker Desktop).

`spanmetrics` is a connector — traces flow through it and re-emerge as RED (rate / errors / duration) metrics on the metrics pipeline. `prometheus/self` scrapes the collector's own telemetry endpoint so operational metrics for the collector itself land in Prometheus alongside agent telemetry. `debug` writes every batch to the collector's stdout — surfaces in `docker logs clawker-otel-collector`, verbose by design.

The `otlp` HTTP receiver has no `cors` block — browser-based pushers will be rejected by preflight. OTLP/HTTP from server-side clients works fine; if a future SPA needs to push directly, add a `cors.allowed_origins` entry scoped to that origin to the receiver in `otel-config.yaml.tmpl`.

OpenSearch's security plugin is disabled in the compose template (`DISABLE_SECURITY_PLUGIN=true`) so the collector talks plain HTTP to it on the docker network. OpenSearch Dashboards runs with its security plugin disabled too — no login required for local development.
