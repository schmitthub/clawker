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
- Baked index patterns carry no `fields`. OpenSearch Dashboards 3.9 (data_views) reads an index pattern's field list from the saved object only and does not fetch it from the index at load time (3.6 did). Bootstrap writes each index pattern's field list from the live mapping after the saved-object import; without it every visualization that names a field fails with "Could not locate that index-pattern-field". Keep this step when the Dashboards image changes.
- A kept OpenSearch volume carries its ISM state: the 7-day retention deletes aged indices under a running stack. Monitoring UAT after a template change starts from `monitor down --volumes`.
- Package contract: `internal/monitor/AGENTS.md`. Runtime checks: `internal/monitor/AGENTS.md` → Runtime UAT. Detailed map and verification workflow: `.agents/skills/monitoring-checks/SKILL.md`.
- For component resolution: `mem:bundle/core`.
- For eBPF egress events and SDS identity separation: `mem:controlplane/firewall/core`.
