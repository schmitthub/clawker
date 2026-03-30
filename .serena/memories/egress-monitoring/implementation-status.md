# Egress Traffic Monitoring — Implementation Status

## Branch: `feat/firewall-monitoring`

## Status: Code complete, all tests passing (4147/4147), needs end-to-end verification after rebuild+redeploy

## What Was Done

### Phase 1: Envoy Access Logging (`internal/firewall/envoy.go`)
- Added `buildAccessLog(proto string) []any` helper — generates Envoy stdout JSON access log config
- Wired into all 5 listener builders: `buildHTTPListener` (proto="http"), `buildMITMFilterChain` (proto="tls_mitm"), `buildPassthroughFilterChain` (proto="tls"), `buildDenyFilterChain` (proto="deny"), `buildTCPListener` (proto="tcp")
- JSON fields: timestamp, domain (SNI), upstream_host, method, path, response_code, response_flags, bytes_sent, bytes_received, duration_ms, proto, source
- Tests: `TestBuildAccessLog`, `TestGenerateEnvoyConfig_AccessLogPresent` (4 subtests covering all listener types)

### Phase 2: CoreDNS Query Logging (`internal/firewall/coredns.go`)
- Added `corefileLogFormat` constant with logfmt-compatible format: `source=coredns domain={name} qtype={type} rcode={rcode} duration={duration}`
- Added `log . "FORMAT"` directive to all 3 zone types: per-domain forward zones, internal host zones, catch-all zone
- Updated golden file: `internal/firewall/testdata/corefile_basic.golden`

### Phase 3: Promtail Log Collection (`internal/monitor/`)
- Added `PromtailImage` constant: `grafana/promtail:3.6.0@sha256:2aafa34b3d5fba888c51081d3a22c234906ffd3cafc5def11c581549b297d449`
- Added `PromtailImage string` to `MonitorTemplateData` struct, wired in `NewMonitorTemplateData()`
- Added `PromtailConfigTemplate` (embed) + `PromtailConfigFileName` constant
- Created `internal/monitor/templates/promtail-config.yaml.tmpl`:
  - Docker SD with `dev.clawker.purpose=firewall` label filter
  - Relabel: `clawker-envoy` → `service_name=envoy`, `clawker-coredns` → `service_name=coredns`
  - `match` stages: Envoy uses `json` parser, CoreDNS uses `logfmt` parser
  - Key labels promoted: domain, proto, response_code, response_flags, rcode, qtype
- Added `promtail` service to `compose.yaml.tmpl` (Docker socket mount, clawker-net network)
- Added to `monitor init` files list in `internal/cmd/monitor/init/init.go`
- Tests: `TestRenderTemplate_PromtailConfig`, `TestNewMonitorTemplateData_PromtailImage`

### Phase 4: Grafana Dashboard (`internal/monitor/templates/grafana-dashboard.json`)
- Added "Egress Traffic" row (id: 54, y: 67)
- Envoy Traffic logs panel (id: 55, y: 68, w: 12) — `{service_name="envoy"}` with line_format
- DNS Lookups logs panel (id: 56, y: 68, x: 12, w: 12) — `{service_name="coredns"}` with line_format
- Top Blocked Domains table (id: 57, y: 78, w: 12) — `topk(20, sum by (domain) (...))` with labelsToFields transforms
- Egress Over Time timeseries (id: 58, y: 78, x: 12, w: 12) — `sum by (proto) (count_over_time(...))`
- All panel IDs verified unique (37 total panels, no duplicates)

### Phase 5: Documentation
- `internal/monitor/CLAUDE.md` — added Promtail template, filename, image entries
- `internal/firewall/CLAUDE.md` — documented access logging for Envoy and CoreDNS
- `.claude/rules/monitoring.md` — added Egress Traffic Logs section with data model
- `docs/monitoring.mdx` — added Egress Traffic Visibility section, Promtail service to tables

## Files Modified
- `internal/firewall/envoy.go` — buildAccessLog + wired into 5 builders
- `internal/firewall/envoy_test.go` — 2 new tests
- `internal/firewall/coredns.go` — log plugin in all zones
- `internal/firewall/testdata/corefile_basic.golden` — updated
- `internal/monitor/templates.go` — PromtailImage, PromtailConfigTemplate, PromtailConfigFileName, MonitorTemplateData
- `internal/monitor/templates/promtail-config.yaml.tmpl` — NEW
- `internal/monitor/templates/compose.yaml.tmpl` — promtail service
- `internal/monitor/templates/grafana-dashboard.json` — 5 new panels
- `internal/monitor/templates_test.go` — 2 new tests
- `internal/cmd/monitor/init/init.go` — promtail in files list
- `internal/monitor/CLAUDE.md` — updated
- `internal/firewall/CLAUDE.md` — updated
- `.claude/rules/monitoring.md` — updated
- `docs/monitoring.mdx` — updated

## End-to-End Testing Guide

### Quick verification (no Docker):
```bash
make test  # 4146 pass, 0 fail
go build -o /dev/null ./cmd/clawker  # clean build
```

### Full E2E:
```bash
# 1. Build
make clawker

# 2. Regenerate monitoring configs
clawker monitor init --force

# 3. Restart monitoring stack
clawker monitor down
clawker monitor up

# 4. Restart firewall (picks up new Envoy/CoreDNS configs with logging)
clawker firewall down
# Firewall auto-starts on next container run, OR:
clawker firewall up

# 5. Run an agent to generate traffic
clawker run -it --agent dev @

# 6. Verify each hop:
docker logs clawker-envoy --tail 10     # JSON access logs?
docker logs clawker-coredns --tail 10   # logfmt query logs?
docker logs promtail --tail 20          # pipeline errors?

# 7. Query Loki directly:
curl -s 'http://localhost:3100/loki/api/v1/query_range' \
  --data-urlencode 'query={service_name="envoy"}' \
  --data-urlencode 'limit=5'

curl -s 'http://localhost:3100/loki/api/v1/query_range' \
  --data-urlencode 'query={service_name="coredns"}' \
  --data-urlencode 'limit=5'

# 8. Open Grafana: http://localhost:3000
# Scroll to "Egress Traffic" row at bottom
```

### Potential issues:
- Firewall containers need restart to pick up new configs (they bind-mount envoy.yaml/Corefile)
- Promtail needs Docker socket access — check `docker logs promtail` if no data appears
- CoreDNS catch-all zone may be noisy from health check queries — acceptable for dev use
