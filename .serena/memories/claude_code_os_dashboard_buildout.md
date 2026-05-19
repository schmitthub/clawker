# Claude Code OpenSearch Dashboard — Handoff State 2026-05-19

## Workflow change (THIS IS THE NEW SHAPE)

User builds dashboards / visualizations / explore SOs **manually in the OSD UI**, exports the resulting saved-object JSON / NDJSON, hands it to the agent. Agent's job is then to **bake the exported asset into `bootstrap.sh.tmpl`** (or `clawker.ndjson`) so it materializes on every fresh `monitor up`.

The agent's role during construction is **research assistant only** — answer OSD/Prom/Vega questions, source-read plugins, probe the live stack, NEVER unilaterally hand-craft saved-objects from training-data recall.

Iron rules from `feedback_no_guessing_dashboard_work` + `feedback_ground_in_real_data` still apply at full force.

## Branch

- Branch: `feat/os-dashboards` — handoff happens after the workspace was switched to `features: ["use-case-all"]`. Inspect `git log` for the live tip.

## What's wired in bootstrap as of HEAD

`internal/monitor/templates/opensearch-bootstrap/bootstrap.sh.tmpl` does, in order:

1. Component-templates / index-templates / ISM policies (loops over subdirs).
2. Registers `clawker_prometheus` direct-query datasource via the SQL plugin (`/_plugins/_query/_datasources`).
3. Polls `/api/status` on OSD.
4. Imports `saved-objects/clawker.ndjson` via `/api/saved_objects/_import?overwrite=true` (5 index-pattern SOs only as of HEAD — no dashboards yet).
5. POSTs `data-connection/clawker-prometheus-conn` (idempotent via `?overwrite=true`).
6. POSTs workspace `Clawker` with **`features: ["use-case-all"]`** (skip-if-exists by name list).

Workspace id is auto-generated each fresh `monitor up`. Current live id: `5mimS6`.

## Stack restart workflow

After editing any template or `Dockerfile.tmpl`:

```sh
make clawker && \
clawker monitor init --force && \
clawker monitor down --volumes && \
clawker monitor up
```

`down --volumes` is what wipes the workspace + all SOs. Workspace IDs change. The probe SOs documented below ARE wiped — recreate on a fresh stack.

`monitor down` (no `--volumes`) preserves data; `monitor up` reruns bootstrap which is idempotent. Use this lighter cycle when only template logic (not schema/SOs) changed.

## Verified state of the live stack right now

- All four plugins green: explore, workspace, dataSource, queryEnhancements (all `@3.6.0`).
- Workspace `Clawker` (id `5mimS6`) with `features: ["use-case-all"]`.
- Data-connection `clawker-prometheus-conn` registered, `connectionId: clawker_prometheus`.
- Probe explore SOs in the workspace:
  - `probe-prom-cost` — `chartType: table`, query `claude_code_cost_usage_USD_total`. Confirmed renders standalone AND embedded in dashboard.
  - `probe-prom-line` (id `69cec650-531a-11f1-bc73-dfbcc9be49c9`) — UI-saved via "Save explore" + "Add to dashboard". `chartType: line`, `axesMapping: {x: "Time", y: "Value", color: "Series"}`. Renders in dashboard. **This is the canonical reference shape for any line panel in the future.**
- Probe dashboard `clawker-probe-dash` (workspace-scoped) — contains both panels. Time picker propagation confirmed working. No filter bar (none was wired; see "Open question" below).

## Key shapes (verified by API probe + production UI flow)

### explore SO body — UI-produced reference (line chart, PROMQL)

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
- PROMQL pipeline produces columns named `Time` (Date), `Value` (Numerical), `Series` (Categorical). axesMapping must reference these LITERAL names — NOT `@timestamp` / `@value` (those come from the PPL `source = clawker_prometheus.X` form, which is a different code path).
- `references[].type = "index-pattern"` even though clawker_prometheus is a `data-connection`. This is what the UI emits — don't second-guess it.

### dashboard SO body — UI-produced reference

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
- Production `addToDashboard` (`src/plugins/explore/public/components/visualizations/utils/add_to_dashboard.ts`) writes panels with INLINE `id` + `type`; OSD's `extractReferences` on save rewrites them to `panelRefName` + entries in `references[]`. Both shapes load; the post-extract form (`panelRefName`) is canonical when fetching via API.
- Panel size: production default is `w: 24, h: 15` (max grid width 48). Cosmetic.
- `migrationVersion.dashboard = "7.9.3"` is added by the server on POST — don't include it in the request.

