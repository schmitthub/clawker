---
description: Monitoring stack guidelines (OpenSearch + Prometheus)
paths:
  - "internal/monitor/**"
  - "cmd/coredns-clawker/plugins/otel/**"
  - "internal/controlplane/firewall/ebpf/netlogger/**"
  - "internal/consts/monitoring.go"
---

# Monitoring Rules

> For event schemas and OpenSearch quirks, see `.claude/docs/MONITORING-REFERENCE.md`.

> **Ground in the live telemetry spec before any monitoring work — do not guess.**
> Claude Code's metric/event surface evolves and is far larger than whatever a
> running stack happens to have emitted. Before designing dashboards, queries,
> ingest pipelines, index templates, or collector config, ALWAYS load the
> upstream spec into context:
> **https://code.claude.com/docs/en/monitoring-usage.md** (the `.md` suffix
> serves raw markdown — fetch that, not the rendered page). It is the source of
> truth for every metric (name, unit, attributes), every event
> (`claude_code.*` name + fields), the standard/identity attributes, and the
> **"Audit security events"** mapping of security signals → events. Live probes
> confirm what is *currently flowing*; the spec tells you what *exists*. Use both
> — never infer a field, label, or event name from memory.

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
- **Logs path (untrusted)**: agent containers share the `OTEL_EXPORTER_OTLP_ENDPOINT=cfg.OtelCollectorURL()` base; the OTel SDK appends `/v1/logs`. Host CLI hits `OtelCollectorHost:OtelGRPCPort` (plaintext OTLP/gRPC). Collector's `routing/untrusted` connector forwards `service.name=clawker-cli` → `clawker-cli` index plus each SEEDED monitoring unit's declared service names → its index (e.g. `claude-code` when a project selects that extension in `monitor.extensions` and seeds it via `monitor up`/`monitor reload`). Everything else is routed to `logs/untrusted_unrouted` (debug-only collector stdout, not indexed). Spoofed `service.name=envoy` or `=clawkercp` from this lane reaches no index — those land via the trusted (mTLS) lane only; unit validation rejects reserved infra service names outright.
- **Traces path (untrusted, Claude Code beta)**: Claude Code exports spans when both `CLAUDE_CODE_ENABLE_TELEMETRY=1` and `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1` are set (both baked into the base template). `OTEL_TRACES_EXPORTER=otlp` pairs with the shared base endpoint — SDK appends `/v1/traces`. Spans land in the `traces` pipeline → `opensearch/traces` exporter (SS4O `traces`/`clawker`) + `spanmetrics` connector → RED metrics on the Prom side. Span hierarchy: `claude_code.interaction` → `claude_code.llm_request` / `claude_code.tool` (`tool.blocked_on_user` + `tool.execution`) / `claude_code.hook`. `OTEL_LOG_USER_PROMPTS=1` and `OTEL_LOG_TOOL_DETAILS=1` (both default-on) unredact prompt text + tool input details onto spans; `OTEL_LOG_TOOL_CONTENT` is intentionally NOT set so tool output bodies stay redacted.
- **Trusted infra logs (CP / Envoy / CoreDNS / netlogger)**: pushed to the mTLS-gated `otlp/infra` receiver (gRPC only — no HTTP listener configured; trusted clients must use OTLP/gRPC). Receiver `client_ca_file` is the **infra intermediate CA** (NOT the CLI root). CP, Envoy, CoreDNS, and netlogger all present short-lived leaves signed by that intermediate, minted via `internal/controlplane/otelcerts.Service` (CP's leaf and netlogger's leaf are built in-process per handshake via `LoadTLSConfig("cp")` / `LoadTLSConfig("netlogger")`; Envoy/CoreDNS leaves are written to disk at `firewall.Stack.EnsureRunning` and bind-mounted into the sibling containers). Agent leaves are CLI-root-direct (`auth.MintAgentCert`) and do NOT chain through the intermediate — their handshake fails the receiver's chain validation. The trust anchor MUST stay on the infra intermediate: if the receiver accepted CLI-root-signed leaves, any agent could forge `service.name=clawkercp` or `service.name=ebpf-egress` records on the trusted forensic indices. `routing/trusted` dispatches by sender-declared `service.name` (CP → `clawkercp`, envoy → `clawker-envoy`, coredns → `clawker-coredns`, ebpf-egress → `clawker-ebpf-egress`). `service.name` is NOT force-overwritten — mTLS handshake is the auth boundary; trusted peers' self-declared identity is honored. `resource/*` processors stamp `ingest_source` for cross-index queries via the pattern `clawkercp,claude-code,clawker-envoy,clawker-coredns,clawker-ebpf-egress`.
- **Adding a new trusted infra source**: `EnsureClient(name)` for sibling containers that must bind-mount disk-resident material; `LoadTLSConfig(name)` only for callers running inside the CP process (the closure holds the Service reference, which is not transportable across process boundaries). Then bind-mount or wire the cert into the new container and add the matching `condition: attributes["service.name"] == "<name>"` branch to `routing/trusted` + the per-source pipeline + OpenSearch exporter in `otel-config.yaml.tmpl`. No CLI release required.
- **URL composition**: build endpoints via the `cfg.*Endpoint()` / `cfg.*URL()` accessors in `internal/config/consts.go` — never hand-concatenate host + port + path.
- **`bundler/assets/Dockerfile.base.tmpl`** bakes the endpoint env vars at build time. `internal/docker/env.go` adds runtime `OTEL_RESOURCE_ATTRIBUTES` and overrides `CLAUDE_CODE_ENABLE_TELEMETRY=0` when the monitoring stack isn't running.
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

