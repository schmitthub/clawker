# Monitoring Reference — Detailed Schemas & Patterns

> For essential rules, see `.claude/rules/monitoring.md`.

> **Backend (current):** logs in OpenSearch — five indices `claude-code` (Claude Code OTLP push, untrusted port), `clawker-cli` (host CLI OTLP push, untrusted port), `clawker-cp` (mTLS-gated CP push), `clawker-envoy` (firewall data-plane access logs, mTLS-gated), and `clawker-coredns` (firewall DNS query logs, mTLS-gated). Cross-index queries use pattern `clawker-cp,claude-code,clawker-cli,clawker-envoy,clawker-coredns`. Traces in OpenSearch SS4O dataset `traces` / namespace `clawker` (Claude Code beta export gated on `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1`, both env vars baked into the image). Metrics in Prometheus. UIs: OpenSearch Dashboards (`:5601`) for logs+traces, Prometheus (`:9090`) for metrics. Field semantics are unchanged, but paths are SS4O-shaped: resource attributes land FLAT at `resource.*` (so `resource.service.name`, `resource.project`, `resource.agent`) — NOT nested at `resource.attributes.*`, despite the opensearch-go exporter's name. Event content stays under `attributes.*` (`attributes.event.name`, `attributes.tool_name`). Translate the historical Loki LogQL / Grafana panel examples here to OpenSearch DSL / PPL / Lucene against these paths as needed.
>
> **Stack ships preconfigured** — `clawker-opensearch-bootstrap` (one-shot compose service, see `internal/monitor/templates/opensearch-bootstrap/`) applies component + index templates, the default ISM retention policy, the `clawker_prometheus` direct-query datasource, an OSD `Clawker` workspace with `features: ["use-case-all"]`, and Dashboards saved objects (index patterns for every log index + the preconfigured `Claude Code` dashboard with KPI strip and filter controls) on every `monitor up`. The Grafana panel/MCP sections below remain HISTORICAL — kept as a porting reference for the kinds of queries to add to OpenSearch Dashboards visualizations. Data plane is fully wired; the visualization surface is seeded with one dashboard and expected to grow via the NDJSON-export-and-bake workflow into `internal/monitor/templates/opensearch-bootstrap/saved-objects/clawker.ndjson`.

## Complete Event Schemas

> ⚠️ **Field-name caveat:** the field tables below use the flat Loki-style names (`service_name`, `event_name`, `tool_name`, etc.) that were emitted under the prior Loki/Grafana stack. The underlying OTLP record content is unchanged, but on OpenSearch translate `event_name` → `attributes.event.name`, `service_name` → `resource.service.name`, `project`/`agent` → `resource.project`/`resource.agent`, etc. before querying. Resource attrs are FLAT under `resource.*` (NOT nested at `resource.attributes.*`); event-time fields stay under `attributes.*`. The semantics, presence conditions, and tool-name resolution rules all still apply.

> Validated against live data as of 2026-02-13 (Claude Code v2.1.42). Subject to change — consider valid first before querying for updates.

### Common OTEL envelope fields (present on ALL events)

`service_name`, `service_version`, `scope_name`, `scope_version`, `detected_level`, `observed_timestamp`, `host_arch`, `os_type`, `os_version`, `terminal_type`, `organization_id`, `user_account_uuid`, `user_email`, `user_id`

### Common clawker-injected fields (from OTEL_RESOURCE_ATTRIBUTES)

`project`, `agent`, `session_id`

### Common event fields

`event_name`, `event_timestamp`, `event_sequence`

### `claude_code.tool_result`

Logged when a tool completes.

| Field | Type | Description |
|-------|------|-------------|
| `tool_name` | string | Tool name (e.g., "Bash", "Read", "Edit", "mcp_tool") |
| `success` | string | `"true"` or `"false"` |
| `duration_ms` | numeric string | Execution time in milliseconds |
| `decision_type` | string | `"accept"` or `"reject"` |
| `decision_source` | string | `"config"`, `"user_permanent"`, `"user_temporary"`, `"user_abort"`, `"user_reject"` |
| `tool_result_size_bytes` | numeric string | Size of tool result |
| `error` | string | Error message (only present when `success=false`) |
| `tool_parameters` | JSON string | Tool-specific params (requires `OTEL_LOG_TOOL_DETAILS=1`). Bash: `{bash_command, full_command, timeout, description, sandbox}`. MCP: `{mcp_server_name, mcp_tool_name}`. Skill: `{skill_name}` |
| `mcp_server_scope` | string | Present for mcp_tool calls |

