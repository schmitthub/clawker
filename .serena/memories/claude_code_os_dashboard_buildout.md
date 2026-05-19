# Claude Code OpenSearch Dashboard — Status: OPEN

## Scope

Substrate is shipped. Future work: build out the **pre-made dashboard suite** that users get on every `monitor up`. Today the bake includes a starter `Claude Code` dashboard with a KPI strip + filter-controls panel; goal is a richer, opinionated set covering session activity, cost/token economics, tool decisions, agentic loops, edit velocity, etc.

## Workflow

User builds dashboards / visualizations / explore SOs **manually in the OSD UI**, exports the saved-object JSON / NDJSON, hands it to the agent. Agent's job is to **bake the exported asset into `internal/monitor/templates/opensearch-bootstrap/saved-objects/clawker.ndjson`** (or per-SO POSTs in `bootstrap.sh.tmpl`) so it materializes on every fresh `monitor up`.

The agent's role during construction is **research assistant only** — answer OSD/Prom/Vega questions, source-read plugins, probe the live stack, NEVER unilaterally hand-craft saved-objects from training-data recall.

Iron rules: [[feedback_no_guessing_dashboard_work]], [[feedback_ground_in_real_data]], [[feedback_dashboard_filter_bar_explicit]], [[feedback_believe_user_observations]], [[feedback_no_host_clawker_in_container]], [[feedback_clawker_container_no_direct_net]].

## Stack restart workflow

After editing any template, `Dockerfile.tmpl`, or pinned image:

```sh
make clawker && \
clawker monitor init --force && \
clawker monitor down --volumes && \
clawker monitor up
```

`down --volumes` wipes the workspace + all SOs (workspace IDs regenerate every cycle) AND the Prom TSDB. `monitor down` (no `--volumes`) preserves data; `monitor up` reruns bootstrap idempotently — use this lighter cycle when only pipeline/template logic changed and no Prom label rewrites are involved.

Inside an agent container, never run these host-side ops; ask the user.

## Key SO body shapes (reusable templates for new panels)

### explore SO body — line chart, PROMQL

```json
{
  "type": "explore",
  "attributes": {
    "title": "<name>",
    "description": "",
    "hits": 0,
    "columns": ["_source"],
    "sort": [],
    "version": 1,
    "type": "metrics",
    "visualization": "{\"title\":\"\",\"chartType\":\"line\",\"params\":{...defaults from line_vis_config.ts...},\"axesMapping\":{\"x\":\"Time\",\"y\":\"Value\",\"color\":\"Series\"}}",
    "uiState": "{\"activeTab\":\"explore_visualization_tab\"}",
    "kibanaSavedObjectMeta": {
      "searchSourceJSON": "{\"query\":{\"query\":\"<PROMQL>\",\"language\":\"PROMQL\",\"dataset\":{\"id\":\"clawker_prometheus\",\"title\":\"clawker_prometheus\",\"type\":\"PROMETHEUS\",\"language\":\"PROMQL\",\"timeFieldName\":\"Time\",\"dataSource\":{},\"signalType\":\"metrics\"}},\"filter\":[],\"indexRefName\":\"kibanaSavedObjectMeta.searchSourceJSON.index\"}"
    }
  },
  "references": [{"name":"kibanaSavedObjectMeta.searchSourceJSON.index","type":"index-pattern","id":"clawker_prometheus"}],
  "workspaces": ["<wsId>"]
}
```

Notes:
- PROMQL pipeline produces columns named `Time` (Date), `Value` (Numerical), `Series` (Categorical). `axesMapping` must reference these LITERAL names — NOT `@timestamp` / `@value` (those come from the PPL `source = clawker_prometheus.X` form, a different code path).
- `references[].type = "index-pattern"` even though `clawker_prometheus` is a `data-connection`. This is what the UI emits — don't second-guess it.

### dashboard SO body

