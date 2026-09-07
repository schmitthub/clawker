# Monitoring

- `internal/monitor/` owns Compose/collector/Prometheus generation and OpenSearch bootstrap assets.
- Prometheus stores metrics. OpenSearch stores logs/traces. OpenSearch Dashboards supplies their UI. The old Grafana/Loki notes are historical.
- Agent-specific telemetry comes from monitoring extensions. Keep agent-specific configuration out of generic infrastructure templates.
- `monitor.extensions` selects extensions through the bundle resolver. Selection enables an extension; there is no separate per-extension active flag.
- `monitor up` merges selected extensions into the host ledger and renders routes from the full seeded set.
- Keep other projects' seeded routes when one project changes its selection. Validate shared names and cluster-object claims before saving.
- `monitor reload` applies selection changes to a running stack. Removing volumes with `monitor down --volumes` removes the ledger.
- The collector endpoint is a base URL from config. Do not concatenate per-signal endpoints in bundler code.
- Trusted CP/Envoy/CoreDNS telemetry and agent telemetry have separate paths. Certificate provisioning packages under `controlplane/` have separate roles.
- Live ingest, routing, and certificate access need runtime proof. Template unit tests do not establish those results.
- Package contract: `internal/monitor/AGENTS.md`. Runtime checks: `.claude/rules/monitoring.md`. Detailed map: `.claude/docs/MONITORING-REFERENCE.md`.
- For component resolution: `mem:bundle/core`.
- For eBPF egress events and SDS identity separation: `mem:controlplane/firewall/core`.
