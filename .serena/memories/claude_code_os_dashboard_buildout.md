# Claude Code OpenSearch Dashboard — Buildout (Handoff State 2026-05-18)

## End goal

Ship a preconfigured "Claude Code" dashboard inside OpenSearch Dashboards (OSD), auto-imported by `clawker monitor up`. Single pane of glass mixing Claude Code Prometheus counters + claude-code OS log events. Mirrors the prior Grafana dashboard at `f420327e^:internal/monitor/templates/grafana-dashboard.json`.

**Branch:** `feat/os-dashboards` (clawker repo at `/Users/andrew/Code/clawker`).
**Live OSD stack:** running locally; bootstrap-imported saved-objects + index templates.

## Iron rules (re-read every turn)

- **Empirical proof per claim.** If you can't show a probe output, don't state the behavior as fact. LLM training data for OSD 3.6 + explore plugin is unreliable.
- **One panel before scaling.** Never roll out N panels off an unverified pattern.
- **Probe live stack via `docker exec opensearch-dashboards curl http://localhost:5601/...`** — DNS for clawker-net hostnames does NOT resolve from agent containers due to firewall CoreDNS.
- **Use deepwiki for codebase Q's, web/official docs for research, NOT training-data recall.**
- **Ask user before scaling.**

## Verified working — 2026-05-18 (changes in working tree)

### 1. compose.yaml.tmpl — OSD service env + command

`internal/monitor/templates/compose.yaml.tmpl` opensearch-dashboards service:

```yaml
environment:
  - DATA_SOURCE_ENABLED=true       # registers `data-connection` SO type
  - WORKSPACE_ENABLED=true         # required by explore plugin's app-mount logic
  - VEGA_ENABLEEXTERNALURLS=true   # LEFTOVER from obsolete Vega-on-Prom path — should be removed
command:
  - opensearch-dashboards
  - --explore.enabled=true              # `explore.enabled` NOT in entrypoint env→config allowlist; pass via CLI longopt
  - --explore.discoverMetrics.enabled=true   # surfaces metrics flavor in nav + UI capability
```

The OSD docker entrypoint's `opensearch_dashboards_vars` allowlist (in `opensearch-dashboards-docker-entrypoint.sh`) accepts `workspace.enabled` and `data_source.enabled` via env. `explore.enabled` is NOT in the allowlist — must pass via compose `command:` longopt. Entrypoint forwards `"$@"` to OSD's argv: `exec "$@" ... "${longopts[@]}"`.

Plugins green after this:
- `plugin:dataSource@3.6.0 green`
- `plugin:workspace@3.6.0 green`
- `plugin:explore@3.6.0 green`
- `plugin:queryEnhancements@3.6.0 green`

### 2. otel-config.yaml.tmpl — fix `metric_expiration` bug

`internal/monitor/templates/otel-config.yaml.tmpl` prometheus exporter:

```yaml
prometheus:
  endpoint: "0.0.0.0:{{.PrometheusMetricsPort}}"
  metric_expiration: 8760h
```

**Was wrong:** prior commit `a8056eed` set `metric_expiration: 0` thinking it disabled expiration. Source `opentelemetry-collector-contrib/exporter/prometheusexporter/accumulator.go` lines 409 + 436:

```go
expirationTime := time.Now().Add(-a.metricExpiration)
if expirationTime.After(v.updated) { /* expire */ }
```

`metric_expiration: 0` → `expirationTime = Now() - 0 = Now()` → always-true expiration → all metrics evicted every Collect call. `/metrics` endpoint returned `Content-Length: 0` despite collector debug logs showing claude_code metrics flowing. Use `8760h` for "effectively never expire". See memory `project_prom_exporter_expiration_zero_means_immediate`.

After fix + rebuild: Prom `/api/v1/label/__name__/values` returned 85 metric names including `claude_code_active_time_seconds_total`, `claude_code_cost_usage_USD_total`, `claude_code_token_usage_tokens_total`.

### 3. bootstrap.sh.tmpl — auto-create data-connection + workspace

`internal/monitor/templates/opensearch-bootstrap/bootstrap.sh.tmpl` — new section appended after the saved-objects `_import` step:

```sh
# data-connection SO (idempotent via overwrite=true)
POST /api/saved_objects/data-connection/clawker-prometheus-conn?overwrite=true
body: {"attributes":{"connectionId":"clawker_prometheus","type":"Prometheus","meta":"{\"type\":\"Prometheus\",\"timeFieldName\":\"Time\"}"}}

# workspace (skip if "Clawker" already in /api/workspaces/_list)
POST /api/workspaces
body: {"attributes":{"name":"Clawker","description":"Claude Code observability","features":["use-case-observability"]},"settings":{"dataConnections":["clawker-prometheus-conn"]}}
```