```json
{
  "type": "dashboard",
  "attributes": {
    "title": "<title>",
    "panelsJSON": "[{\"version\":\"3.6.0\",\"panelIndex\":\"<uuid>\",\"gridData\":{\"i\":\"<uuid>\",\"x\":0,\"y\":0,\"w\":24,\"h\":15},\"panelRefName\":\"panel_0\"}, ...]",
    "optionsJSON": "{\"hidePanelTitles\":false,\"useMargins\":true}",
    "version": 1,
    "timeRestore": false,
    "kibanaSavedObjectMeta": {"searchSourceJSON":"{\"query\":{\"language\":\"kuery\",\"query\":\"\"},\"filter\":[]}"}
  },
  "references": [{"name":"panel_0","type":"explore","id":"<explore-so-id>"}, ...],
  "workspaces": ["<wsId>"]
}
```

Notes:
- Production `addToDashboard` writes panels with INLINE `id` + `type`; OSD's `extractReferences` on save rewrites them to `panelRefName` + entries in `references[]`. Both shapes load; the post-extract form (`panelRefName`) is canonical when fetching via API.
- Default panel size: `w: 24, h: 15` (max grid width 48).
- `migrationVersion.dashboard = "7.9.3"` is server-added on POST — don't include in the request.

### Workspace ID is dynamic

Captured at runtime by `bootstrap.sh` from the workspace-create response (or list-and-filter by name "Clawker"), then template-substituted into the SO body before POSTing. Bulk import via `/w/<wsId>/api/saved_objects/_import?overwrite=true`. The existing bake logic already handles this — new dashboards just need their `workspaces: ["<wsId>"]` and any internal references templated the same way.

## Gotchas worth not relitigating

