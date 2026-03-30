# Egress Monitoring Dashboard — Agent Filtering Fix

## End Goal
Get the Grafana egress traffic dashboard panels (Envoy Traffic, DNS Lookups, Top Blocked Domains, Egress Over Time) to correctly filter by agent and display agent names per log entry.

## Branch
`feat/firewall-monitoring`

## Background Context

### What was built (completed)
1. **Dedicated Envoy health check listener** (port 9902) — TLS port 10000 is no longer published to the host. Health probes go to the dedicated listener. Avoids Docker's port-publish NAT rules on the traffic port.
2. **SNAT fix in firewall.sh** — Containers are on two Docker networks (default bridge eth0 + clawker-net eth1). DNAT'd packets had wrong source IP (eth0's IP) going to Envoy on clawker-net, causing the VM to masquerade to gateway. Added SNAT rules in POSTROUTING to rewrite source to the container's clawker-net IP. **Confirmed working** — Envoy access logs now show real container IPs (e.g., 172.18.0.8) instead of gateway (172.18.0.1).
3. **Agent identity emission from firewall.sh** — `emit_agent_identity()` function pushes agent_map entry (agent name + project + client_ip) directly to Loki during `enable`. Uses OTEL env vars as signal that monitoring is configured. Best-effort, silent on failure. Supplements the manager's `emitAgentMapping`.
4. **Promtail labeldrop fix** — `labeldrop` stage needed list format (`- detected_level`), not map format (`regex: detected_level`). Was crashing promtail entirely.
5. **Dashboard queries moved from pipeline filter to stream selector** — `client_ip` filtering moved from `| json | client_ip=~\`...\`` (broken through Grafana interpolation) to `{..., client_ip=~"${agent_ips:regex}"}` (works correctly).
6. **agent_ips variable** — removed `allValue: ".*"` which was causing ALL traffic to show regardless of agent selection. Now uses `includeAll: true` with NO `allValue`, so Grafana pipe-joins actual resolved IPs.
7. **DNS empty entries fix** — re-added `rcode=~".+"` stream label filter to CoreDNS panel that was accidentally dropped.

### Current Open Issue — PANELS SHOW "NO DATA"
Both Envoy Traffic (panel 55) and DNS Lookups (panel 56) show "No Data" in the live Grafana dashboard despite data existing in Loki.

#### What we know for certain
- **Loki has data**: Direct Loki API queries return correct results for both agent_map entries and access logs
- **Loki filtering works**: `{service_name="envoy", source="envoy", client_ip=~"172.18.0.8|172.18.0.9"}` returns data when queried directly against Loki (`http://loki:3100/loki/api/v1/query_range`)
- **Agent map exists**: `curl -s 'http://loki:3100/loki/api/v1/label/client_ip/values' --data-urlencode 'query={service_name="envoy", source="agent_map"}'` returns `["172.18.0.8", "172.18.0.9"]`
- **Grafana datasource proxy returns empty**: The same label_values query through `http://grafana:3000/api/datasources/proxy/uid/P8E80F9AEF21F6940/...` returns `{"status":"success"}` with NO data — possible time range or auth issue with the proxy
- **`current: {}` persists** — Setting `current: {"selected": true, "text": "All", "value": "$__all"}` in the template doesn't help because Grafana recomputes variable values for provisioned dashboards

#### Root cause hypothesis (UNCONFIRMED)
The `agent_ips` variable may be resolving to empty because:
1. Grafana's variable resolution through the Loki datasource proxy doesn't return data (confirmed empty via proxy API test)
2. This could be a time range issue — the proxy might not pass the dashboard time range to the label_values query
3. Or a variable chaining issue — `${agent:regex}` might not resolve correctly when used inside the `agent_ips` query at dashboard load time
4. The `$__all` value with no `allValue` might produce an empty regex when there are no resolved options