Workspace IDs are auto-generated. Future bootstrap iterations that need to POST explore-flavor SOs scoped to the workspace will need to capture the id from the create response (or list-and-filter by name) — not yet wired since explore SO embedding into a regular dashboard is blocked (see below).

## PPL+Prom at API level (verified)

```
POST /api/ppl/search
{"query":"source = clawker_prometheus.claude_code_cost_usage_USD_total | stats sum(@value) by span(@timestamp, 1h)","format":"jdbc"}
→ datarows: [[<real number>, "<ts>"]]
```

Prom-PPL aggregation requires `by span(@timestamp, …)` — without it returns 500: `"Prometheus Catalog doesn't support aggregations without span expression"`.

## explore-metrics flavor — standalone page works

After bootstrap creates workspace + data-connection, the dataset picker at `/w/{wsId}/app/explore/metrics/` shows `clawker_prometheus`. Pick it, write PROMQL like `claude_code_cost_usage_USD_total`, save the explore. URL `/w/{wsId}/app/explore/metrics#/view/{id}` renders a table (and optionally a chart tab — the metrics flavor registers `id: "metrics"` Table + `id: "metrics-raw"` Raw, per `src/plugins/explore/public/application/register_tabs.ts`; no line-chart tab is registered for the metrics flavor, but the rendered MetricsTab does present a chart view).

### Verified explore SO body shape (POSTs cleanly past strict mapping)

The shape that worked (deepwiki was WRONG about `legacyState` and `queryState` being top-level — they get rejected by `strict_dynamic_mapping_exception`; the SO type `explore` server schema at `src/plugins/discover/server/saved_objects/search.js` only mapps title/description/hits/columns/sort/version/kibanaSavedObjectMeta/type/visualization/uiState):

```json
{
  "attributes": {
    "title": "probe-prom-cost",
    "description": "",
    "hits": 0,
    "columns": [],
    "sort": [],
    "version": 1,
    "type": "metrics",
    "visualization": "{\"title\":\"\",\"chartType\":\"line\",\"params\":{},\"axesMapping\":{}}",
    "uiState": "{\"activeTab\":\"explore\"}",
    "kibanaSavedObjectMeta": {
      "searchSourceJSON": "{\"query\":{\"query\":\"claude_code_cost_usage_USD_total\",\"language\":\"PROMQL\",\"dataset\":{\"id\":\"clawker_prometheus\",\"title\":\"clawker_prometheus\",\"type\":\"PROMETHEUS\",\"timeFieldName\":\"Time\",\"language\":\"PROMQL\",\"signalType\":\"metrics\",\"dataSource\":{\"meta\":{\"type\":\"Prometheus\",\"timeFieldName\":\"Time\"}}}},\"filter\":[]}"
    }
  },
  "references": [{"id":"clawker-prometheus-conn","name":"dataSource","type":"data-connection"}],
  "workspaces": ["<workspace-id>"]
}
```

## Dashboard embedding the explore SO — ERRORED (the open blocker)

Attempted at `http://localhost:5601/w/s26O_K/app/dashboards#/view/clawker-probe-dash` using dashboard SO:

```json
{
  "attributes": {
    "title": "Clawker — probe",
    "panelsJSON": "[{\"version\":\"3.6.0\",\"gridData\":{\"x\":0,\"y\":0,\"w\":48,\"h\":24,\"i\":\"1\"},\"panelIndex\":\"1\",\"embeddableConfig\":{},\"panelRefName\":\"panel_0\"}]",
    "optionsJSON": "{\"hidePanelTitles\":false,\"useMargins\":true}",
    "version": 1,
    "timeRestore": false,
    "kibanaSavedObjectMeta": {"searchSourceJSON": "{\"query\":{\"language\":\"kuery\",\"query\":\"\"},\"filter\":[]}"}
  },
  "references": [{"name":"panel_0","type":"explore","id":"probe-prom-cost"}],
  "workspaces": ["<workspace-id>"]
}
```

The panel rendered with an ERROR (user confirmed). Exact error text NOT yet captured — that is the very next thing to ask for.

### Suspected root causes (to investigate in order)