### workspace ID is dynamic

Capture from `/api/workspaces/_list` by name:

```sh
WS=$(docker exec opensearch-dashboards curl -s -X POST -H 'osd-xsrf: true' \
  -H 'content-type: application/json' \
  http://localhost:5601/api/workspaces/_list -d '{}' \
  | python3 -c 'import sys,json; print([w["id"] for w in json.load(sys.stdin)["result"]["workspaces"] if w["name"]=="Clawker"][0])')
```

For bootstrap baking: capture from the workspace-create response (or list-and-filter), then template-substitute into the dashboard SO JSON before POSTing. The bootstrap pattern for SOs is `POST /api/saved_objects/<type>/<id>?overwrite=true` per-SO, or NDJSON bulk via `/api/saved_objects/_import?overwrite=true`.

### Embedding errors decoded (don't repeat past mistakes)

- `Cannot load saved visualization "<title>" with id <id>` — thrown by `src/plugins/explore/public/embeddable/explore_embeddable.tsx:415` when `chartType !== "table"`, `chartType !== "logs"`, AND `findRuleByAxesMapping(axesMapping, allColumns)` returns no matching rule. axesMapping must reference column names that bucket into the rule's expected (numerical, categorical, date) shape. `axesMapping: {}` + `chartType: "line"` is the canonical "embedding fails" combo from prior probes.
- `chartType: "table"` bypasses the rule matcher entirely (renders via `TableVis` with `searchProps.tableData`). Useful debugging probe to confirm the embedding plumbing works.

## OPEN QUESTION (do not lose this)

**Filter mechanism is not wired.** The dashboard SO has no query/filter bar UI. User-facing brief was "verify pill propagation," but no pill UI has ever been configured.

Two unanswered sub-questions:

1. **Do dashboard-level filter pills, if they existed, even propagate into the explore embeddable's PROMQL execution?** Source-read in progress: `explore_embeddable.tsx` creates a `filtersSearchSource` and parents the search source under it, but `input.filters` was NOT observed pushed onto `filtersSearchSource.setField('filter', ...)`. Only references to `input.filters` were: gating re-fetch in `updateHandler`, and passing into `searchContext` for `matchedRule.toSpec()` (i.e. Vega-side client filtering). `prepareQueryForLanguage` may inject filters into PROMQL — not yet traced. If filters don't reach the PROMQL string sent to the data source, dashboard pills are cosmetic at best.

2. **What input mechanism does the user actually want?** Options previously presented:
   - Standard OSD top filter+query bar (kuery + pills) — requires figuring out why it's absent on this dashboard and what wires it on. With `use-case-all` it MAY appear automatically; user has not yet verified post-switch.
   - Input-controls panel (dropdowns/sliders) — separate visualization panel emitting filters into dashboard state.
   - PROMQL-side label selectors via dashboard URL/params.

User has not selected one. Don't pick unilaterally.

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

# Verified Claude Code Prom counters: claude_code_session_count_total,
# claude_code_cost_usage_USD_total, claude_code_token_usage_tokens_total,
# claude_code_code_edit_tool_decision_total, claude_code_active_time_seconds_total,
# claude_code_commit_count_total, claude_code_lines_of_code_count_total
```

## Handoff instructions for next agent (same as iron rules but more)

1. **Re-read this memory completely** before responding to anything.
2. Re-read `feedback_no_guessing_dashboard_work` + `feedback_ground_in_real_data` + `feedback_clawker_container_no_direct_net` + `feedback_dashboard_filter_bar_explicit` + `feedback_believe_user_observations`.
3. Run `mcp__serena__check_onboarding_performed` first turn.
4. **The user drives construction in the UI.** Your job is research assistant + permanence engineer. Do not unilaterally write dashboard / explore / panel JSON.
5. When the user provides an export (`.ndjson` or SO JSON), bake it into `bootstrap.sh.tmpl` (per-SO POST) or `clawker.ndjson` (bulk import). Capture workspace id at runtime; template-substitute into the SO body before POSTing.
6. Don't repaint old paths. The `use-case-observability` history is dead; current is `use-case-all`. The `axesMapping: {}` + line-chart shape is dead; UI-saved `axesMapping: {x:"Time",y:"Value",color:"Series"}` is the reference.
7. Don't restart the stack proactively — ask before `monitor down --volumes`.