> **MCP tool name resolution**: `tool_name` is `"mcp_tool"` for all MCP calls — the actual tool name is inside the `tool_parameters` JSON string as `mcp_tool_name`. Since `tool_parameters` is a flat string label (not parsed by Loki), use `label_format` with `regexReplaceAll` to extract it before aggregation:
> ```
> | label_format tool=`{{ if eq .tool_name "mcp_tool" }}mcp:{{ regexReplaceAll ".*mcp_tool_name\\":\\"([^\\"]*)\\".*" .tool_parameters "${1}" }}{{ else }}{{ .tool_name }}{{ end }}`
> ```
> Then `sum by (tool)` instead of `sum by (tool_name)`. Dashboard panels use this pattern with `mcp:` prefix for clarity.

> **Field naming discrepancy**: Live Loki data uses `decision_type`/`decision_source`, official docs use `decision`/`source`. Dashboard queries use the live field names.

### `claude_code.tool_decision`

Logged when a tool permission decision is made.

| Field | Type | Description |
|-------|------|-------------|
| `tool_name` | string | Tool name |
| `decision` | string | `"accept"` or `"reject"` |
| `source` | string | `"config"`, `"user_permanent"`, `"user_temporary"`, `"user_abort"`, `"user_reject"` |

### `claude_code.api_request`

Logged for each API call.

| Field | Type | Description |
|-------|------|-------------|
| `model` | string | Model ID (e.g., "claude-opus-4-6") |
| `cost_usd` | numeric string | Estimated cost in USD |
| `duration_ms` | numeric string | Request duration |
| `input_tokens` | numeric string | Input tokens |
| `output_tokens` | numeric string | Output tokens |
| `cache_read_tokens` | numeric string | Tokens read from cache |
| `cache_creation_tokens` | numeric string | Tokens for cache creation |
| `speed` | string | e.g., "normal" (not in official docs, present in live data) |

### `claude_code.api_error`

Logged when API request fails.

| Field | Type | Description |
|-------|------|-------------|
| `model` | string | Model ID |
| `error` | string | Error message |
| `status_code` | string | HTTP status code (may be "undefined") |
| `duration_ms` | numeric string | Request duration |
| `attempt` | numeric string | Retry attempt number |
| `speed` | string | e.g., "normal" |

### `claude_code.user_prompt`

Logged when user submits a prompt.

| Field | Type | Description |
|-------|------|-------------|
| `prompt_length` | numeric string | Length of prompt |
| `prompt` | string | Prompt content (requires `OTEL_LOG_USER_PROMPTS=1`, redacted otherwise) |

## Grafana Dashboard Patterns

> ⚠️ **HISTORICAL — does not ship.** Grafana / Loki are no longer part of the monitoring stack (replaced by OpenSearch Dashboards + Prometheus, see top-of-file banner). The patterns below are kept as a porting reference for the kinds of queries to rebuild against OpenSearch Dashboards. The actual stack today does NOT contain Grafana, Loki, or any of the LogQL/transformation features described in this section.

### Loki instant query gotchas (CRITICAL)

Loki instant metric queries return **separate data frames per result**, each with a field named `"Value"` and dimensions stored as **field-level labels** (NOT frame-level labels). This is fundamentally different from Prometheus which returns a single merged table.

**The "Value #A" problem**: Without transformations, Grafana sees N frames each with a "Value" field. When merging, it disambiguates by appending `#refId` suffixes: "Value #A", "Value #B", etc. This breaks color overrides, legends, and grouping.

**What works — single-dimension queries with `labelsToFields` → `merge`:**

For queries grouped by ONE dimension (e.g., `sum by (tool_name)`), this chain reliably extracts the dimension as a column:
```json
"transformations": [
  { "id": "labelsToFields", "options": { "mode": "columns" } },
  { "id": "merge", "options": {} },
  { "id": "organize", "options": { "excludeByName": { "Time": true }, "renameByName": { ... } } }
]
```
Result: `tool_name | Value` — works for pie charts (`reduceOptions.values: true`) and tables.

**What works — multi-target queries (A+B) with rename:**

For queries needing multiple value columns, use separate targets (one per metric) each grouped by the same dimension. Each target gets its own refId, producing predictable "Value #A", "Value #B" names that you rename in `organize`:
```json
"transformations": [
  { "id": "labelsToFields", "options": { "mode": "columns" } },
  { "id": "merge", "options": {} },
  { "id": "organize", "options": { "excludeByName": { "Time": true }, "renameByName": { "Value #A": "Calls", "Value #B": "Failures" } } }
]
```

**What does NOT work — cross-tabulation / pivoting with Loki data:**

Do NOT attempt to pivot Loki instant query results across two dimensions (e.g., tool_name × decision_type) using transformations. The following approaches have all been tested and fail:
- `labelsToFields` mode `valueLabel` — does not rename fields or merge frames from Loki
- `joinByLabels` — requires frame-level labels; Loki puts labels at field-level → "no labels in result"
- `groupingToMatrix` — requires exactly 1 input frame; silently no-ops when given multiple frames (`data.length !== 1` check)
- `labelsToFields` (columns) → `merge` → `groupingToMatrix` — merge produces "Value #A" collisions before groupingToMatrix runs

