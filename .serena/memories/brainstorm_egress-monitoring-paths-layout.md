# Brainstorm: Egress Monitoring — Paths in Envoy Output + Log Pane Layout

> **Status:** Prototyped
> **Created:** 2026-03-31
> **Last Updated:** 2026-03-31 00:01

## Problem / Topic
Two improvements to the Grafana egress monitoring dashboard: (1) Add egress paths (URL paths) to the Envoy monitoring output when available (HTTPS MITM and HTTP connections), making path-level traffic visible in the dashboard. (2) Change the Envoy and CoreDNS log panes from a 50/50 side-by-side layout to full-width stacked rows, since both panels have many columns that benefit from horizontal space.

## Open Items / Questions
- (none)

## Decisions Made
- **Fuse domain+path**: Show `domain/path` as a single destination string when path is available. No separate column.
- **Cosmetic proto rename in line_format only (option B)**: `tls`/`tls_mitm` → `https`, `deny` → `DENIED`, `http`/`tcp`/`ssh` unchanged. Raw Envoy `proto` label unchanged — no breaking change for Loki queries.
- **Collapse tls and tls_mitm to `https`**: MITM is an implementation detail, not user-facing. Path presence already implies inspection.
- **Upstream host always visible**: Show `[IP:port]` as a fixed-position field in every log line. Security signal — DNS poisoning detection. Only omit when upstream_host IS the primary destination (no domain available, avoids redundancy).
- **Envoy + CoreDNS log panes → full-width stacked rows**: Each gets w=24 on its own row instead of 50/50 side-by-side.
- **Bottom row stays 50/50**: Top Blocked Domains (table) + Egress Over Time (timeseries) keep their current layout — no column pressure on those panel types.

## Conclusions / Insights
- Dashboard is a security observability tool first — every field that helps detect anomalies earns its place
- Full-width log rows eliminate the horizontal space constraint that would've forced trade-offs between fields
- Bottom row (table + timeseries) is fine at 50/50 — those panel types don't have column pressure

## Gotchas / Risks
- Proto rename is cosmetic only (line_format) — raw `proto` label values in Envoy access logs stay unchanged to avoid breaking existing Loki queries

## Unknowns
- (none)

## Format Examples

With path (MITM/HTTP):
```
https → api.anthropic.com/v1/messages POST 200 [10.0.0.5:443] ↑1234 ↓5678 45ms
```

Passthrough TLS (no path):
```
https → github.com [140.82.121.4:443] ↑892 ↓4521 32ms
```

No domain (IP-only fallback — no brackets, IP is the destination):
```
tcp → 10.0.0.5:22 ↑1234 ↓5678 45ms
```

Denied:
```
DENIED → suspicious.com [—] ↑0 ↓0 0ms
```

## Next Steps
- Prototype: update line_format in grafana-dashboard.json
- Prototype: update gridPos for Envoy/CoreDNS log panels (w=12 → w=24, adjust y offsets)
