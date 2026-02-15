# Grafana Dashboard: Prometheus → Loki Migration for Token/Cost Panels

## Status: IMPLEMENTATION COMPLETE — PIE CHART LEGEND FIX APPLIED, NEEDS LIVE VERIFICATION

## What Was Done

Switched all token/cost panels in `internal/monitor/templates/grafana-dashboard.json` from Prometheus `increase()` queries to Loki `sum_over_time(unwrap ...)` queries on `api_request` structured log events. This eliminates counter arithmetic inaccuracies caused by high-cardinality label explosion and counter reset artifacts.

### Root Cause of Inaccuracy

Prometheus `increase()` on OTLP counter metrics (`claude_code_token_usage_tokens_total`, `claude_code_cost_usage_USD_total`) produced wildly inaccurate values:
- Cache read: 1.78M in Grafana vs 42k actual (~42x over)
- Input: 4.45k vs 14k actual (~3x under)
- Output: 5.29k vs 23k actual (~4x under)
- Cost: $0.924 vs $1.66 (~1.8x under)

Likely causes: `session_id` + `model` labels create many short-lived series, counter resets when sessions end, and `increase()` extrapolation.

### Loki Query Pattern Used

```logql
sum(sum_over_time(
  {service_name="claude-code"} |= `api_request`
  | project=~`$project` | agent=~`$agent` | session_id=~`$session_id`
  | unwrap <field> [$__range]
))
```

Fields available on `api_request` events: `cost_usd`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens`

### Panels Changed

| Panel | ID | Change |
|-------|----|--------|
| Total Cost | 3 | Prometheus → Loki, `unwrap cost_usd` |
| Input Tokens | 4 | Prometheus → Loki, `unwrap input_tokens` |
| Output Tokens | 40 | Prometheus → Loki, `unwrap output_tokens` |
| Cache Read | 41 | Prometheus → Loki, `unwrap cache_read_tokens` |
| **Cache Creation** | **52** | **NEW** — Loki, `unwrap cache_creation_tokens`, dark-purple |
| Cost by Model | 8 | Prometheus → Loki timeseries, `$__auto` range |
| Token Usage by Type | 9 | Prometheus (1 target) → Loki (4 targets: input, output, cacheRead, cacheCreation) |
| Cost by Project/Agent | 21 | Prometheus → Loki pie, `label_format pa` + transformations |
| Tokens by Project/Agent | 22 | Prometheus → Loki pie, binary `+` of 4 fields + transformations |
| Sessions by Project/Agent | 23 | Prometheus → **Mixed** (Loki: cost/tokens, Prometheus: sessions count) |
| Session Details | 31 | Prometheus → **Mixed** (Loki: cost/tokens, Prometheus: lines/time) |

### Overview Row Rebalanced

All 8 stat panels now at `w=3` each (was mixed 3/4 widths with 7 panels):

| Panel | x | w |
|-------|---|---|
| Total Sessions (id 2) | 0 | 3 |
| Total Cost (id 3) | 3 | 3 |
| Input Tokens (id 4) | 6 | 3 |
| Output Tokens (id 40) | 9 | 3 |
| Cache Read (id 41) | 12 | 3 |
| Cache Creation (id 52) | 15 | 3 |
| Lines Changed (id 5) | 18 | 3 |
| Active Time (id 6) | 21 | 3 |

### Housekeeping

- Fixed `"instant": true` → `"queryType": "instant"` on ALL Loki targets (panels 44, 50, 51, 46)
- Added `"noValue": "0"` to `fieldConfig.defaults` on all stat panels (2, 3, 4, 40, 41, 52, 5, 6)
- Added `labelsToFields` → `merge` → `organize` transformations to pie charts (21, 22) and mixed tables (23, 31) per MONITORING-REFERENCE.md

### Panels Staying on Prometheus (no Loki equivalent)

- Total Sessions (id 2) — unique session_id counting
- Lines Changed (id 5)
- Active Time (id 6)
- Lines of Code Over Time (id 11)
- Commits & PRs (id 12)

## Bug Fix: Pie Chart "Value #A" Legend

After initial deploy, pie charts (panels 21, 22) showed "Value #A" instead of project/agent names.

**Root cause**: Loki instant queries return separate data frames with field-level labels (not frame-level like Prometheus). After `labelsToFields` → `merge` → `organize`, the result is a flat table with rows. Pie charts need `reduceOptions.values: true` to read each row as a slice (using the text column for names and value column for sizes). With `values: false`, it collapses everything into one slice named "Value #A".

**Fix applied**:
- Panel 21 (Cost by Project/Agent): `reduceOptions.values` → `true`, `calcs` → `["lastNotNull"]`
- Panel 22 (Tokens by Project/Agent): `reduceOptions.values` → `true`, `calcs` → `["lastNotNull"]`

This matches the MONITORING-REFERENCE.md guidance: "works for pie charts (`reduceOptions.values: true`)".

**Confirmed via deepwiki (grafana/loki)**:
- `unwrap` correctly parses numeric string structured metadata for `sum_over_time`
- Binary `+` of `sum by (x)` on unwrapped fields produces correct results
- No double-counting gotchas with `sum_over_time` on structured metadata
- Data accuracy of the Loki approach is sound; the issue was purely rendering

## Verification Steps (NOT YET DONE)

1. ✅ JSON validates: `python3 -c "import json; json.load(open('...'))"` — PASS
2. ✅ `make test` — no new failures (6 pre-existing slug test failures unrelated)
3. ⬜ `make clawker && clawker monitor init --force && clawker monitor down --volumes && clawker monitor up` — full redeploy
4. ⬜ Compare Grafana stat panels against Claude Code statusline values
5. ⬜ Cross-check stat totals against API Requests log panel
6. ⬜ Verify pie charts render correctly with transformations
7. ⬜ Verify mixed-datasource tables merge Loki + Prometheus frames correctly

## Key Technical Notes

- Loki instant queries use `queryType: "instant"` (NOT `instant: true` which is Prometheus convention)
- Pie charts with Loki instant queries REQUIRE `labelsToFields` → `merge` → `organize` transformations
- Mixed datasource panels use `"type": "datasource", "uid": "-- Mixed --"` at panel level, with per-target datasource objects
- Timeseries panels use `$__auto` for Loki range (NOT `$__rate_interval` which is Prometheus-specific)
- `label_format pa=...` creates a combined dimension for pie chart grouping since Loki can't natively `sum by (project, agent)` in a way pie charts consume