- **SS4O divergence between exporter wire shape and OSD UI bindings is real and upstream-unfixed.** opensearchexporter `ss4o` mode writes `severity.{text,number}` nested + flat `resource.<k>`; OSD's explore plugin reads top-level `severityText` + nested `resource.attributes.service.name`. Open upstream without merged fixes: opensearch-project/data-prepper#5791, opensearch-project/opensearch-catalog#118, open-telemetry/opentelemetry-collector-contrib#45428. Our `envelope-normalize` ingest pipeline (final_pipeline on all 5 indices) mirrors canonical SS4O paths into the legacy paths OSD reads. Don't try `mode: ecs` / `flatten_attributes` — collapses the namespace separation `ingest_source=untrusted_otlp` stamping depends on.
- **Resource attrs land FLAT (`resource.<k>`) AND nested (`resource.attributes.<k>`).** Exporter writes flat; envelope-normalize mirrors flat → nested. New panels: prefer flat `resource.<k>` (canonical); nested also works for OSD UI default columns. [[project_otel_os_exporter_flat_resource]]
- **`mapping: {dedup: true, dedot: true}` on opensearchexporter is silently no-op in SS4O mode.** [[feedback_trace_dispatch_before_trusting_config_option]]
- **CP source is OFF-LIMITS for monitoring fixes.** Fix at collector / OS template / ingest pipeline. [[feedback_cp_source_off_limits_for_monitoring_fixes]]
- **`disable_objects: true` mapping fails on multi-segment dotted children**. Use `flat_object` + an ingest pipeline that nests the dotted keys first.
- **OTTL `transform/logs` has no map-iteration construct** — works only for bounded collision-key sets, DEAD-END for unbounded sets.
- **Claude Code SDK + Envoy native OTel ALS ship records with `SeverityNumber=Unspecified(0)` + `SeverityText=""`.** Not a pipeline drop; producers genuinely don't set severity. `severityText` blank for those two indices is correct truth.
- **Embedding errors:** `Cannot load saved visualization "<title>" with id <id>` is thrown by `src/plugins/explore/public/embeddable/explore_embeddable.tsx:415` when `chartType !== "table"`, `chartType !== "logs"`, AND `findRuleByAxesMapping(axesMapping, allColumns)` returns no matching rule. `axesMapping: {}` + `chartType: "line"` is the canonical "embedding fails" combo. `chartType: "table"` bypasses the rule matcher entirely.
- **`use-case-observability` is dead history; current is `use-case-all`.**
- **Prom `type` label is renamed to `kind` at the OTel collector** (workaround for OS SQL plugin's direct-query Prom connector). Affects PROMQL panels on `claude_code_token_usage_tokens_total`, `claude_code_active_time_seconds_total`, `claude_code_lines_of_code_count_total`. Full context + removal criteria: `.claude/docs/MONITORING-REFERENCE.md` → "Prometheus Metric Labels".
- **Filter wiring on a dashboard is explicit.** OSD does not auto-show a top filter bar; if a new dashboard needs filters, wire an input-controls panel or PROMQL-side label selectors. Whether dashboard-level pills propagate into explore-embeddable's PROMQL execution is unverified — source-read shows `input.filters` is NOT pushed onto the embeddable's `filtersSearchSource.setField('filter', ...)`. Treat dashboard pills as cosmetic unless proven otherwise for the panel type in question. [[feedback_dashboard_filter_bar_explicit]]

## Probe commands cheat sheet

```sh
# Plugin health
docker exec opensearch-dashboards curl -s http://localhost:5601/api/status | jq

# List workspaces
docker exec opensearch-dashboards curl -s -X POST -H 'osd-xsrf: true' \
  -H 'content-type: application/json' \
  http://localhost:5601/api/workspaces/_list -d '{}' | jq

# Fetch a single SO
docker exec opensearch-dashboards curl -s -H 'osd-xsrf: true' \
  "http://localhost:5601/w/<wsId>/api/saved_objects/<type>/<id>" | jq

# Search SOs by title
docker exec opensearch-dashboards curl -s -H 'osd-xsrf: true' \
  "http://localhost:5601/w/<wsId>/api/saved_objects/_find?type=<type>&search_fields=title&search=<title>" | jq

# Prom column-shape via PPL (NOTE: differs from explore-PROMQL pipeline)
docker exec opensearch-dashboards curl -s -X POST -H 'osd-xsrf: true' \
  -H 'content-type: application/json' http://localhost:5601/api/ppl/search \
  -d '{"query":"source = clawker_prometheus.<metric>","format":"jdbc"}' | jq

# Prom metric list
docker exec prometheus curl -s 'http://localhost:9090/api/v1/label/__name__/values' | jq

# Pipeline run counters
docker exec opensearch-dashboards curl -s 'http://opensearch-node:9200/_nodes/stats/ingest?filter_path=nodes.*.ingest.pipelines' | jq

# Raw OTLP record severity at collector (debug exporter dumps to stdout)
docker logs otel-collector 2>&1 | awk '/ResourceLog/{block=1} /SeverityText/{if(block)print}'
```

Verified Claude Code Prom counters: `claude_code_session_count_total`, `claude_code_cost_usage_USD_total`, `claude_code_token_usage_tokens_total`, `claude_code_code_edit_tool_decision_total`, `claude_code_active_time_seconds_total`, `claude_code_commit_count_total`, `claude_code_lines_of_code_count_total`, `claude_code_pull_request_count_total`. Counters carrying `kind` label (renamed from upstream `type`): `active_time_seconds_total` (cli|user), `token_usage_tokens_total` (input|output|cacheRead|cacheCreation), `lines_of_code_count_total` (added|removed).

## Handoff

1. Re-read this memory completely before responding.
2. Re-read the linked feedback memories above.
3. Run `mcp__serena__check_onboarding_performed` first turn.
4. **User drives construction in the UI.** Agent role: research assistant + permanence engineer. Don't unilaterally write dashboard / explore / panel JSON.
5. When user provides an export, bake it into `clawker.ndjson` (bulk import) or per-SO POSTs in `bootstrap.sh.tmpl`. Workspace id is captured at runtime — template-substitute into the SO body and any cross-SO `references[].id` before POSTing.
6. Don't restart the stack proactively — ask before `monitor down --volumes`.