If you need cross-tabulated data, use the multi-target approach above (one query per pivot value) or display the data as a table instead.

**Other Loki instant query rules:**
- `format: "table"` is a **no-op for Loki metric queries**
- `legendFormat` templates ARE applied as display names but pie/bar charts **ignore them** for field naming
- Always use `queryType: "instant"` instead of the deprecated `instant: true` on Loki targets — the Loki datasource plugin checks `queryType` first
- Always set `"xField"` explicitly in bar chart options — auto-detection fails when transformations produce multiple string columns

### Panel type guidance

| Type | Query | Transformations Needed? |
|------|-------|------------------------|
| Timeseries | Range query | No — `legendFormat: "{{label}}"` works natively |
| Pie chart | Instant query | YES — `labelsToFields` → `merge` → `organize` |
| Bar chart | Instant query | Avoid for Loki cross-tabulation; use tables or multi-target with rename |
| Stat (single value) | Instant query | No — single-value `sum(...)` works natively |
| Table | Instant query (multi-target) | YES — `labelsToFields` → `merge` → `organize` (rename Value #A/B/C) |
| Logs | Log query | No — native Loki format, use `line_format` for display |

### Other rules

- **Panel IDs**: Must be globally unique across the entire dashboard. Always check existing IDs before adding panels.
- **Grid math**: Row header `h:1`, panel sub-rows `h:8`. Section total = `1 + n*8 + gaps`. Plan `gridPos` accordingly.
- **Template variables**: Use backtick syntax in Loki label filters (`project=~` `` `$project` `` `)`), double quotes in stream selectors (`{service_name="claude-code"}`).
- **Per-agent filtering**: All Loki queries include `| agent=~` `` `$agent` `` for consistency with dashboard filters.

## Grafana MCP Quirks

> ⚠️ **HISTORICAL — does not ship.** Grafana MCP is no longer used; the monitoring UI is OpenSearch Dashboards. Kept as porting reference for equivalent OpenSearch Dashboards tooling. Do NOT attempt to run these commands — they will fail or hit a dead endpoint.

When using Grafana MCP tools to develop or debug dashboards:

- **`list_loki_label_names`** only returns indexed stream labels (just `service_name`), NOT structured metadata. Don't rely on it for discovering event fields — use `query_loki_logs` with a log query instead.
- **`query_loki_logs` for metrics**: Use `queryType: "instant"` to test metric queries; returns `value` field with numeric result.
- **`query_loki_logs` for logs**: Returns `line` (log body text) and `labels` (structured metadata as flat object) — use this to inspect the actual data model.
- **`list_datasources`**: Use to get datasource UIDs. The Loki UID is the actual UID (e.g., `P8E80F9AEF21F6940`), NOT the Grafana variable name `${lokidatasource}`.
- **`get_dashboard_panel_queries`**: Use `uid: "claude-code-monitoring"` to inspect deployed panel queries.
- **`query_loki_stats`**: Only accepts simple label selectors — cannot test full LogQL queries. Use `query_loki_logs` instead.
- **Testing queries**: Replace Grafana variables (`$project`, `$agent`) with `.*` regex wildcard, and `$__range`/`$__auto` with explicit durations like `1h`.
- **Docker network requirement**: The Grafana MCP server resolves `grafana` via Docker internal DNS (`clawker-net`). If the MCP Docker container is not on `clawker-net`, all Grafana MCP calls will fail with `dial tcp: lookup grafana ... no such host`. Ensure the MCP server container is connected to the monitoring network. As a fallback, use `curl http://localhost:3000/api/...` from the host for Grafana API calls.

## Verification Workflow

When modifying templates, datasource, workspace, or saved objects:
1. Validate JSON syntax (parse the template output)
2. Run `make test` to ensure template tests pass
3. Rebuild the binary and re-bootstrap the monitoring stack. **Always use `--volumes`**: index templates, ISM, datasources, workspace, and saved objects all apply at bootstrap time and are bound to the OpenSearch volume — a plain `monitor down` keeps the old state.
   ```
   make clawker && clawker monitor init --force && clawker monitor down --volumes && clawker monitor up
   ```
4. Verify saved objects landed in the `Clawker` workspace at `http://localhost:5601`. For dashboards, open Discover/Explore against the relevant index pattern to confirm fields resolve before adjusting visualizations.

## Reference

- Official Claude Code monitoring docs: https://code.claude.com/docs/en/monitoring-usage.md
- **CRITICAL**: Only query this URL when you need to verify schema changes or discover new event types/fields. Do not fetch it routinely.
