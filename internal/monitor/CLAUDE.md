# Monitor Package

Templates + generation for the monitoring stack (Docker Compose + OTEL Collector pipeline + Prometheus). Backed by OpenSearch (logs + traces) and Prometheus (metrics). OpenSearch Dashboards is the UI for logs/traces; Prometheus has its own UI for metrics.

The package carries ONLY generic infrastructure config (envoy/coredns/cli/clawkercp/ebpf-egress lanes, compose, prometheus, bootstrap machinery) — agent-side observability is delivered by **monitoring units** resolved at render time (`units.go`), and a grep-guard test (`TestNoClaudeCodeInMonitorPackage`) pins the zero-claude-code invariant. The otel-config template ranges over the ACTIVE unit set to emit per-lane exporters, routing entries, pipelines, and service-scoped OTTL datapoint renames.

## Monitoring units (`units.go`)

```go
const UnitSourceBuiltIn = "(built-in)"
const UnitsMarkerFile   = ".clawker-units"
type ResolvedUnit struct { Name string; Unit *bundler.MonitoringUnit; Source, Path string; Active bool; LoadErr error }
func ResolveUnits(cfg config.Config) ([]ResolvedUnit, error)      // built-ins (shipped bundles) ∪ settings monitoring.units; flat namespace; all default inactive
func ActiveUnits(cfg config.Config) ([]ResolvedUnit, error)       // active subset; broken active entry = error
func ActiveFromResolved(units []ResolvedUnit) ([]ResolvedUnit, error)
func ValidateActiveSet(active []ResolvedUnit) error               // index + service.name exclusivity across the active set
func DiscoverableUnits(cfg config.Config) []DiscoverableUnit      // project-registered bundles' declared units not yet registered (list-only)
type UnitRouting struct { Name string; Lanes []UnitLogLane; MetricRenameStatements []string }
func BuildUnitRoutings(active []ResolvedUnit) ([]UnitRouting, error) // logs/unit_<id> + opensearch/logs_unit_<id>; sanitized-ID collision = error
func ReadUnitsMarker(destDir string) ([]string, error)            // active set the bootstrap dir was rendered with (up's drift warning)
```

Everything is opt-in: built-in AND registered units default inactive; `clawker monitor enable` is the only activation path (commands live in `internal/cmd/monitor/units/`). Inactive units' records fall to the debug-only `logs/untrusted_unrouted` pipeline — never indexed, so a later activation can't fight a pre-existing dynamically-mapped index.