- **Logs**: split across six indices to keep dynamic mappings clean — `claude-code` (Claude Code OTLP push, nested `attributes.event.name`), `clawker-cli` (host CLI zerolog push via the untrusted lane, scalar `attributes.event`), `clawkercp` (clawkercp's mTLS zerolog push, scalar `attributes.event`), `clawker-envoy` (Envoy native OTLP access logs, flat HTTP/TLS/TCP fields), `clawker-coredns` (CoreDNS query records emitted by the in-tree `otel` plugin over OTLP/gRPC + mTLS, structured `dns.query` attributes), and `clawker-ebpf-egress` (netlogger's per-decision-point eBPF egress events, `service.name=ebpf-egress`, `event.name=ebpf.egress`). All six carry explicit field mappings via the index templates rendered by `monitor init` (see `internal/monitor/CLAUDE.md` → "OpenSearch Bootstrap"). `ingest_source` is stamped on the cp / envoy / coredns / netlogger indices via `resource/*` processors; Claude Code and CLI records carry `service.name=claude-code` / `service.name=clawker-cli` natively. Cross-index queries use pattern `clawkercp,claude-code,clawker-cli,clawker-envoy,clawker-coredns,clawker-ebpf-egress`.
- **Traces**: SS4O dataset `traces` / namespace `clawker` (per `opensearch/traces` exporter config). Use the Trace Analytics view in OpenSearch Dashboards to inspect spans.
- **Security plugin disabled** for local development (`DISABLE_SECURITY_PLUGIN=true` + `DISABLE_SECURITY_DASHBOARDS_PLUGIN=true`). HTTP, no auth.

## Egress Traffic Logs

Envoy and CoreDNS access logs are scraped into OpenSearch with dedicated indices so each shape gets a clean dynamic mapping.