#### What has NOT been tried yet
- Testing the Grafana variable preview endpoint to see what `agent_ips` actually resolves to at runtime
- Adding a `start`/`end` time range to the label_values query definition
- Checking Grafana server logs for variable resolution errors
- Testing with a hardcoded `allValue` that's a valid IP regex pattern instead of `.*` (e.g., `172\.18\.0\.\d+`)
- Testing if the `agent` variable itself resolves correctly (it depends on Prometheus metrics which may not exist for the "test" agent)
- Simplifying: removing the `agent_ips` intermediary variable entirely and using `client_ip` as a direct user-facing variable

## Key Endpoints (from inside the agent container)
- **Grafana API**: `http://grafana:3000/api/...`
- **Grafana dashboard**: `http://grafana:3000/d/claude-code-monitoring/claude-code-monitoring`
- **Loki API**: `http://loki:3100/loki/api/v1/...`
- **Loki datasource UID**: `P8E80F9AEF21F6940`
- **Prometheus datasource UID**: `PBFA97CFB590B2093`
- **Dashboard UID**: `claude-code-monitoring`
- **Egress panel IDs**: 54 (row), 55 (Envoy traffic), 56 (DNS lookups), 57 (Top blocked), 58 (Egress over time)

## Grafana MCP Tools Available
- `mcp__grafana__query_loki_logs` — query Loki through Grafana
- `mcp__grafana__list_loki_label_names` / `list_loki_label_values` — inspect labels
- `mcp__grafana__get_dashboard_by_uid` / `get_dashboard_summary` — read dashboard config
- `mcp__grafana__search_dashboards` — find dashboards
- Can also query Grafana REST API directly via curl from inside the container

## Key Files
- `internal/monitor/templates/grafana-dashboard.json` — dashboard template (embedded via `//go:embed`)
- `internal/monitor/templates/promtail-config.yaml.tmpl` — promtail config
- `internal/bundler/assets/firewall.sh` — iptables + agent identity emission
- `internal/firewall/envoy.go` — Envoy config generation (health listener added)
- `internal/firewall/manager.go` — firewall manager (health port wiring, emitAgentMapping)
- `internal/config/consts.go` — port constants (envoyHealthPort = 9902)

## TODO Sequence
- [x] Dedicated health check listener for Envoy (port 9902)
- [x] SNAT fix for source IP preservation
- [x] Agent identity emission from firewall.sh
- [x] Promtail labeldrop fix
- [x] Move client_ip filter to stream selector
- [x] Remove `allValue: ".*"` from agent_ips variable
- [x] Re-add rcode filter to DNS panel
- [ ] **FIX: Diagnose why Grafana panels show "No Data" despite Loki having data** — the variable chain `agent → agent_ips → panel queries` is broken somewhere in Grafana's interpolation
- [ ] Verify filtering works: selecting "dev" shows only dev traffic, selecting "test" shows only test traffic
- [ ] Address agent name display per-line (lower priority — filtering must work first)
- [ ] Run full test suite and commit

## Lessons Learned
- **Do NOT test only against Loki directly** — the bug is in Grafana's variable interpolation/proxy layer. Always verify through Grafana's API or the dashboard itself.
- **Grafana provisioned dashboards recompute variable values on load** — setting `current` in the JSON template has no effect.
- **Pipeline filters (`| json | field=~...`) break through Grafana's variable interpolation** — use stream selectors (`{field=~"..."}`) instead for label-based filtering.
- **`allValue: ".*"` bypasses actual filtering** — it makes the regex match everything regardless of variable selection. Remove it for intermediary/lookup variables.
- **The binary must be built on macOS (host)** — the container builds Linux binaries. Dashboard template is embedded at compile time.
- **Grafana MCP tools (e.g., `list_loki_label_values`) may return different results than direct Loki API calls** — don't trust them as the source of truth for debugging.

---
**IMPERATIVE**: Always check with the user before proceeding with the next TODO item. If all work is done, ask the user if they want to delete this memory.