> Confirming live ingest/routing/rendering is **not** unit-testable — see `.claude/rules/monitoring.md` → "Runtime UAT" for the curl-container working-session loop (ask the user to `clawker monitor up`; you can't from inside a container).

Service hostnames live in `internal/consts/monitoring.go`. `MonitoringServiceHostnames` (`otel-collector`, `prometheus`) is the subset wired into CoreDNS's `internalHosts` forward zones — only services agent containers legitimately need to dial. `opensearch-node` and `opensearch-dashboards` are intentionally omitted: agents never query indices directly, so those containers reach each other via Docker's embedded resolver without going through CoreDNS.

## Template Files

Template source files live in `templates/`. Every file is a Go `text/template` (`.tmpl`) rendered via `RenderTemplate` before being written to disk. The OpenSearch bootstrap asset tree is also embedded — see [OpenSearch Bootstrap](#opensearch-bootstrap) below.

| Embedded Variable | Source File |
|-------------------|-------------|
| `ComposeTemplate` | `templates/compose.yaml.tmpl` |
| `OtelConfigTemplate` | `templates/otel-config.yaml.tmpl` |
| `PrometheusTemplate` | `templates/prometheus.yaml.tmpl` |
| `OpenSearchBootstrapFS` (`embed.FS`) | `templates/opensearch-bootstrap/**` |

## Filename Constants

Output filenames for writing rendered content to disk:

| Constant | File |
|----------|------|
| `ComposeFileName` | `compose.yaml` |
| `OtelConfigFileName` | `otel-config.yaml` |
| `PrometheusFileName` | `prometheus.yaml` |
| `OpenSearchBootstrapDirName` | `opensearch-bootstrap` (workdir subdir) |

## Image Constants

All pinned to a multi-arch manifest list digest (`@sha256:`). Verify with `docker buildx imagetools inspect <pin>` before bumping.

| Constant | Image |
|----------|-------|
| `OtelCollectorImage` | `otel/opentelemetry-collector-contrib:0.152.0` |
| `PrometheusImage` | `prom/prometheus:v3.11.3` |
| `OpenSearchImage` | `opensearchproject/opensearch:3.6.0` |
| `OpenSearchDashboardsImage` | `opensearchproject/opensearch-dashboards:3.6.0` |
| `CurlImage` | `curlimages/curl:8.20.0` (one-shot bootstrap container) |

## Template Rendering

### MonitorTemplateData

Struct providing values for `{{.FieldName}}` placeholders. Service hostname fields (`OtelCollectorService`, `PrometheusService`, `OpenSearchNodeService`, `OpenSearchDashboardsService`) are populated from `consts.MonitoringService*` so renaming a service in consts propagates everywhere.

Each port field drives **both** sides of the host:container publish mapping AND the container's own listener config (Prometheus `--web.listen-address`, OpenSearch `http.port` env, Dashboards `SERVER_PORT` env, otel-collector receiver endpoints in `otel-config.yaml.tmpl`). Changing one setting moves host + internal together so users can shuffle ports to avoid host conflicts without diverging from the image's listener.

| Field | Type | Description |
|-------|------|-------------|
| `OtelCollectorPort` | `int` | OTEL collector OTLP/HTTP port (default 4318) |
| `OtelGRPCPort` | `int` | OTEL collector gRPC port (default 4317; independent of HTTP) |
| `OtelInfraPort` | `int` | mTLS-gated host-loopback receiver port for trusted infra push (clawkercp + firewall Envoy + CoreDNS — all share this lane) |
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
| `OtelCollectorImage` / `PrometheusImage` / `OpenSearchImage` / `OpenSearchDashboardsImage` / `CurlImage` | `string` | Pinned image refs |
| `OpenSearchBootstrapDirName` | `string` | Workdir subdir holding bootstrap.sh + JSON/NDJSON assets; bind-mounted into the bootstrap container |

### NewMonitorTemplateData(s *config.Settings, units []ResolvedUnit) (MonitorTemplateData, error)

Constructor that populates `MonitorTemplateData` from full `config.Settings` plus the ACTIVE unit set. Service hostnames come from the consts package; ports + heap come from `s.Monitoring`; `Units []UnitRouting` and `ISMIndexPatternsJSON` (infra indices + active default-retention lane indices, JSON-encoded Go-side) come from the units.

### RenderTemplate(name, tmplContent string, data MonitorTemplateData) (string, error)

Renders a Go `text/template` string with the given `MonitorTemplateData`. Returns the rendered string or an error if parsing/execution fails.

### WriteOpenSearchBootstrap(destDir string, data MonitorTemplateData, units []ResolvedUnit) error

Wipes `destDir` (it holds only generated content), mirrors `OpenSearchBootstrapFS` into it, then overlays every ACTIVE unit's artifacts into the same category dirs. `.tmpl` core files are rendered with `MonitorTemplateData` and written with the suffix stripped (`0755` so `bootstrap.sh` is executable); JSON/NDJSON files — core and unit — are copied verbatim (`0644`). A unit artifact path colliding with a core file or another unit is a hard error naming both provenances (PUT names are basename-derived), and saved-object IDs are collision-checked across every ndjson line + explore filename (`_import?overwrite=true` would otherwise be silent last-write-wins). Writes a `.clawker-units` marker (sorted active names) that `monitor up` compares for its drift warning. The wipe-first mirror means files removed from the embedded tree — or belonging to a deactivated unit — disappear from the rendered dir too. `monitor init` always re-renders this dir (no `--force` needed).

## Usage

The `monitor init` command constructs `MonitorTemplateData` via `NewMonitorTemplateData`, calls `RenderTemplate` for each `.tmpl` template, writes them to disk, then calls `WriteOpenSearchBootstrap` to materialize the bootstrap asset tree under `<monitorDir>/opensearch-bootstrap/`.

Stack template symbols are in `templates.go`; unit resolution + routing in `units.go`.

## OpenSearch Bootstrap

The OpenSearch + Dashboards cluster ships preconfigured: index templates (per-source mappings + retention), ISM policies (auto-attached via `ism_template.index_patterns`), data sources (Prometheus registered via the SQL plugin's `_plugins/_query/_datasources` API), and Dashboards saved objects (index patterns + dashboards) all apply on every fresh `monitor up`. The mechanism is a one-shot service in the compose stack — `clawker-opensearch-bootstrap` — that runs `bootstrap.sh` against the cluster between OpenSearch reaching `service_healthy` and the otel-collector starting. Prometheus starts in parallel; bootstrap depends on it (`service_started`) so the `clawker_prometheus` datasource registration can validate the configured URI.

### Source tree

Embedded into the CLI binary via `//go:embed all:templates/opensearch-bootstrap`:

```
templates/opensearch-bootstrap/
  bootstrap.sh.tmpl                    # POSIX sh, file-driven loops over the subdirs below
  component-templates/
    clawker-common.json                # Shared @timestamp / service.name / ingest_source mappings
  ingest-pipelines/
    cp-actor-attr-nest.json            # Painless: nest flat attributes.actor_attr.<k> into one flat_object
    netlogger-normalize.json           # Convert processor: stringify attributes.bpf_ts_ns so the UI doesn't localize the BPF monotonic timestamp as a comma-separated number
    envelope-normalize.json            # Painless: mirror severity.{text,number} → severityText/severityNumber + resource.service.name → resource.attributes.service.name so OSD explore's default log columns render; also strips the SS4O exporter's noisy `attributes.data_stream` envelope
    envoy-normalize.json               # Painless: strip empty CommonProperties noise (cluster_name/log_name/node_name/zone_name), remove empty instrumentationScope, drop literal '-' sentinels from response_flags/upstream_transport_failure_reason, coerce tls.established string→boolean
  index-templates/
    clawker-cli.json                   # Scalar attributes.event (zerolog Str("event", ...)); final_pipeline=envelope-normalize
    clawkercp.json                     # Same shape as clawker-cli with cp-specific fields; default_pipeline=cp-actor-attr-nest, final_pipeline=envelope-normalize
    clawker-envoy.json                 # Flat HTTP/TLS/TCP fields from Envoy ALS; default_pipeline=envoy-normalize, final_pipeline=envelope-normalize
    clawker-coredns.json               # Structured dns.query attributes from CoreDNS otel plugin; final_pipeline=envelope-normalize
    clawker-ebpf-egress.json             # eBPF egress event attributes (action + attribution + 4-tuple + domain) from netlogger (service.name=ebpf-egress); default_pipeline=netlogger-normalize, final_pipeline=envelope-normalize
  ism-policies/
    clawker-retention.json.tmpl        # 7d retention; ism_template.index_patterns generated: infra indices + active default-retention unit lanes
  datasources/
    clawker_prometheus.json.tmpl       # Prometheus connector for SQL/PPL + Dashboards Metrics Analytics
  saved-objects/
    clawker.ndjson                     # Generic saved objects (infra index patterns + Clawker Networking dashboard); one workspace-scoped _import per *.ndjson file
```

Claude Code artifacts (claude-code index template, claude-code-prompt-nest pipeline, both CC dashboards, all Explore PROMQL panels) live in the claude harness bundle's monitoring unit (`internal/bundler/assets/harnesses/claude/monitoring/claude-code/`) and are overlaid into this tree only when the unit is active. Index-template basenames MUST equal their index name (`index_patterns` = exactly the basename) — bootstrap.sh derives its index pre-create list from the basenames.

`monitor init` walks this tree via `WriteOpenSearchBootstrap`, renders `bootstrap.sh.tmpl` against `MonitorTemplateData` (only OpenSearch / Dashboards hostnames + ports), and copies the rest verbatim into `<monitorDir>/opensearch-bootstrap/`. That directory is bind-mounted RO into the bootstrap container at `/opensearch-bootstrap`.

### Compose ordering

```
opensearch-node (service_healthy via /_cluster/health)     prometheus (service_started)
        │                                                          │
        └──────────────────────────┬───────────────────────────────┘
                                   ▼
                clawker-opensearch-bootstrap (one-shot)
   - PUT /_component_template/<name> for each component-templates/*.json
   - PUT /_ingest/pipeline/<name>    for each ingest-pipelines/*.json
   - PUT /_index_template/<name>     for each index-templates/*.json
   - PUT /_plugins/_ism/policies/<id> for each ism-policies/*.json
   - poll http://prometheus:9090/-/ready until 2xx
   - POST /_plugins/_query/_datasources (body=file) for each datasources/*.json;
     PUT same endpoint on 400/409 to update an existing datasource
   - poll http://opensearch-dashboards:5601/api/status until 2xx
   - POST /api/saved_objects/data-connection/clawker-prometheus-conn?overwrite=true (global; referenced by workspace settings.dataConnections)
   - POST /api/workspaces (skip-if-exists by name "Clawker"); capture workspace id from /api/workspaces/_list
   - POST /w/<wsId>/api/saved_objects/_import?overwrite=true (multipart NDJSON, osd-xsrf: true) — workspace-scoped so imported SOs land with workspaces:[wsId] and are visible in the Clawker workspace UI
   - exit 0
                                   │  (service_completed_successfully)
                                   ▼
                              otel-collector
```

Prometheus is intentionally NOT gated on bootstrap — the SQL plugin validates the configured `prometheus.uri` at register time (DNS + TCP), so Prometheus must already be reachable when the bootstrap registers its datasource. The bootstrap polls Prometheus's `/-/ready` before the POST. Prometheus's only scrape targets (otel-collector self-scrape, the collector's Prometheus exporter) come up after bootstrap, but Prometheus tolerates down targets as normal operational state.

The data sources API requires `plugins.query.datasources.encryption.masterkey` to be set on the OpenSearch node, even with the security plugin disabled. The compose template sets a fixed dev key in the `opensearch-node` env block — the stack is local + ephemeral, no real credentials are encrypted with it.

If `bootstrap.sh` exits non-zero (e.g. malformed template JSON, OpenSearch rejects a mapping), the collector never starts. Logs surface in `docker logs clawker-opensearch-bootstrap`. There is no Dashboards healthcheck in compose — the script polls `/api/status` itself before doing saved-objects work, keeping all readiness logic in one place and avoiding having to add curl/wget to the Dashboards image's healthcheck.

### Templates only apply at index creation

OpenSearch index templates only take effect when an index is created — they do NOT retroactively re-map existing indices. The monitoring stack is preconfigured + ephemeral by design (see `.claude/rules/monitoring.md` → "Monitoring stack throwaway"), so the canonical way to pick up template / ISM / saved-object edits is `clawker monitor down --volumes && clawker monitor up`. Bootstrap re-runs on every `monitor up`; PUT semantics make template / ISM updates idempotent and `?overwrite=true` makes saved-objects import idempotent, but pre-existing index mappings stay locked to whatever was applied at first ingest of that index.

Ingest pipeline bodies (`ingest-pipelines/*.json`) are the exception — they're resolved by name on every document, so editing a Painless script and re-running `monitor up` picks up the change without a volume wipe. Only changing which pipeline name an index uses (the `settings.index.default_pipeline` or `settings.index.final_pipeline` reference in the index template) requires the volume cycle, because the binding is set at index creation.

### Default vs final pipeline split

Per OS docs, `default_pipeline` runs before document indexing, and `final_pipeline` runs after `default_pipeline` (and after any explicit `?pipeline=` override). Two roles:

- `default_pipeline` is per-index: `cp-actor-attr-nest` for `clawkercp`, `claude-code-prompt-nest` for `claude-code`, `netlogger-normalize` for `clawker-ebpf-egress`, `envoy-normalize` for `clawker-envoy`, unset for cli/coredns. These collapse source-specific dotted-key collisions or coerce wire-shape mismatches before the rest of the pipeline runs.
- `final_pipeline` is the shared `envelope-normalize` on all 6 indices. It writes the legacy SS4O envelope paths (`severityText`, `severityNumber`, `resource.attributes.<k>`) that the OSD explore plugin's default log columns read. The OTLP `opensearchexporter` in `ss4o` mode writes the canonical SS4O paths (`severity.{text,number}` nested, flat `resource.<k>`); OSD reads the legacy paths. Multiple upstream issues document the divergence with no merged fix (data-prepper#5791, opensearch-catalog#118, contrib#45428).

## OTEL Pipelines (otel-config.yaml.tmpl)

**Metrics default path: OTLP push → otel-collector → Prometheus scrape**. Clients wire `OTEL_EXPORTER_OTLP_ENDPOINT=cfg.OtelCollectorURL()` (base URL only); the OTel SDK appends `/v1/metrics` and pushes to the collector's OTLP/HTTP receiver. The collector's `transform/metrics` processor copies resource attrs (project, agent) to datapoint attributes, then the `prometheus` exporter exposes a scrape endpoint on `PrometheusMetricsPort` that Prometheus scrapes. Prometheus' native OTLP receiver is also enabled (`--web.enable-otlp-receiver`, `--enable-feature=otlp-deltatocumulative`, `otlp.promote_resource_attributes` in prometheus.yaml) and `cfg.PrometheusURL() + Telemetry.PrometheusOTLPPath` returns its URL — direct OTLP push works and saves a hop, but Prometheus' `/api/v1/metadata` excludes anything ingested via OTLP/remote-write (upstream limitation), so consumers depending on metric metadata (OpenSearch Dashboards' Observability Metrics catalog, etc.) silently miss those metrics. Route via the collector unless metadata-blindness is acceptable.

Every pipeline runs `memory_limiter` as its first processor — the collector container has a 200M hard memory cap (compose deploy.resources.limits), and `memory_limiter` applies backpressure before the kernel OOM-kills the process. Pipelines on the untrusted lane additionally stamp `resource/untrusted_otlp` (sets `ingest_source=untrusted_otlp`) so dashboards can separate forgeable sender-declared records from records anchored by mTLS handshake. `batch` is explicitly sized (`timeout: 5s`, `send_batch_size: 1024`, `send_batch_max_size: 2048`) so behavior is predictable under burst.

| Pipeline | Receivers | Exporters |
|----------|-----------|-----------|
| `traces` | `otlp` (untrusted; `resource/untrusted_otlp` stamps `ingest_source=untrusted_otlp` so forgeable spans are distinguishable) | `opensearch/traces` (SS4O — dataset=traces, namespace=clawker), `spanmetrics` (→ metrics pipelines), `debug` |
| `metrics/untrusted` | `otlp` (default path for Claude Code + agent metrics push; direct Prometheus OTLP push is the documented alternate). `resource/untrusted_otlp` stamps `ingest_source` before `transform/metrics` copies project/agent resource attrs to datapoint labels so the forgeable labels are always paired with the provenance marker | `prometheus` (scrape endpoint on `PrometheusMetricsPort` — Prometheus scrapes this), `debug` |
| `metrics/trusted` | `prometheus/self` (collector self-scrape on :8888), `spanmetrics` (RED from traces) — all locally sourced, no sender-declared attrs to defend | `prometheus` (shared with `metrics/untrusted`), `debug` |
| `logs/in_untrusted` | `otlp` (no client auth) | `routing/untrusted` connector — only `service.name=clawker-cli` plus ACTIVE monitoring units' declared service names route downstream; everything else falls to `default_pipelines: [logs/untrusted_unrouted]` (debug-only, never indexed) |
| `logs/clawker-cli` | `routing/untrusted` (when `service.name=clawker-cli` — stamped by `internal/logger.newOtelProvider` Resource when called with `OtelOptions.ServiceName="clawker-cli"`; wired in `internal/cmd/factory/default.go`) | `opensearch/logs_clawker_cli` (index `clawker-cli`), `debug` |
| `logs/unit_<id>` (generated per active unit lane) | `routing/untrusted` (when `service.name` matches the lane's declared values) | `opensearch/logs_unit_<id>` (the lane's index), `debug` |
| `logs/in_trusted` *(trusted block, see note)* | `otlp/infra` (mTLS-gated; infra intermediate CA is `client_ca_file` — agent leaves chained to the CLI root cannot complete the handshake) | `routing/trusted` connector — `error_mode: propagate` (OTTL errors surface in collector stdout), `default_pipelines: [logs/trusted_unrouted]` catches unmapped `service.name` values |
| `logs/cp` *(trusted block, see note)* | `routing/trusted` (when `service.name=clawkercp`); `resource/cp` stamps `ingest_source=cp` post-routing | `opensearch/logs_cp` (index `clawkercp`), `debug` |
| `logs/envoy` *(trusted block, see note)* | `routing/trusted` (when `service.name=envoy` — stamped by Envoy ALS via the `otel_collector_als` upstream cluster's mTLS path); `resource/envoy` stamps `ingest_source=envoy` post-routing | `opensearch/logs_envoy` (index `clawker-envoy`), `debug` |
| `logs/coredns` *(trusted block, see note)* | `routing/trusted` (when `service.name=coredns` — stamped by the in-tree CoreDNS `otel` plugin's OTLP/gRPC + mTLS push); `resource/coredns` stamps `ingest_source=coredns` post-routing | `opensearch/logs_coredns` (index `clawker-coredns`), `debug` |
| `logs/netlogger` *(trusted block, see note)* | `routing/trusted` (when `service.name=ebpf-egress` — stamped by netlogger's `*sdklog.LoggerProvider` via `controlplane.NewOtelLoggerProvider` over OTLP/gRPC + mTLS leaf from `otelcerts.LoadTLSConfig("netlogger")`); `resource/netlogger` stamps `ingest_source=netlogger` post-routing | `opensearch/logs_netlogger` (index `clawker-ebpf-egress`), `debug` |
| `logs/trusted_unrouted` *(trusted block, see note)* | `routing/trusted` default branch (any `service.name` outside cp/envoy/coredns/ebpf-egress). Should never fire — only those four hold infra-intermediate-chained leaves today | `debug` only (operator can grep `docker logs clawker-otel-collector` to identify a misconfigured trusted sender) |

All `opensearch/*` exporters have `sending_queue.enabled: true` and `retry_on_failure` (5s initial, 5m max elapsed) so forensic data survives the OpenSearch boot window (start gated on `service_healthy` via cluster-health endpoint) and short outages. The `prometheus` exporter is pull-based — no retry config needed.

**`transform/metrics` quirks — `type` → `kind` rename**: the `transform/metrics` processor (applied on both `metrics/untrusted` and `metrics/trusted`) does more than copy resource attrs to datapoints. It also **unconditionally renames any datapoint attribute named `type` to `kind`** before metrics reach the Prometheus exporter. Reason: the OpenSearch SQL plugin's experimental direct-query Prometheus connector (`OpenSearchImage`/`OpenSearchDashboardsImage` pinned at 3.6.0) has a substring-check bug in `ExecuteDirectQueryActionResponse.parseResult` (`direct-query/src/main/java/org/opensearch/sql/directquery/transport/model/ExecuteDirectQueryActionResponse.java`) that decides whether to inject the Jackson polymorphic discriminator at the JSON root via `rawResult.contains("\"type\":")`. Any Prom series in the response carrying a label literally named `type` flips the check false-positive, the root wrap is skipped, and Jackson dies with `MismatchedInputException: missing type id property 'type'`. This breaks the OSD Explore "Metrics" UI for every series with such a label — Claude Code carries one on `claude_code.token.usage` (`input`/`output`/`cacheRead`/`cacheCreation`), `claude_code.active_time.total` (`cli`/`user`), and `claude_code.lines_of_code.count` (`added`/`removed`). The PPL form (`source = clawker_prometheus.<metric>`) and native Prom UI take a different code path and are unaffected. Removal criteria when the OS image is bumped: re-read that file's `parseResult` method; if the substring check has been replaced with a JSON-root check (or the wrap is unconditional), delete the two `set(attributes["kind"], ...)` + `delete_key(attributes, "type")` statements in `otel-config.yaml.tmpl`'s `transform/metrics` block. No upstream issue tracks the bug at the time of writing; the closest neighbor is `opensearch-project/sql#5251` (a different scalar-shape deserialization bug in the same `PrometheusResult` class).

> **Trusted block conditionality**: the `otlp/infra` receiver + all five trusted pipelines (`logs/in_trusted`, `logs/cp`, `logs/envoy`, `logs/coredns`, `logs/netlogger`) are **always rendered** into `otel-config.yaml` — the template has no `{{ if }}` gate inside the collector config. Note that `compose.yaml.tmpl` separately gates the host-side mounts (cert paths) and the port publish on `OtelInfraPort` being non-zero; production wiring always passes a non-zero port, but if a future code path ever set `OtelInfraPort=0`, the receiver would still render but be unreachable because the host bind-mounted server cert + port mapping would be absent. `monitor init` always mints + mounts the collector server cert at `/etc/otel/tls/`. Degradation is sender-side only: when the infra issuer isn't available or per-sender cert minting fails, the receiver keeps listening but no trusted client can complete the mTLS handshake. Envoy drops the OTel access-log sink + `otel_collector_als` cluster (gated at the sender via `als.MTLS` in `buildHTTPAccessLog` / `buildTCPAccessLog` / `buildClusters`), keeping only the stdout JSON sink for `docker logs clawker-envoy` triage; the CoreDNS otel plugin installs `noopEmitter` (no `CLAWKER_COREDNS_OTEL_ENDPOINT` env var); the CP's mTLS log shipper stays cold. Infra services never push OTLP across the untrusted `otel-collector:4317` lane reserved for agent containers — the infra ingestion path is closed end-to-end at the sender.

**Index split rationale**: clawker's zerolog pattern (`Str("event", "name")`) emits `attributes.event` as a scalar string. Claude Code follows OTEL semantic conventions and emits `attributes.event.name` (nested). Envoy ALS attributes carry HTTP request fields (method, path, response_code) flat on the record. CoreDNS query records (emitted by the in-tree `otel` plugin, see below) carry DNS-specific attributes (`query_name`, `qtype`, `rcode`, `answer_count`, `duration_ms`, `answers`) flat on the record. netlogger eBPF egress records carry decision-point fields (`action`, `dst_ip`, `dst_port`, `l4_proto`, `domain_hash`, `cgroup_id`, etc.) flat on the record. OpenSearch dynamic mapping locks the first-seen shape per field, so sharing one index would reject whichever source loses the race. Splitting by source keeps each schema clean. The `ingest_source` resource attribute is stamped on every record for cross-index queries via the pattern `clawkercp,claude-code,clawker-envoy,clawker-coredns,clawker-ebpf-egress`.

**Envoy access logs**: Envoy ships records via the native `envoy.access_loggers.open_telemetry` sink to the collector's mTLS-gated `otlp/infra` receiver. Resource attribute `service.name=envoy` is set on the Envoy side (see `firewall/envoy_config.go::otelAccessLogEntry`), and the cluster `otel_collector_als` (parameterized by `ALSConfig` — `MTLS=true` dials `OtelInfraPort` with an upstream TLS transport_socket using the CLI-CA-chained leaf bind-mounted under `/etc/envoy/otel-tls/`; `MTLS=false` causes the OTel access-log sink AND the `otel_collector_als` cluster to be omitted entirely at the sender — gated in `buildHTTPAccessLog` / `buildTCPAccessLog` / `buildClusters` — so infra services never cross into the untrusted `otel-collector:4317` lane reserved for agent containers) handles the gRPC connection. The legacy `envoy.access_loggers.stdout` sink is kept alongside for `docker logs clawker-envoy` triage when the monitoring stack is down (and is the sole access-log sink in degraded mode).

**CoreDNS query logs**: ships via the in-tree `otel` CoreDNS plugin (`cmd/coredns-clawker/plugins/otel/`) which emits one structured `dns.query` OTLP log record per query (OTLP/gRPC + mTLS) to the collector's `otlp/infra` receiver. The plugin is the **first** directive in every server block (set in `cmd/coredns-clawker/main.go`) so it observes the final rcode + answer set after `forward`/`template`/etc. Endpoint host:port is wired by `firewall.Stack` via `CLAWKER_COREDNS_OTEL_ENDPOINT`; CLI-CA-chained leaf is bind-mounted at `/etc/clawker/auth/coredns/client.{pem,key}` + the CA at `/etc/clawker/auth/coredns/ca.pem`. Leaves are issued + rotated by `internal/controlplane/infracerts`; `tls.Config.GetClientCertificate` re-reads the leaf on every handshake so rotation requires no container restart. Each record carries `event.name=dns.query` plus attributes `client.address` (OTel-canonical, replaces colloquial `client_ip`), `zone`, `query_name`, `qtype`, `rcode`, `answer_count`, `duration_ms`, and (when non-empty) `answers`. There is no `action` attribute — CoreDNS makes no explicit allow/deny decision per query (it forwards or NXDOMAINs by zone), so `rcode` is the honest signal; a prior zone-derived `action` was provably wrong (a non-allowlisted subdomain of an exact-allow apex logged `action=allowed` while returning NXDOMAIN). No per-record `source=coredns` attribute — `service.name=coredns` (resource layer) + `ingest_source=coredns` (stamped post-routing) cover provenance. NXDOMAIN comes through with `rcode=NXDOMAIN`; resolver errors set `record.SetErr(...)` with `rcode=SERVFAIL`. The stdout `log` plugin is kept alongside for `docker logs clawker-coredns` triage when the monitoring stack is down — it is no longer scraped into OpenSearch.

**netlogger eBPF egress events**: ships via netlogger's own `*sdklog.LoggerProvider` (built by `controlplane.NewOtelLoggerProvider`, see `internal/controlplane/firewall/ebpf/netlogger/CLAUDE.md`) over OTLP/gRPC + mTLS to the collector's `otlp/infra` receiver. The mTLS leaf is minted per-handshake from `otelcerts.Service.LoadTLSConfig("netlogger")` — chains through the same infra intermediate CA as the CP zerolog bridge, no new on-disk material. The provider carries `service.name=ebpf-egress` so `routing/trusted` lands records in `clawker-ebpf-egress` instead of `clawkercp` — different retention + volume profile + consumer audience (per-agent security telemetry vs operator-facing daemon health). `event.name` is per-emit-site (`ebpf.egress.connect` / `ebpf.egress.sendmsg` / `ebpf.egress.sock_create`) so dashboards can filter by record kind. Each record carries that plus attributes `action` (`allowed`/`denied`/`bypassed`), `container_id`, `agent`, `project`, `cgroup_id`, `bpf_ts_ns`, `dst_ip`, `dst_port`, `l4_proto` + `l4_proto_code`, `ipv6`, `ipv4_mapped`, `no_dst`, `dst_host`, `domain_hash`. Strict directive with per-code-path carve-outs: every field is emitted on every record EXCEPT `dst_ip` (omitted when `!DstIP.IsValid()` — sock_create + native-IPv6-with-no-addr defensive), `dst_port` (omitted when `no_dst=true`), and `dst_host` (omitted when no DNS context — direct-IP connect). Operators partition via `_exists_:attributes.<key>`. `dst_ip` follows the Cilium / Tetragon address representation: a single attribute carrying either an IPv4 dotted-quad or an IPv6 colon-form string (BPF emits a flat 16-byte slot; OS `type: ip` mapping accepts both). No `source` or `component` per-record attributes — `service.name` + `ingest_source` resource attrs discriminate (the per-record dupes were dropped as schema rot). The CP boot path degrades to `event=netlogger_unavailable` when the collector preflight dial fails (20s deadline; no background reconnect), so firewall enforcement is unaffected when the monitoring stack is down.

`spanmetrics` is a connector — traces flow through it and re-emerge as RED (rate / errors / duration) metrics on the metrics pipeline. `prometheus/self` scrapes the collector's own telemetry endpoint so operational metrics for the collector itself land in Prometheus alongside agent telemetry. `debug` writes every batch to the collector's stdout — surfaces in `docker logs clawker-otel-collector`, verbose by design.

The `otlp` HTTP receiver has no `cors` block — browser-based pushers will be rejected by preflight. OTLP/HTTP from server-side clients works fine; if a future SPA needs to push directly, add a `cors.allowed_origins` entry scoped to that origin to the receiver in `otel-config.yaml.tmpl`.

OpenSearch's security plugin is disabled in the compose template (`DISABLE_SECURITY_PLUGIN=true`) so the collector talks plain HTTP to it on the docker network. OpenSearch Dashboards runs with its security plugin disabled too — no login required for local development.
