---
description: Monitoring stack, Grafana dashboard, and Loki query guidelines
paths: ["internal/monitor/**"]
---

# Monitoring Rules

## Loki Data Model (Critical)

Claude Code's OTEL pipeline sends event data as **structured metadata labels**, NOT as JSON in the log body. The log body is a plain string event name.

- **NEVER use `| json`** — log bodies are plain strings like `claude_code.tool_result`, not JSON. Using `| json` causes parse errors across all panels.
- **Only stream label**: `service_name` is the sole indexed Loki stream label. All other fields (project, agent, tool_name, model, etc.) are structured metadata.
- **Event name filtering**: Filter by log line content with `|= "event_name"` (e.g., `|= "claude_code.tool_result"`).
- **Structured metadata access**: Labels are directly accessible for:
  - Filtering: `| tool_name="Read"`
  - Aggregation: `sum by (tool_name) (...)`
  - Unwrap: `| unwrap duration_ms`
  - Line format: `{{ .tool_name }}`

## Available Loki Event Schemas

### `claude_code.tool_result`
Fields: `project`, `agent`, `tool_name`, `duration_ms`, `is_error`, `num_turns`, `session_id`

### `claude_code.api_request`
Fields: `project`, `agent`, `model`, `duration_ms`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`, `cost_usd`, `session_id`

### `claude_code.api_error`
Fields: `project`, `agent`, `model`, `error_type`, `status_code`, `session_id`

## Grafana Dashboard JSON

- **Panel IDs**: Must be globally unique across the entire dashboard. Always check existing IDs before adding panels.
- **Grid math**: Row header `h:1`, panel sub-rows `h:8`. Section total = `1 + n*8 + gaps`. Plan `gridPos` accordingly.
- **Template variables**: Use backtick syntax in Loki label filters (`project=~` `` `$project` `` `)`), double quotes in stream selectors (`{service_name="claude-code"}`).

## Grafana MCP Quirks

When using Grafana MCP tools to develop or debug dashboards:

- **`list_loki_label_names`** only returns indexed stream labels (just `service_name`), NOT structured metadata. Don't rely on it for discovering event fields — use `query_loki_logs` with a log query instead.
- **`query_loki_logs` for metrics**: Use `queryType: "instant"` to test metric queries; returns `value` field with numeric result.
- **`query_loki_logs` for logs**: Returns `line` (log body text) and `labels` (structured metadata as flat object) — use this to inspect the actual data model.
- **`list_datasources`**: Use to get datasource UIDs. The Loki UID is the actual UID (e.g., `P8E80F9AEF21F6940`), NOT the Grafana variable name `${lokidatasource}`.
- **`get_dashboard_panel_queries`**: Use `uid: "claude-code-monitoring"` to inspect deployed panel queries.
- **`query_loki_stats`**: Only accepts simple label selectors — cannot test full LogQL queries. Use `query_loki_logs` instead.
- **Testing queries**: Replace Grafana variables (`$project`, `$agent`) with `.*` regex wildcard, and `$__range`/`$__auto` with explicit durations like `1h`.

## Verification Workflow

When modifying the dashboard:
1. Validate JSON syntax (parse the template output)
2. Run `make test` to ensure template tests pass
3. Rebuild and redeploy the monitoring stack (`clawker monitor down && clawker monitor up`)
4. Verify panels load correctly in Grafana (use Grafana MCP `get_dashboard_panel_queries` and `query_loki_logs`)