1. `embeddableConfig: {}` is empty. Explore embeddable likely requires fields. Source pointer: `src/plugins/explore/public/components/visualizations/utils/add_to_dashboard.ts` (the production path) — copy its panel/embeddable JSON construction verbatim.
2. `panelRefName` shape may be specific to the explore embeddable factory.
3. The explore embeddable may not be registered with `dashboard` plugin's embeddable host at all and might require `observability-dashboards` (the separate dashboard surface) instead.

**Source pointers** for debug:
- `src/plugins/explore/public/components/visualizations/add_to_dashboard_button.tsx` — production "Add to dashboard" UI flow
- `src/plugins/explore/public/components/visualizations/utils/add_to_dashboard.ts` — actual mutation routine
- `src/plugins/explore/public/embeddable/` — EXPLORE_EMBEDDABLE_TYPE definition + factory + create flow

## Verified PPL Prom counters (after collector fix)

The 7 canonical Claude Code counters present in Prom right now:
- `claude_code_session_count_total`
- `claude_code_cost_usage_USD_total` — labels: agent, project, session_id, model, …
- `claude_code_token_usage_tokens_total` — extra label `type` ∈ {input, output, cacheRead, cacheCreation}
- `claude_code_code_edit_tool_decision_total`
- `claude_code_active_time_seconds_total` — extra label `type` ∈ {cli, user}
- `claude_code_commit_count_total`
- `claude_code_lines_of_code_count_total`

Verified live this session that `claude_code_active_time_seconds_total`, `claude_code_cost_usage_USD_total`, `claude_code_token_usage_tokens_total` all return real values via `prometheus:9090/api/v1/query?query=<name>`.

## What's in the working tree (uncommitted as of this memory write)

- `internal/monitor/templates/compose.yaml.tmpl` — added DATA_SOURCE_ENABLED, WORKSPACE_ENABLED, command override with `--explore.enabled` + `--explore.discoverMetrics.enabled`. VEGA_ENABLEEXTERNALURLS still present (LEFTOVER — should be dropped).
- `internal/monitor/templates/otel-config.yaml.tmpl` — `metric_expiration: 0` → `metric_expiration: 8760h`.
- `internal/monitor/templates/opensearch-bootstrap/bootstrap.sh.tmpl` — POSTs data-connection SO + workspace at the end. Idempotent on both (data-connection via overwrite=true, workspace via list-and-skip).

Nothing committed yet for this session.

## Tasks (running)

| # | State | What |
|---|---|---|
| #5 | done | Workspace + data-connection baked into bootstrap.sh.tmpl |
| NEW | pending | Capture exact dashboard-embed panel error from browser |
| NEW | pending | Resolve dashboard embedding of explore SO — read `add_to_dashboard.ts` source |
| NEW | pending | Once embedding works: bake explore SOs + dashboard SO into bootstrap (workspace id captured at runtime — explore SOs POST per-counter after workspace create) |
| NEW | pending | Remove `VEGA_ENABLEEXTERNALURLS=true` from compose (obsolete) |
| NEW | pending | Commit working-tree changes after embedding resolved |

## Handoff instructions for next agent

1. **Re-read this memory completely.** Then re-read iron rules at the top. Run `mcp__serena__check_onboarding_performed`. Read `feedback_no_guessing_dashboard_work` and `feedback_ground_in_real_data` memories before responding.
2. **Confirm current stack state** before doing anything: `docker exec opensearch-dashboards curl -s http://localhost:5601/api/status` — ensure plugins explore/workspace/dataSource/queryEnhancements all green.
3. **First step:** ask user to share the exact error text rendered in the dashboard panel area. URL is `http://localhost:5601/w/<workspace-id>/app/dashboards#/view/clawker-probe-dash`. Workspace id changes after every `monitor down --volumes && monitor up` (bootstrap creates a fresh one); find current with `docker exec opensearch-dashboards curl -s -X POST -H 'osd-xsrf: true' -H 'content-type: application/json' http://localhost:5601/api/workspaces/_list -d '{}'`.
4. **Do NOT scale** to more panels until the embedding error is resolved AND ONE dashboard panel renders end-to-end with time picker + filter pill propagation verified.
5. **Workspace ID is dynamic.** After bootstrap, fetch fresh id via `_list` (workspace named "Clawker").
6. After embedding works: bake explore SOs + dashboard SO into bootstrap.sh.tmpl. Strategy: capture workspace id from the workspace-create response, write to /tmp, then template-substitute into the explore + dashboard JSON before POSTing.
