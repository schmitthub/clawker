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
Claude Code → OTLP (http/protobuf) → otel-collector ──┬─→ OpenSearch (logs + traces)
                                                       └─→ Prometheus (metrics)
```

- **Dockerfile template** (`bundler/assets/Dockerfile.tmpl`) sets OTEL env vars at build time
- **`env.go`** (`internal/docker/env.go`) adds runtime `OTEL_RESOURCE_ATTRIBUTES` and disables telemetry when monitoring is inactive
- **OpenSearch** receives logs via the `opensearch/logs` exporter (index `clawker-logs`) and traces via `opensearch/traces` using the SS4O (Simple Schema for Observability) data model
- **Prometheus** receives metrics via the `prometheus` exporter on `prometheus_metrics_port`
- **OpenSearch Dashboards** is the UI for logs + traces; Prometheus has its own UI for metrics

## Service Hostnames Are Constants

Service hostnames (`otel-collector`, `prometheus`, `opensearch-node`, `opensearch-dashboards`) live in `internal/consts/consts.go` as `MonitoringServiceHostnames`. The compose template service keys, the OTEL exporter endpoints, and the CoreDNS `internalHosts` forward zones all reference these constants. Renaming a service in one place would silently break the others — there is no per-config knob for these names.

## OpenSearch Data Model

- **Logs**: index `clawker-logs`. Attributes flow through as document fields. `ingest_source` distinguishes agent (`agent`) from CP (`cp`) telemetry; both streams land in the same index and are filterable.
- **Traces**: SS4O dataset `traces` / namespace `clawker` (per `opensearch/traces` exporter config). Use the Trace Analytics view in OpenSearch Dashboards to inspect spans.
- **Security plugin disabled** for local development (`DISABLE_SECURITY_PLUGIN=true` + `DISABLE_SECURITY_DASHBOARDS_PLUGIN=true`). HTTP, no auth.

## Egress Traffic Logs

Firewall containers (Envoy + CoreDNS) emit structured access logs to stdout. The OTEL collector ingests them and ships to the same OpenSearch `clawker-logs` index alongside agent telemetry.

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
