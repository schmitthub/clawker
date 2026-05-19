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

- **Default metrics path**: clients are wired with `OTEL_EXPORTER_OTLP_ENDPOINT=cfg.OtelCollectorURL()` (`http://otel-collector:4318`); the OTel SDK appends `/v1/metrics` per signal. Collector's `transform/metrics` processor copies resource attrs (project, agent) to datapoint attributes, prometheus exporter on `PrometheusMetricsPort` exposes a scrape endpoint, Prometheus scrapes it. This is the default because Prom's `/api/v1/metadata` excludes OTLP/remote-write ingested metrics (upstream limitation) — anything depending on metadata (e.g. OpenSearch Dashboards' Observability Metrics catalog) will miss direct-push metrics.
- **Alternate metrics path** (direct to Prom OTLP receiver): `cfg.PrometheusURL() + Telemetry.PrometheusOTLPPath` (default `/api/v1/otlp/v1/metrics`). Saves a hop. Prometheus runs with `--web.enable-otlp-receiver` + `--enable-feature=otlp-deltatocumulative` and `prometheus.yaml` has an `otlp.promote_resource_attributes` block (`project`, `agent`, `service.name`, `service.version`) so labels still land. Use when metadata-blindness is acceptable.
- **Logs path (untrusted)**: agent containers share the `OTEL_EXPORTER_OTLP_ENDPOINT=cfg.OtelCollectorURL()` base; the OTel SDK appends `/v1/logs`. Host CLI hits `OtelCollectorHost:OtelGRPCPort` (plaintext OTLP/gRPC). Collector's `routing/untrusted` connector forwards `service.name=claude-code` → `claude-code` index AND `service.name=clawker-cli` → `clawker-cli` index. Everything else is dropped. Spoofed `service.name=envoy` or `=clawker-cp` from this lane goes nowhere — those land via the trusted (mTLS) lane only.
- **Traces path (untrusted, Claude Code beta)**: Claude Code exports spans when both `CLAUDE_CODE_ENABLE_TELEMETRY=1` and `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1` are set (both baked into `Dockerfile.tmpl`). `OTEL_TRACES_EXPORTER=otlp` pairs with the shared base endpoint — SDK appends `/v1/traces`. Spans land in the `traces` pipeline → `opensearch/traces` exporter (SS4O `traces`/`clawker`) + `spanmetrics` connector → RED metrics on the Prom side. Span hierarchy: `claude_code.interaction` → `claude_code.llm_request` / `claude_code.tool` (`tool.blocked_on_user` + `tool.execution`) / `claude_code.hook`. `OTEL_LOG_USER_PROMPTS=1` and `OTEL_LOG_TOOL_DETAILS=1` (both default-on) unredact prompt text + tool input details onto spans; `OTEL_LOG_TOOL_CONTENT` is intentionally NOT set so tool output bodies stay redacted.
- **Trusted infra logs (CP / Envoy / CoreDNS)**: pushed to the mTLS-gated `otlp/infra` receiver (gRPC only — no HTTP listener configured; CP/Envoy/CoreDNS clients must use OTLP/gRPC). Receiver `client_ca_file` is the **infra intermediate CA** (NOT the CLI root). CP, Envoy, and CoreDNS all present short-lived leaves signed by that intermediate, minted via `internal/controlplane/otelcerts.Service` (CP's leaf is built in-process per handshake; Envoy/CoreDNS leaves are written to disk at `firewall.Stack.EnsureRunning` and bind-mounted into the sibling containers). Agent leaves are CLI-root-direct (`auth.MintAgentCert`) and do NOT chain through the intermediate — their handshake fails the receiver's chain validation. The trust anchor MUST stay on the infra intermediate: if the receiver accepted CLI-root-signed leaves, any agent could forge `service.name=clawker-cp` records on the trusted forensic indices. `routing/trusted` dispatches by sender-declared `service.name` (CP → `clawker-cp`, envoy → `clawker-envoy`, coredns → `clawker-coredns`). `service.name` is NOT force-overwritten — mTLS handshake is the auth boundary; trusted peers' self-declared identity is honored. `resource/*` processors stamp `ingest_source` for cross-index queries via the pattern `clawker-cp,claude-code,clawker-envoy,clawker-coredns`.
- **Adding a new trusted infra source**: `EnsureClient(name)` for sibling containers that must bind-mount disk-resident material; `LoadTLSConfig(name)` only for callers running inside the CP process (the closure holds the Service reference, which is not transportable across process boundaries). Then bind-mount or wire the cert into the new container and add the matching `condition: attributes["service.name"] == "<name>"` branch to `routing/trusted` + the per-source pipeline + OpenSearch exporter in `otel-config.yaml.tmpl`. No CLI release required.
- **URL composition**: build endpoints via the `cfg.*Endpoint()` / `cfg.*URL()` accessors in `internal/config/consts.go` — never hand-concatenate host + port + path.
- **`bundler/assets/Dockerfile.tmpl`** bakes the endpoint env vars at build time. `internal/docker/env.go` adds runtime `OTEL_RESOURCE_ATTRIBUTES` and overrides `CLAUDE_CODE_ENABLE_TELEMETRY=0` when the monitoring stack isn't running.
- **OpenSearch Dashboards** is the UI for logs + traces; Prometheus has its own UI for metrics.

## Service Hostnames Are Constants

Service hostnames live in `internal/consts/monitoring.go` as four individual constants (`MonitoringServiceOtelCollector`, `MonitoringServicePrometheus`, `MonitoringServiceOpenSearchNode`, `MonitoringServiceOpenSearchDashboards`). The compose template service keys, the OTEL exporter endpoints, and the CoreDNS `internalHosts` forward zones all reference these constants — renaming a service in one place propagates everywhere without further edits. `MonitoringServiceHostnames` is a slice containing only `otel-collector` and `prometheus` — the two hostnames agent containers legitimately dial. OpenSearch and OpenSearch Dashboards are intentionally excluded: agents push telemetry through the collector and never query indices directly; those services reach each other via Docker's embedded resolver without going through CoreDNS.

## OpenSearch Bootstrap

Cluster ships preconfigured: index templates, ISM policies, Dashboards saved objects all applied on every fresh `monitor up`. See `internal/monitor/CLAUDE.md` → "OpenSearch Bootstrap" for the source-tree layout and full API breakdown. Mechanics worth knowing here:

- `clawker-opensearch-bootstrap` is a one-shot compose service (image: `curlimages/curl`, Alpine + curl + sh) that runs after `opensearch-node` reports `service_healthy` and exits 0 when done.
- `otel-collector` gates on `clawker-opensearch-bootstrap: service_completed_successfully` — it never starts until index templates + ISM + saved objects are applied. Prometheus starts in parallel; bootstrap depends on Prometheus (`service_started`) so the `clawker_prometheus` datasource registration can validate the configured URI. Bootstrap failure means the stack is half-up by design; logs are in `docker logs clawker-opensearch-bootstrap`.
- The script polls Dashboards `/api/status` internally before doing saved-objects work; no Dashboards healthcheck in compose.
- Templates apply only at index creation. Editing an index template + re-running `monitor up` does NOT re-map existing indices. The throwaway-stack model expects `monitor down --volumes && monitor up` for changes to take effect cluster-wide.
- Index template + ISM PUTs are idempotent; saved-objects `_import` uses `?overwrite=true`.

## OpenSearch Data Model

- **Logs**: split across five indices to keep dynamic mappings clean — `claude-code` (Claude Code OTLP push, nested `attributes.event.name`), `clawker-cli` (host CLI zerolog push via the untrusted lane, scalar `attributes.event`), `clawker-cp` (clawker-cp's mTLS zerolog push, scalar `attributes.event`), `clawker-envoy` (Envoy native OTLP access logs, flat HTTP/TLS/TCP fields), and `clawker-coredns` (CoreDNS query records emitted by the in-tree `otel` plugin over OTLP/gRPC + mTLS, structured `dns.query` attributes). All five carry explicit field mappings via the index templates rendered by `monitor init` (see `internal/monitor/CLAUDE.md` → "OpenSearch Bootstrap"). `ingest_source` is stamped on the cp / envoy / coredns indices via `resource/*` processors; Claude Code and CLI records carry `service.name=claude-code` / `service.name=clawker-cli` natively. Cross-index queries use pattern `clawker-cp,claude-code,clawker-cli,clawker-envoy,clawker-coredns`.
- **Traces**: SS4O dataset `traces` / namespace `clawker` (per `opensearch/traces` exporter config). Use the Trace Analytics view in OpenSearch Dashboards to inspect spans.
- **Security plugin disabled** for local development (`DISABLE_SECURITY_PLUGIN=true` + `DISABLE_SECURITY_DASHBOARDS_PLUGIN=true`). HTTP, no auth.

## Egress Traffic Logs

Envoy and CoreDNS access logs are scraped into OpenSearch with dedicated indices so each shape gets a clean dynamic mapping.

### Envoy (`service.name="envoy"`, index `clawker-envoy`)
- Ships via the native `envoy.access_loggers.open_telemetry` sink (OTLP/gRPC) to the collector's mTLS-gated `otlp/infra` receiver. The cluster `otel_collector_als` (defined in `firewall/envoy_config.go::buildOtelALSCluster`) dials `OtelInfraPort` with an upstream TLS transport_socket using the CLI-CA-chained leaf bind-mounted under `/etc/envoy/otel-tls/`. When the infra CA isn't wired (cert mint failure or no issuer), the OTel sink AND cluster are both omitted at the sender — Envoy keeps only the stdout JSON sink for `docker logs` triage. Infra services never push OTLP to the untrusted `otel-collector:4317` lane reserved for agent containers.
- Resource attribute `service.name=envoy` is stamped on the Envoy side by `otelAccessLogEntry`. `routing/trusted` dispatches the record to `logs/envoy`; `resource/envoy` stamps `ingest_source=envoy` post-routing.
- The legacy `envoy.access_loggers.stdout` JSON sink is kept alongside for `docker logs clawker-envoy` triage when the monitoring stack is down.
- Structured fields land on OTLP attributes: `domain`, `proto` (tls/http/deny/tcp/ssh), `response_code` (HTTP contexts), `response_flags`, `method`, `path`, `request_host`. `response_flags` containing `UF` (upstream failure) indicates blocked/denied traffic.

### CoreDNS (`service.name="coredns"`, index `clawker-coredns`)
- Ships via the in-tree `otel` CoreDNS plugin (`cmd/coredns-clawker/plugins/otel/`) which emits one structured `dns.query` OTLP log record per query (OTLP/gRPC + mTLS) to the collector's `otlp/infra` receiver. The plugin is the **first** directive in every server block (set in `cmd/coredns-clawker/main.go`) so it observes the final rcode + answer set after `forward`/`template`/etc.
- Endpoint is `CLAWKER_COREDNS_OTEL_ENDPOINT` (host:port — no scheme; mTLS is forced by the client TLS config). `firewall.Stack` sets it to `consts.MonitoringServiceOtelCollector` + `Settings.Monitoring.OtelInfraPort` and bind-mounts the CLI-CA-chained leaf at `/etc/clawker/auth/coredns/client.{pem,key}` + the CA at `/etc/clawker/auth/coredns/ca.pem`. Leaves are issued + rotated by `internal/controlplane/infracerts`; `tls.Config.GetClientCertificate` re-reads the leaf on every handshake so rotation requires no container restart.
- `service.name=coredns` is stamped by the plugin's OTel SDK Resource; trust comes from the mTLS handshake at `otlp/infra`, not from the self-declared name. `routing/trusted` dispatches to `logs/coredns`; `resource/coredns` stamps `ingest_source=coredns` post-routing.
- Each record carries `event.name=dns.query`, body `"CoreDNS query handled"`, and attributes `source=coredns`, `client_ip`, `zone`, `query_name`, `qtype`, `rcode`, `answer_count`, `duration_ms`, plus `answers` (slice of strings) when non-empty. `rcode=NXDOMAIN` indicates blocked domain lookups; resolver errors set `record.SetErr(...)` with `rcode=SERVFAIL`.
- The stdout `log` plugin is kept alongside for `docker logs clawker-coredns` triage when the monitoring stack is down — it is no longer scraped into OpenSearch.

**Routing topology**:
- Untrusted: `otlp` receiver (no client auth, plaintext) → `logs/in_untrusted` (`memory_limiter` → `resource/untrusted_otlp` stamps `ingest_source=untrusted_otlp` → `batch`) → `routing/untrusted` connector → `service.name=claude-code` reaches `logs/claude-code` and `service.name=clawker-cli` reaches `logs/clawker-cli`; everything else is dropped (`error_mode: ignore`, no `default_pipelines`). Spoofed `service.name=envoy`/`coredns`/`clawker-cp` from this lane goes nowhere. Metrics and traces on the untrusted lane go through dedicated pipelines (`metrics/untrusted`, `traces`) that also stamp `ingest_source=untrusted_otlp` so dashboards can separate forgeable sender-declared records from records anchored by mTLS handshake.
- Trusted: `otlp/infra` receiver (mTLS, `client_ca_file` = **infra intermediate CA** — not the CLI root, which agents also hold) → `logs/in_trusted` (`memory_limiter` → `batch`) → `routing/trusted` connector (`error_mode: propagate`, `default_pipelines: [logs/trusted_unrouted]`) → dispatches by sender-declared `service.name` to `logs/cp` (`clawker-cp`), `logs/envoy` (`envoy`), or `logs/coredns` (`coredns`). Records with unmapped `service.name` land in `logs/trusted_unrouted` (debug-only — should never fire). mTLS is the auth boundary; `service.name` is honored, not overwritten.
- Resilience: every pipeline begins with `memory_limiter` (compose hard-caps the collector at 200M, so this provides backpressure before OOM-kill). All `opensearch/*` exporters carry `sending_queue.enabled` + `retry_on_failure` so the OpenSearch startup window (collector waits for `service_healthy` via cluster-health endpoint) and short outages don't drop forensic data.

## What NOT To Do

- Don't add hostname knobs to `MonitoringConfig` for monitoring services — they're consts shared with the firewall plane.
