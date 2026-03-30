# Egress Traffic Monitoring — Resume State

## Branch: `feat/firewall-monitoring`
## Plan file: `/home/claude/.claude/plans/wobbly-popping-swing.md`

## End Goal
Wire Envoy + CoreDNS egress logs into the existing Grafana monitoring dashboard as a new "Egress Traffic" row, with per-agent filtering via the existing global `$agent` dashboard variable.

## Architecture
```
Envoy (JSON stdout) ──┐
                       ├──> Promtail (Docker SD, label filter) ──> Loki ──> Grafana
CoreDNS (logfmt stdout) ─┘

Agent mapping: firewall.Enable() emits {"source":"agent_map","agent":"dev","client_ip":"172.28.0.5"} 
to Envoy stdout → Promtail picks up → hidden $agent_ips variable chains from $agent → all egress panels filter by client_ip
```

## What Was Fixed This Session

### emitAgentMapping broken — docker exec stdout != container stdout
- `docker exec echo ...` writes to the exec session's stdout, NOT PID 1's stdout
- Promtail only captures PID 1's logs → zero agent_map entries in Loki
- Fix: `sh -c 'echo ... > /proc/1/fd/1'` writes to container main process stdout

### Envoy panel empty fields (bytes, duration, upstream_host)
- Promtail correctly does NOT promote high-cardinality fields as labels
- But `line_format` can only access labels — so body fields were empty
- Fix: added `| json` to Envoy panel query to parse body at query time

### DNS panel showing [INFO] prefix / missing duration
- CoreDNS `{remote}` outputs `IP:port` — Promtail regex now strips port: `(?P<client_ip>[^:\s]+)(?:\d+)?`
- `duration` promoted as Promtail label so `line_format` can use it
- Removed redundant `rcode` from line_format (BLOCKED/✓ prefix already conveys status)

### Agent name attribution
- All egress panel titles now show `${agent}` variable
- When user selects specific agent, `$agent_ips` chains from agent_map to filter by client_ip
- Per-line attribution blocked by Envoy seeing gateway IP (separate investigation)

## What's Done

### [DONE] Phase 1: Envoy Access Logging
- `internal/firewall/envoy.go`: `buildHTTPAccessLog(proto)` for HTTP contexts (method/path/response_code), `buildTCPAccessLog(proto)` for TCP contexts (omits HTTP fields), shared `accessLogEntry()` base
- Wired into: `buildHTTPListener` (http), `buildMITMFilterChain` (tls_mitm), `buildPassthroughFilterChain` (tls), `buildTCPListener` (tcp)
- Deny chain: NO access logging (was causing noise from health probes)
- Fields: timestamp, domain (SNI), upstream_host, listener_port, client_ip, response_flags, bytes_sent, bytes_received, duration_ms, proto, source
- Tests: `TestBuildHTTPAccessLog`, `TestBuildTCPAccessLog`, `TestGenerateEnvoyConfig_AccessLogPresent`

### [DONE] Phase 2: CoreDNS Query Logging
- `internal/firewall/coredns.go`: `corefileLogFormat` with `client_ip={remote}` added
- `log` plugin in all zones (per-domain, internal, catch-all)
- Golden file updated
- Format: `source=coredns client_ip={remote} domain={name} qtype={type} rcode={rcode} duration={duration}`

### [DONE] Phase 3: Promtail
- `PromtailImage` constant: `grafana/promtail:3.6.0@sha256:2aafa34b3d5fba888c51081d3a22c234906ffd3cafc5def11c581549b297d449` (multi-arch manifest digest)
- `promtail-config.yaml.tmpl`: Docker SD with `dev.clawker.purpose=firewall`, match stages (json for envoy, regex for coredns since CoreDNS prepends `[INFO]` prefix), labels promoted: domain, proto, client_ip, response_code, response_flags, rcode, qtype, agent, project, action
- Compose service added, wired into `monitor init`

### [DONE] Phase 4: Agent IP Mapping
- `internal/firewall/manager.go`: `emitAgentMapping()` method emits JSON to Envoy container stdout via docker exec
- Called from `Enable()` (action=enable) and `Disable()` (action=disable)
- Extracts agent name from `LabelAgent()` label, IP from `NetworkSettings.Networks[clawker-net]`

### [DONE] Phase 5: Grafana Dashboard
- Row "Egress Traffic" (id 54, y=67)
- Envoy Traffic logs (id 55) — filters by `client_ip=~\`^(${agent_ips:regex})$\``
- DNS Lookups logs (id 56) — same filter, NXDOMAIN shows 🚫 BLOCKED prefix
- Top Blocked Domains table (id 57) — NXDOMAIN count by domain, filtered
- Egress Over Time timeseries (id 58) — by proto, filtered
- Hidden `$agent_ips` variable chains from `$agent` via agent_map Loki entries
- 37 total panels, no duplicate IDs

### [DONE] Phase 6: Documentation
- `internal/monitor/CLAUDE.md`, `internal/firewall/CLAUDE.md`, `.claude/rules/monitoring.md`, `docs/monitoring.mdx` all updated

### [DONE] Phase 7: Tests
- All 4147 tests pass, clean build verified
- Live data confirmed flowing: Envoy JSON, CoreDNS logfmt, Promtail parsing, Loki ingestion with correct labels

## Remaining TODOs

- [ ] **1. Rebuild + redeploy**: `make clawker && clawker monitor init --force && clawker monitor down && clawker monitor up && clawker firewall down && clawker firewall up`. Then start an agent to generate traffic and verify:
  - agent_map entries appear in Loki: `{service_name="envoy", source="agent_map"}`
  - Envoy Traffic panel shows filled fields (bytes, duration, domain, upstream_host)
  - DNS Lookups panel shows clean formatted lines (no `[INFO]`, shows duration)
  - `$agent` dropdown filters egress panels correctly

- [ ] **2. Investigate Envoy client_ip = gateway**: Envoy sees `172.18.0.1` (Docker gateway) instead of agent container IPs. CoreDNS sees real IPs (`172.18.0.8`). The iptables DNAT in firewall.sh should preserve source IP. Need live debugging: `docker exec agent iptables -t nat -L -v -n` + `docker exec clawker-envoy env` to check network topology. This blocks per-line agent attribution in the Envoy panel. CoreDNS panel can already attribute per-agent once agent_map works.

- [ ] **3. Final `make test`** after any remaining fixes.

- [ ] **4. Update serena memory and clean up** once verified.

## Lessons Learned
- Envoy `%RESPONSE_CODE%`, `%REQ(:METHOD)%`, `%REQ(:PATH)%` are HTTP-only — return empty/0 in tcp_proxy. Must split into HTTP vs TCP access log builders.
- Deny chain access logging is pure noise (health probes, Docker internal traffic). Remove it; CoreDNS NXDOMAIN covers blocked domains.
- CoreDNS `log` plugin prepends `[INFO]` prefix — use regex in Promtail, not logfmt.
- Promtail image: use manifest LIST digest (multi-arch) not per-platform. `docker buildx imagetools inspect` gives correct one.
- Dashboard global variables ($project, $agent) must be respected by ALL panels including egress. Chain via hidden $agent_ips variable.
- User expects full integration, not half-features. Per-agent attribution is not optional — it's the whole point of tying egress into the existing dashboard.

## IMPERATIVE
Always check with the user before proceeding with the next todo item. If all work is done, ask the user if they want to delete this memory.
