---
description: Monitoring stack and Loki query guidelines
paths: ["internal/monitor/**"]
---

# Monitoring Rules

> For event schemas, Grafana patterns, and MCP quirks, see `.claude/docs/MONITORING-REFERENCE.md`.

## Telemetry Pipeline

```
Claude Code → OTLP (http/protobuf) → otel-collector → Loki (logs/events) + Prometheus (metrics)
```

- **Dockerfile template** (`bundler/assets/Dockerfile.tmpl`) sets OTEL env vars at build time
- **`env.go`** (`internal/docker/env.go`) adds runtime `OTEL_RESOURCE_ATTRIBUTES` and disables telemetry when monitoring is inactive
- Loki receives **structured metadata labels** (NOT JSON body). Log body = plain string event name

## Loki Data Model (Critical)

- **NEVER use `| json`** — log bodies are plain strings, not JSON
- **Only stream label**: `service_name` — all other fields are structured metadata
- **Event filtering**: `|= "event_name"` for log line matching
- **Metadata access**: Labels directly accessible for filtering (`| tool_name="Read"`), aggregation (`sum by (tool_name)`), unwrap (`| unwrap duration_ms`), and formatting (`{{ .tool_name }}`)

### Counter gotcha — absent values are NOT zero

`count_over_time` returns **no result** when no matching events exist. Panels must handle gracefully — use `noValue` text on panel options, not `or vector(0)`.
