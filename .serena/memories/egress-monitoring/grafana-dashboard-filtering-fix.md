# Egress Monitoring Dashboard — Agent Filtering & "No Data" Fix

## End Goal
Get the Grafana egress traffic dashboard panels (Envoy Traffic, DNS Lookups, Top Blocked Domains, Egress Over Time) to correctly filter by agent and display data. Fix all regressions introduced by previous agent's work.

## Branch
`feat/firewall-monitoring`

## Background Context

### Architecture (critical — understand before touching anything)
- Envoy/CoreDNS logs come through **Promtail** → Loki. These logs have `client_ip` as a stream label but **NO agent name** — Envoy only sees source IPs.
- Agent identity is pushed **directly to Loki's HTTP API** from two places:
  1. `internal/bundler/assets/firewall.sh` → `emit_agent_identity()` — runs inside agent container during `enable_firewall`, pushes to `http://loki:3100`
  2. `internal/firewall/manager.go` → `emitAgentMapping()` — runs on host during `EnableForContainer`, pushes to `http://localhost:<lokiPort>`
- These create `{source="agent_map"}` entries with labels: `service_name=envoy`, `source=agent_map`, `agent=<name>`, `client_ip=<ip>`, `project=<name>`, `action=enable|disable`
- The dashboard variable chain: `project` (Prometheus) → `agent` (Prometheus, filtered by project) → `agent_ips` (Loki label_values on agent_map, filtered by project+agent) → panel queries use `client_ip=~"${agent_ips:regex}"`
- **All containers are on `clawker-net`** — agent containers CAN resolve `loki:3100`. Do not assume network isolation.
- OTEL provides `project` and `agent` as separate resource attributes → separate Prometheus labels. The `agent` label is just the short name (e.g., "dev"), NOT fully qualified.
- The dashboard template is embedded via `//go:embed` — changes require binary rebuild on macOS host.

### What was built previously (completed, still valid)
1. Dedicated Envoy health check listener (port 9902)
2. SNAT fix for source IP preservation — confirmed working
3. Agent identity emission from firewall.sh
4. Promtail labeldrop fix
5. Dashboard queries moved from pipeline filter to stream selector
6. DNS empty entries fix (rcode filter)

### What THIS session accomplished
1. **Diagnosed root cause of "No Data"**: The `agent_ips` variable had no `allValue`, and agent_map entries weren't in Loki (pushes failed because monitoring wasn't up when containers started). With empty `agent_ips`, `client_ip=~""` matched nothing.
2. **Fixed `allValue` on `agent_ips`**: Added `allValue: ".*"` back. The previous agent removed it thinking it bypassed filtering — WRONG. `allValue` only applies when "All" is selected; individual agent selections use resolved values.
3. **Added `project` filter to `agent_ips` query**: The lookup was `label_values({source="agent_map", agent=~"..."}, client_ip)` — not namespaced by project. A "dev" agent in projectA and projectB would collide. Fixed to include `project=~"^(${project:regex})$"`.
4. **Confirmed agent_map push works**: Tested from inside container — `curl POST http://loki:3100/loki/api/v1/push` returns 204, entries appear in Loki AND are visible through Grafana's proxy.
5. **Confirmed agent_map populated on fresh restart**: After user rebuilt binary + restarted everything, agent_map had entries for both dev (172.18.0.8) and test (172.18.0.9).

## Key Endpoints (from inside agent container)
- **Grafana API**: `http://grafana:3000/api/...`
- **Loki API**: `http://loki:3100/loki/api/v1/...`
- **Loki datasource UID**: `P8E80F9AEF21F6940`
- **Prometheus datasource UID**: `PBFA97CFB590B2093`
- **Dashboard UID**: `claude-code-monitoring`
- **Egress panel IDs**: 54 (row), 55 (Envoy traffic), 56 (DNS lookups), 57 (Top blocked), 58 (Egress over time)

## Key Files
- `internal/monitor/templates/grafana-dashboard.json` — dashboard template (embedded via `//go:embed`)
- `internal/monitor/templates/promtail-config.yaml.tmpl` — promtail config
- `internal/bundler/assets/firewall.sh` — iptables + agent identity emission
- `internal/firewall/manager.go` — firewall manager (emitAgentMapping)

## TODO Sequence
- [x] Diagnose root cause of "No Data" on egress panels
- [x] Add `allValue: ".*"` back to `agent_ips` variable
- [x] Add `project` filter to `agent_ips` variable query
- [x] Confirm agent_map push mechanism works
- [x] Confirm agent_map entries exist after fresh restart
- [ ] **URGENT: Investigate dashboard breakage** — `git diff HEAD~1 -- internal/monitor/templates/grafana-dashboard.json` to see all changes. Check for JSON corruption or unintended edits.
- [ ] **Fix session_id variable** — shows UUID4 values. Check git history (`git show HEAD~2:internal/monitor/templates/grafana-dashboard.json | python3 -m json.tool | grep -A20 session`) to see original state.
- [ ] Verify egress panels show data with "All" selected
- [ ] Verify agent filtering works per-agent
- [ ] Address agent name display per-line in log panels (lower priority)
- [ ] Run full test suite and commit

## Lessons Learned
- **All containers are on clawker-net** — do not assume network isolation between agent and monitoring containers.
- **`allValue` only applies to the "All" selection** — removing it breaks "show all" without fixing individual filtering.
- **Agent map pushes are fire-once during firewall enable** — if monitoring isn't up yet, they fail silently.
- **Do NOT jump to conclusions** — verify before claiming root causes. Test from inside the container.
- **The binary must be built on macOS (host)** — dashboard template is embedded at compile time.
- **`session_id` in Prometheus is a UUID from OTEL** — it's what Claude Code telemetry emits per-session.

---
**IMPERATIVE**: Always check with the user before proceeding with the next TODO item. The dashboard may be in a broken state — the FIRST priority is to investigate and fix the "broke the whole dashboard" regression before anything else. If all work is done, ask the user if they want to delete this memory.