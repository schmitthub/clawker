# Claude Code OpenSearch Dashboard — Living Current State

## Workflow shape

User builds dashboards / visualizations / explore SOs **manually in the OSD UI**, exports the resulting saved-object JSON / NDJSON, hands it to the agent. Agent's job is to **bake the exported asset into `bootstrap.sh.tmpl`** (or `clawker.ndjson`) so it materializes on every fresh `monitor up`.

The agent's role during construction is **research assistant only** — answer OSD/Prom/Vega questions, source-read plugins, probe the live stack, NEVER unilaterally hand-craft saved-objects from training-data recall.

Iron rules from [[feedback_no_guessing_dashboard_work]] + [[feedback_ground_in_real_data]] apply at full force.

## Open issues

### 1. OS SQL direct-query Prometheus connector chokes on `type` label

OSD Explore's Metrics UI returns `Could not resolve subtype of [class PrometheusResult]: missing type id property 'type'` for any Prom metric whose label set contains a bare key named `type`.

- Affected metrics: `claude_code_active_time_seconds_total` (`type=cli|user`), `claude_code_lines_of_code_count_total` (`type=added|removed`), `claude_code_token_usage_tokens_total` (`type=input|output|cacheRead|cacheCreation`).
- Root cause: `direct-query/.../ExecuteDirectQueryActionResponse.parseResult()` uses a brittle `rawResult.contains("\"type\":")` substring check to decide whether to wrap the Prom response with `{"type":"prometheus",...}`. Any label literally named `type` flips the flag, so the wrap is skipped, Jackson finds no discriminator at root, deserialization fails. Module is `@opensearch.experimental` (PR #4375, 2025-09-26). No upstream issue tracks it yet. #5251 is a different scalar-result bug.
- Alt query paths that work today: PromQL via `/api/v1/query` on Prom, Prom UI on `:9090`, PPL `source = clawker_prometheus.<metric>` (different code path).
- Decision: don't ship a local OTTL rename — would diverge from Claude Code's published metric label names; fix is upstream (~3 lines: `objectMapper.readTree(rawResult).has("type")` or unconditional wrap). File issue + carry doc-only caveat until landed.

## Bootstrap state (as of HEAD)

`internal/monitor/templates/opensearch-bootstrap/bootstrap.sh.tmpl` runs:

1. Component-templates → ingest-pipelines → index-templates → ISM policies (loops over subdirs; ingest-pipelines PUT before index-templates so `settings.index.default_pipeline` / `final_pipeline` references resolve at ingest time).
2. Registers `clawker_prometheus` direct-query datasource (SQL plugin `/_plugins/_query/_datasources`).
3. Polls `/api/status` on OSD.
4. POSTs `data-connection/clawker-prometheus-conn` (idempotent via `?overwrite=true`).
5. POSTs workspace `Clawker` with `features: ["use-case-all"]` (skip-if-exists by name).
6. Imports `saved-objects/clawker.ndjson` into the workspace via `/w/<wsId>/api/saved_objects/_import?overwrite=true`.

### Active ingest pipelines

- `cp-actor-attr-nest` (default_pipeline on `clawker-cp`) — collapses flat dotted `attributes.actor_attr.<k>` keys into a single nested `actor_attr` `flat_object`.
- `claude-code-prompt-nest` (default_pipeline on `claude-code`) — collapses scalar `attributes.prompt` + sibling `prompt.id` into one `{value, id}` object.
- `envelope-normalize` (**final_pipeline on all 5 indices**) — mirrors SS4O envelope into legacy paths OSD reads: `severity.{text,number}` → `severityText`/`severityNumber`, flat `resource.<k>` → nested `resource.attributes.<k>`. Required because OSD's explore plugin hard-codes default log columns at `['body','severityText','resource.attributes.service.name']` but the opensearchexporter writes the canonical SS4O shape; multiple open upstream issues with no merged fix (see "Gotchas" below).

Pipeline body edits land via plain `monitor up` (PUT replaces in place). Changing which pipeline NAME an index uses requires the volume-wipe cycle (binding is set at index creation).

## Stack restart workflow

After editing any template, `Dockerfile.tmpl`, or pinned image:

```sh
make clawker && \
clawker monitor init --force && \
clawker monitor down --volumes && \
clawker monitor up
```

`down --volumes` wipes the workspace + all SOs (workspace IDs change every cycle). `monitor down` (no `--volumes`) preserves data; `monitor up` reruns bootstrap idempotently — use this lighter cycle when only pipeline/template logic (not schema/SOs) changed.

Inside an agent container, never run these host-side ops; ask the user. ([[feedback_no_host_clawker_in_container]])

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
- PROMQL pipeline produces columns named `Time` (Date), `Value` (Numerical), `Series` (Categorical). `axesMapping` must reference these LITERAL names — NOT `@timestamp` / `@value` (those come from the PPL `source = clawker_prometheus.X` form, a different code path).
- `references[].type = "index-pattern"` even though `clawker_prometheus` is a `data-connection`. This is what the UI emits — don't second-guess it.

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

### Workspace ID is dynamic

Capture from `/api/workspaces/_list` by name:

```sh
WS=$(docker exec opensearch-dashboards curl -s -X POST -H 'osd-xsrf: true' \
  -H 'content-type: application/json' \
  http://localhost:5601/api/workspaces/_list -d '{}' \
  | python3 -c 'import sys,json; print([w["id"] for w in json.load(sys.stdin)["result"]["workspaces"] if w["name"]=="Clawker"][0])')
```

For bootstrap baking: capture from the workspace-create response (or list-and-filter), then template-substitute into the dashboard SO JSON before POSTing. The bootstrap pattern for SOs is `POST /api/saved_objects/<type>/<id>?overwrite=true` per-SO, or NDJSON bulk via `/api/saved_objects/_import?overwrite=true`.

## OPEN QUESTION: filter mechanism

**Filter mechanism is not wired.** The dashboard SO has no query/filter bar UI. User-facing brief was "verify pill propagation," but no pill UI has ever been configured.

Two unanswered sub-questions:

1. **Do dashboard-level filter pills, if they existed, even propagate into the explore embeddable's PROMQL execution?** Source-read in progress: `explore_embeddable.tsx` creates a `filtersSearchSource` and parents the search source under it, but `input.filters` was NOT observed pushed onto `filtersSearchSource.setField('filter', ...)`. Only references to `input.filters` were: gating re-fetch in `updateHandler`, and passing into `searchContext` for `matchedRule.toSpec()` (i.e. Vega-side client filtering). `prepareQueryForLanguage` may inject filters into PROMQL — not yet traced. If filters don't reach the PROMQL string sent to the data source, dashboard pills are cosmetic at best.

2. **What input mechanism does the user actually want?** Options previously presented:
   - Standard OSD top filter+query bar (kuery + pills) — requires figuring out why it's absent on this dashboard and what wires it on. With `use-case-all` it MAY appear automatically; user has not yet verified post-switch.
   - Input-controls panel (dropdowns/sliders) — separate visualization panel emitting filters into dashboard state.
   - PROMQL-side label selectors via dashboard URL/params.

User has not selected one. Don't pick unilaterally. ([[feedback_dashboard_filter_bar_explicit]])

## Gotchas worth not relitigating

- **SS4O divergence between exporter wire shape and OSD UI bindings is real and upstream-unfixed.** opensearchexporter `ss4o` mode writes `severity.{text,number}` nested + flat `resource.<k>`; OSD's explore plugin reads top-level `severityText` + nested `resource.attributes.service.name`. Open issues without merged fixes: opensearch-project/data-prepper#5791, opensearch-project/opensearch-catalog#118, open-telemetry/opentelemetry-collector-contrib#45428. Our `envelope-normalize` ingest pipeline mirrors the canonical SS4O paths into the legacy paths OSD reads. Don't try `mode: ecs` / `flatten_attributes` — collapses the namespace separation `ingest_source=untrusted_otlp` stamping depends on.
- **OS resource attrs land FLAT (`resource.<k>`), not nested at `resource.attributes.<k>`.** ([[project_otel_os_exporter_flat_resource]]) — index template pre-mappings under `resource.attributes` are populated only by the `envelope-normalize` mirror, not the exporter.
- **`mapping: {dedup: true, dedot: true}` on opensearchexporter is silently no-op in SS4O mode.** `encoder.go:86 if m.sso { return encodeLogSSO }` skips both. ([[feedback_trace_dispatch_before_trusting_config_option]])
- **CP source (`internal/controlplane/dockerevents/dispatch.go` actor_attr fanout, zerolog OTel writer, etc.) is OFF-LIMITS for monitoring fixes.** ([[feedback_cp_source_off_limits_for_monitoring_fixes]]) — fix at collector / OS template / ingest pipeline.
- **`disable_objects: true` mapping fails on multi-segment dotted children** (`Cannot add nested object field [attributes.actor_attr.org]`). Use `flat_object` + an ingest pipeline that nests the dotted keys first.
- **OTTL `transform/logs` has no map-iteration construct** — works only for bounded collision-key sets (e.g. claude-code's `prompt`/`prompt.id`), DEAD-END for unbounded sets (e.g. cp `actor_attr.<docker labels>`).
- **Claude Code SDK + Envoy native OTel ALS ship log records with `SeverityNumber=Unspecified(0)` + `SeverityText=""`.** Not a pipeline drop; producers genuinely don't set severity. Verified at the raw OTLP debug exporter. `severityText` blank for those two indices is correct truth.
- **Embedding errors:** `Cannot load saved visualization "<title>" with id <id>` is thrown by `src/plugins/explore/public/embeddable/explore_embeddable.tsx:415` when `chartType !== "table"`, `chartType !== "logs"`, AND `findRuleByAxesMapping(axesMapping, allColumns)` returns no matching rule. `axesMapping: {}` + `chartType: "line"` is the canonical "embedding fails" combo. `chartType: "table"` bypasses the rule matcher entirely (renders via `TableVis`).
- **`use-case-observability` is dead history; current is `use-case-all`.** Don't repaint.

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

# Verified Claude Code Prom counters: claude_code_session_count_total,
# claude_code_cost_usage_USD_total, claude_code_token_usage_tokens_total,
# claude_code_code_edit_tool_decision_total, claude_code_active_time_seconds_total,
# claude_code_commit_count_total, claude_code_lines_of_code_count_total
```

## Handoff instructions for next agent

1. **Re-read this memory completely** before responding to anything.
2. Re-read [[feedback_no_guessing_dashboard_work]] + [[feedback_ground_in_real_data]] + [[feedback_clawker_container_no_direct_net]] + [[feedback_dashboard_filter_bar_explicit]] + [[feedback_believe_user_observations]] + [[feedback_no_host_clawker_in_container]].
3. Run `mcp__serena__check_onboarding_performed` first turn.
4. **The user drives construction in the UI.** Your job is research assistant + permanence engineer. Do not unilaterally write dashboard / explore / panel JSON.
5. When the user provides an export (`.ndjson` or SO JSON), bake it into `bootstrap.sh.tmpl` (per-SO POST) or `clawker.ndjson` (bulk import). Capture workspace id at runtime; template-substitute into the SO body before POSTing.
6. Don't restart the stack proactively — ask before `monitor down --volumes`.
