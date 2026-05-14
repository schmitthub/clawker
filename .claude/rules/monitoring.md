---
description: Monitoring stack guidelines (OpenSearch + Prometheus)
paths: ["internal/monitor/**"]
---

# Monitoring Rules

> For event schemas and OpenSearch quirks, see `.claude/docs/MONITORING-REFERENCE.md`.

## Purpose
The monitoring stack provides critical security observability into Clawker's internal operations for end users.

## Telemetry Pipeline

```
OTLP/HTTP push ─┬─→ otel-collector ─┬─→ Prometheus scrape (metrics)
                │                    ├─→ OpenSearch (traces, SS4O)
                │                    └─→ OpenSearch (logs)
                └─→ Prometheus (native OTLP receiver, optional direct push)
```

- **Default metrics path**: clients hit `cfg.OtelMetricsEndpoint()` (`otel-collector:4318/v1/metrics`). Collector's `transform/metrics` processor copies resource attrs (project, agent) to datapoint attributes, prometheus exporter on `PrometheusMetricsPort` exposes a scrape endpoint, Prometheus scrapes it. This is the default because Prom's `/api/v1/metadata` excludes OTLP/remote-write ingested metrics (upstream limitation) — anything depending on metadata (e.g. OpenSearch Dashboards' Observability Metrics catalog) will miss direct-push metrics.
- **Alternate metrics path** (direct to Prom OTLP receiver): `cfg.PrometheusURL() + Telemetry.PrometheusOTLPPath` (default `/api/v1/otlp/v1/metrics`). Saves a hop. Prometheus runs with `--web.enable-otlp-receiver` + `--enable-feature=otlp-deltatocumulative` and `prometheus.yaml` has an `otlp.promote_resource_attributes` block (`project`, `agent`, `service.name`, `service.version`) so labels still land. Use when metadata-blindness is acceptable.
- **Logs path**: clients hit `cfg.OtelLogsEndpoint()` (`otel-collector:4318/v1/logs`). Collector's `routing/logs_by_service` connector dispatches by `resource.service.name`: `claude-code` → `claude-code` OpenSearch index; unmatched is dropped. Add a routing rule + exporter for each new log source.
- **clawker-cp logs**: pushed to the mTLS-gated `otlp/cp` receiver → `clawker-cp` OpenSearch index. Separate index because clawker's zerolog (`Str("event", ...)`) emits `attributes.event` as scalar while Claude Code follows OTEL semantic conventions and emits nested `attributes.event.name` — OpenSearch dynamic mapping locks the first-seen shape per field, so sharing one index would silently reject whichever schema loses the race. Cross-index queries use pattern `clawker-cp,claude-code`.
- **URL composition**: build endpoints via the `cfg.*Endpoint()` / `cfg.*URL()` accessors in `internal/config/consts.go` — never hand-concatenate host + port + path.
- **`bundler/assets/Dockerfile.tmpl`** bakes the endpoint env vars at build time. `internal/docker/env.go` adds runtime `OTEL_RESOURCE_ATTRIBUTES` and overrides `CLAUDE_CODE_ENABLE_TELEMETRY=0` when the monitoring stack isn't running.
- **OpenSearch Dashboards** is the UI for logs + traces; Prometheus has its own UI for metrics.

## Service Hostnames Are Constants

Service hostnames (`otel-collector`, `prometheus`, `opensearch-node`, `opensearch-dashboards`) live in `internal/consts/consts.go` as `MonitoringServiceHostnames`. The compose template service keys, the OTEL exporter endpoints, and the CoreDNS `internalHosts` forward zones all reference these constants. Renaming a service in one place would silently break the others — there is no per-config knob for these names.

## OpenSearch Data Model

- **Logs**: split across two indices to keep dynamic mappings clean — `claude-code` for Claude Code OTLP push (nested `attributes.event.name`, OTEL semantic conventions) and `clawker-cp` for clawker-cp's zerolog push (scalar `attributes.event`, `Str("event", ...)` pattern). `ingest_source` is stamped on each (`claude-code` / `cp`) so cross-index queries via pattern `clawker-cp,claude-code` work.
- **Traces**: SS4O dataset `traces` / namespace `clawker` (per `opensearch/traces` exporter config). Use the Trace Analytics view in OpenSearch Dashboards to inspect spans.
- **Security plugin disabled** for local development (`DISABLE_SECURITY_PLUGIN=true` + `DISABLE_SECURITY_DASHBOARDS_PLUGIN=true`). HTTP, no auth.

## Egress Traffic Logs

Envoy and CoreDNS access logs flow into OpenSearch alongside agent telemetry.

### Envoy (`service.name="envoy"`)
- JSON access log format
- Key fields: `domain`, `proto` (tls/tls_mitm/http/tcp/deny), `response_code`, `response_flags`
- `response_flags` containing `UF` (upstream failure) indicates blocked/denied traffic

### CoreDNS (`service.name="coredns"`)
- Logfmt key=value format (parsed at the collector)
- Key fields: `domain`, `qtype` (A/AAAA), `rcode` (NOERROR/NXDOMAIN), `duration`
- `rcode=NXDOMAIN` indicates blocked domain lookups

## What NOT To Do

- Don't add hostname knobs to `MonitoringConfig` for monitoring services — they're consts shared with the firewall plane.
- Don't bring back Grafana/Loki/Jaeger/Promtail without re-introducing the consts they need, and without auditing CoreDNS + compose template + otel-config + bundler dockerfile generator together.