### Envoy (`service.name="envoy"`, index `clawker-envoy`)
- Ships via the native `envoy.access_loggers.open_telemetry` sink (OTLP/gRPC) to the collector's mTLS-gated `otlp/infra` receiver. The cluster `otel_collector_als` (defined in `firewall/envoy_config.go::buildOtelALSCluster`) dials `OtelInfraPort` with an upstream TLS transport_socket using the CLI-CA-chained leaf bind-mounted under `/etc/envoy/otel-tls/`. When the infra CA isn't wired (cert mint failure or no issuer), the OTel sink AND cluster are both omitted at the sender — Envoy keeps only the stdout JSON sink for `docker logs` triage. Infra services never push OTLP to the untrusted `otel-collector:4317` lane reserved for agent containers.
- Resource attribute `service.name=envoy` is stamped on the Envoy side by `otelAccessLogEntry`. `routing/trusted` dispatches the record to `logs/envoy`; `resource/envoy` stamps `ingest_source=envoy` post-routing.
- The legacy `envoy.access_loggers.stdout` JSON sink is kept alongside for `docker logs clawker-envoy` triage when the monitoring stack is down.
- Structured fields land on OTLP attributes using OTel semantic conventions for network/server/client/tls (not the legacy overloaded `proto` field, which is gone): `network.transport` (always `tcp` today), `network.protocol.name` (`http` for HCM chains; rule's `proto:` value verbatim for opaque TCP listeners; empty for the deny chain), `network.protocol.version` (raw `%PROTOCOL%` — `HTTP/1.1` / `HTTP/2` / `HTTP/3`; HTTP-only, absent on TCP/SSH records), `tls.established` (boolean — Envoy substitution emits `"true"`/`"false"` strings; the `envoy-normalize` ingest pipeline coerces to real boolean), `tls.protocol.version`, `tls.cipher`. Plus identity on OTel canonical names: `server.address` (SNI via `%REQUESTED_SERVER_NAME%` on TLS-MITM HCM + TCP/SSH chains; Host header via `%REQ(Host)%` on the plaintext HCM chain — single consolidated "host the client was trying to reach"; replaces deprecated `tls.server.name`), `client.address` (`%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%`), `network.peer.address`/`network.peer.port` (post-resolution upstream peer). Plus the clawker firewall verdict on `action` (`allowed`/`denied`, stamped from route metadata via `%METADATA(ROUTE:clawker:action)%` for HCM chains and hardcoded for TCP-level filter chains). Plus HTTP-only fields: `method`, `path`, `response_code`, `response_code_details`, `user_agent`, `req_duration_ms`/`resp_duration_ms`/`resp_tx_duration_ms`, `upstream_transport_failure_reason`. Plus Envoy-specific operational fields with no OTel mapping: `listener_ip`, `bytes_sent`/`bytes_received`/`upstream_bytes_sent`/`upstream_bytes_received`, `duration_ms`, `response_flags` (short Envoy codes), `upstream_tls_version`/`upstream_tls_cipher` (upstream MITM re-encryption diagnostic — flat, not nested under `tls.client.*` because OTel deprecated that subtree without leaving a canonical home for upstream-side re-encryption). The OTel collector's `transform/envoy_logs` processor coerces all numeric attributes (`response_code`, `bytes_*`, `*_ms`, `network.peer.port`) from Envoy's string substitution to typed ints before the opensearchexporter, and defaults `severity_text=INFO` / `severity_number=9` since Envoy ALS does not populate severity. **Pruned** (no longer emitted): `source`, `timestamp`, `request_host`, `domain`, `client_ip`, `upstream_ip`, `upstream_port`, `response_flags_long`, `filter_chain_name`, `connection_termination_details`, `upstream_host`, `connection_id`, `stream_id` — all redundant with `resource.service.name`, `@timestamp` envelope, the OTel canonical field, short response_flags, or unused. See `.claude/rules/envoy.md` "Access-log schema" for the full field-by-field rationale. The firewall verdict is read from `action` ONLY — never inferred from `response_code` (upstream-returned 403 vs clawker-blocked 403 distinguished by route metadata, not status code).

### CoreDNS (`service.name="coredns"`, index `clawker-coredns`)
- Ships via the in-tree `otel` CoreDNS plugin (`cmd/coredns-clawker/plugins/otel/`) which emits one structured `dns.query` OTLP log record per query (OTLP/gRPC + mTLS) to the collector's `otlp/infra` receiver. The plugin is the **first** directive in every server block (set in `cmd/coredns-clawker/main.go`) so it observes the final rcode + answer set after `forward`/`template`/etc.
- Endpoint is `CLAWKER_COREDNS_OTEL_ENDPOINT` (host:port — no scheme; mTLS is forced by the client TLS config). `firewall.Stack` sets it to `consts.MonitoringServiceOtelCollector` + `Settings.Monitoring.OtelInfraPort` and bind-mounts the CLI-CA-chained leaf at `/etc/clawker/auth/coredns/client.{pem,key}` + the CA at `/etc/clawker/auth/coredns/ca.pem`. Leaves are issued + rotated by `internal/controlplane/infracerts`; `tls.Config.GetClientCertificate` re-reads the leaf on every handshake so rotation requires no container restart.
- `service.name=coredns` is stamped by the plugin's OTel SDK Resource; trust comes from the mTLS handshake at `otlp/infra`, not from the self-declared name. `routing/trusted` dispatches to `logs/coredns`; `resource/coredns` stamps `ingest_source=coredns` post-routing.
- Each record carries `event.name=dns.query`, body `"CoreDNS query handled"`, and attributes `client.address` (OTel-canonical, replaces colloquial `client_ip`), `zone`, `query_name`, `qtype`, `rcode`, `answer_count`, `duration_ms`, plus `answers` (slice of strings) when non-empty. The colloquial `source=coredns` per-record attribute is gone — `service.name=coredns` (resource layer) + `ingest_source=coredns` (stamped by `resource/coredns` post-routing) cover provenance. There is no `action` attribute: CoreDNS makes no explicit allow/deny decision per query (it forwards or NXDOMAINs by zone), so `rcode` is the honest verdict signal. A prior zone-derived `action` was removed because it was provably wrong — a non-allowlisted subdomain of an exact-allow apex returned NXDOMAIN but logged `action=allowed`. Resolver errors set `record.SetErr(...)` with `rcode=SERVFAIL`. The CoreDNS pie in the networking dashboard keys on `rcode`.
- The stdout `log` plugin is kept alongside for `docker logs clawker-coredns` triage when the monitoring stack is down — it is no longer scraped into OpenSearch.

### netlogger (`service.name="ebpf-egress"`, index `clawker-ebpf-egress`)
- Drains the BPF `events_ringbuf` populated at every cgroup/connect/sendmsg/sock_create decision; userspace enriches each record by cgroup_id with `{container_id, agent, project}` via overseer-bus enrollment events + a one-shot Docker inspect at enrollment time, and resolves the route `identity` to its destination string via the CP IdentityAllocator's live table.
- Emits OTLP log records through netlogger's own `*sdklog.LoggerProvider` (built by `controlplane.NewOtelLoggerProvider`) to the collector's `otlp/infra` receiver. mTLS leaf is minted per-handshake by `otelcerts.Service.LoadTLSConfig("netlogger")` — chains through the **infra intermediate CA**, same trust anchor as CP zerolog. Endpoint MUST be mTLS — plaintext `http://` is rejected at CP boot with `event=netlogger_unavailable` + `step=OTLP endpoint is plaintext`.
- Resource attribute `service.name=ebpf-egress` is stamped by the SDK Resource. `routing/trusted` dispatches to `logs/netlogger`; `resource/netlogger` stamps `ingest_source=netlogger` post-routing.
- `event.name` is per-emit-site: `ebpf.egress.connect` (connect4/connect6), `ebpf.egress.sendmsg` (sendmsg4/sendmsg6), `ebpf.egress.sock_create` (socket() syscall). Encoded in BPF flags bits 3-4 (`EGRESS_EMIT_*`), userspace decodes via `Event.EmitSite`. Each record carries the per-site `event.name` plus attributes `action` (`allowed`/`denied`/`bypassed`), `container_id`, `agent`, `project`, `cgroup_id`, `bpf_ts_ns`, `dst_ip`, `dst_port`, `l4_proto` + `l4_proto_code`, `ipv6`, `ipv4_mapped`, `no_dst`, `dst_host`, `identity`. Strict directive with per-code-path carve-outs: every field is emitted on every record (zero/empty when unset) EXCEPT `dst_ip` (omitted when invalid — sock_create + defensive native-v6), `dst_port` (omitted when `no_dst=true`), and `dst_host` (omitted when Domain is empty — direct-IP connect / unattributed identity). Operators partition via `_exists_:attributes.<key>`. `dst_ip` follows the Cilium / Tetragon address representation: single attribute carrying either an IPv4 dotted-quad or an IPv6 colon-form string (BPF emits a flat 16-byte slot in `struct egress_event.dst_ip`; OS `type: ip` mapping accepts both). No `source` / `component` per-record attributes — routing + provenance live in resource layer (`service.name` + `ingest_source`).
- Failure isolation: BPF token-bucket rate limit per cgroup_id; circuit breaker on the OTel exporter (3 consecutive failures permanently trips for the CP lifetime); preflight TLS dial at CP boot. Degraded paths emit `event=netlogger_unavailable` and leave the firewall enforcement path untouched.

**Routing topology**:
- Untrusted: `otlp` receiver (no client auth, plaintext) → `logs/in_untrusted` (`memory_limiter` → `resource/untrusted_otlp` stamps `ingest_source=untrusted_otlp` → `batch`) → `routing/untrusted` connector → `service.name=clawker-cli` reaches `logs/clawker-cli` and each ACTIVE monitoring unit's service names reach its generated `logs/unit_<id>` pipeline; everything else lands in `logs/untrusted_unrouted` (debug-only, collector stdout; not indexed — records are forgeable) via `default_pipelines`. Spoofed `service.name=envoy`/`coredns`/`clawkercp` from this lane reaches no index. Metrics and traces on the untrusted lane go through dedicated pipelines (`metrics/untrusted`, `traces`) that also stamp `ingest_source=untrusted_otlp` so dashboards can separate forgeable sender-declared records from records anchored by mTLS handshake.
- Trusted: `otlp/infra` receiver (mTLS, `client_ca_file` = **infra intermediate CA** — not the CLI root, which agents also hold) → `logs/in_trusted` (`memory_limiter` → `batch`) → `routing/trusted` connector (`error_mode: propagate`, `default_pipelines: [logs/trusted_unrouted]`) → dispatches by sender-declared `service.name` to `logs/cp` (`clawkercp`), `logs/envoy` (`envoy`), `logs/coredns` (`coredns`), or `logs/netlogger` (`ebpf-egress`). Records with unmapped `service.name` land in `logs/trusted_unrouted` (debug-only — should never fire). mTLS is the auth boundary; `service.name` is honored, not overwritten.
- Resilience: every pipeline begins with `memory_limiter` (compose hard-caps the collector at 200M, so this provides backpressure before OOM-kill). All `opensearch/*` exporters carry `sending_queue.enabled` + `retry_on_failure` so the OpenSearch startup window (collector waits for `service_healthy` via cluster-health endpoint) and short outages don't drop forensic data.

## Runtime UAT (you assist — you cannot unit-test the stack)

Golden files + template-render tests prove the generated compose/otel-config/bootstrap JSON is **valid**. They do NOT prove the live pipeline **ingests, routes, indexes, and renders**. There is no unit seam for "did a Claude Code log land in the `claude-code` index with the right mapping." That is observed live. When asked to confirm monitoring behavior, run a working-session loop with the user — mirror `.claude/rules/firewall-uat.md`:

### 1. Locate yourself
- `$CLAWKER_AGENT` set → inside an agent container. You **cannot** run `clawker monitor *` (host-only; `feedback_no_host_clawker_in_container`). Confirm — a plain dev shell with the repo also exists.
- `$CLAWKER_AGENT` unset → host shell; you may drive `clawker monitor *` yourself. Host bash sandbox strips network/docker — use `dangerouslyDisableSandbox: true`.

### 2. Bring the stack up (ask the user if you can't)
- Lifecycle is CLI-owned: `clawker monitor up` / `status` / `down --volumes` (`feedback_cli_owns_compose_lifecycle`). In a container you cannot run it — ask the user to `clawker monitor up` and confirm `clawker monitor status` is green before you probe.
- Stack is throwaway (`feedback_monitoring_stack_throwaway`): index-template / saved-object / compose edits need `clawker monitor down --volumes && clawker monitor up` to take effect. Ingest-pipeline *body* edits are the exception — resolved by name per-doc, so a plain `monitor up` re-runs them.

### 3. Check the docker socket
- Reaching OpenSearch / Dashboards from inside the agent needs the docker socket (`/var/run/docker.sock`, gated by `security.docker_socket`, **default OFF**). Probe: `docker info` (sandbox-disabled).
- **No socket** → you cannot reach the stack from in-container. Drive UAT entirely through the user: they run the queries host-side and paste results.
- **Socket present** → use the curl-container pattern below.

### 4. Query via a container (you can't dial the indices directly)
CoreDNS only resolves `otel-collector` + `prometheus` for agents (`MonitoringServiceHostnames`); `opensearch-node` / `opensearch-dashboards` are intentionally unresolvable (`feedback_clawker_container_no_direct_net`), and the collector/Prometheus paths are push/scrape, not query. So hit the service containers on their own loopback:

```
# OpenSearch — what actually indexed (opensearch-node ships curl)
docker exec opensearch-node curl -s 'http://localhost:9200/_cat/indices?v'
docker exec opensearch-node curl -s 'http://localhost:9200/claude-code/_search?size=1&sort=@timestamp:desc' | python3 -m json.tool
docker exec opensearch-node curl -s 'http://localhost:9200/clawker-envoy/_mapping' | python3 -m json.tool

# Prometheus — confirm a series exists (use a curl sidecar; prom image has no curl)
docker run --rm --network container:prometheus --entrypoint curl curlimages/curl \
  -s 'http://localhost:9090/api/v1/query?query=claude_code_token_usage'

# Collector / bootstrap triage
docker logs clawker-otel-collector --tail 50
docker logs clawker-opensearch-bootstrap   # one-shot; non-zero exit = stack half-up by design
```

Pattern: `docker exec <svc> curl …` where the image bundles curl (`opensearch-node`); else `docker run --rm --network container:<svc> --entrypoint curl curlimages/curl …` (Envoy, Prometheus).

### 5. Back-and-forth
You probe → report what indexed / scraped / rendered → user mutates host-side (`monitor down --volumes && up`, config edit) → you re-probe. Never declare a pipeline / index / dashboard change "working" from golden tests alone.

## What NOT To Do

- Don't add hostname knobs to `MonitoringConfig` for monitoring services — they're consts shared with the firewall plane.
