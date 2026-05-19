# Monitoring Reference — Detailed Schemas & Patterns

> For essential rules, see `.claude/rules/monitoring.md`.

> **Backend:** logs in OpenSearch — five indices `claude-code` (Claude Code OTLP push, untrusted port), `clawker-cli` (host CLI OTLP push, untrusted port), `clawker-cp` (mTLS-gated CP push), `clawker-envoy` (firewall data-plane access logs, mTLS-gated), and `clawker-coredns` (firewall DNS query logs, mTLS-gated). Cross-index queries use pattern `clawker-cp,claude-code,clawker-cli,clawker-envoy,clawker-coredns`. Traces in OpenSearch SS4O dataset `traces` / namespace `clawker` (Claude Code beta export gated on `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1`, both env vars baked into the image). Metrics in Prometheus. UIs: OpenSearch Dashboards (`:5601`) for logs+traces, Prometheus (`:9090`) for metrics. Resource attributes land FLAT at `resource.*` (so `resource.service.name`, `resource.project`, `resource.agent`) — NOT nested at `resource.attributes.*`, despite the opensearch-go exporter's name. Event content lives under `attributes.*` (`attributes.event.name`, `attributes.tool_name`).
>
> **Stack ships preconfigured** — `clawker-opensearch-bootstrap` (one-shot compose service, see `internal/monitor/templates/opensearch-bootstrap/`) applies component + index templates, the default ISM retention policy, the `clawker_prometheus` direct-query datasource, an OSD `Clawker` workspace with `features: ["use-case-all"]`, and Dashboards saved objects (index patterns for every log index + the preconfigured `Claude Code` dashboard with KPI strip and filter controls) on every `monitor up`. The visualization surface grows via the NDJSON-export-and-bake workflow into `internal/monitor/templates/opensearch-bootstrap/saved-objects/clawker.ndjson`.

## Complete Event Schemas

> The field tables below use the flat names emitted by Claude Code OTLP records (`service_name`, `event_name`, `tool_name`, etc.). When querying OpenSearch, map `event_name` → `attributes.event.name`, `service_name` → `resource.service.name`, `project`/`agent` → `resource.project`/`resource.agent`, etc. Resource attrs are FLAT under `resource.*` (NOT nested at `resource.attributes.*`); event-time fields stay under `attributes.*`.

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

> **MCP tool name resolution**: `tool_name` is `"mcp_tool"` for all MCP calls — the actual tool name is inside the `tool_parameters` JSON string as `mcp_tool_name`. `tool_parameters` is a flat string attribute (not pre-parsed by the indexer); extract `mcp_tool_name` in the query before aggregation.

> **Field naming discrepancy**: Live event data uses `decision_type`/`decision_source`, official docs use `decision`/`source`. Dashboard queries use the live field names.

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
