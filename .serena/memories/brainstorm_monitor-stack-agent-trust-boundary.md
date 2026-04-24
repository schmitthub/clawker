# Brainstorm: Monitor Stack vs Coerced Agents

> **Status:** Stub — discovered during `brainstorm_cp-informer` (2026-04-16). Not yet worked.
> **Related:** `brainstorm_cp-informer`, `clawker-pantheon` (threat model: agent-as-coercible-actor)

## The problem

Clawker's threat model is inverted: the host user is trusted, the
agents inside managed containers are **not** — they are benign-intent
but coercible via prompt injection from any untrusted content they
read (web, PRs, files, docs). A coerced agent becomes a hostile
insider on clawker-net.

The monitor stack as currently shipped (`internal/monitor/templates/compose.yaml.tmpl`)
puts **every observability component on clawker-net with no
authentication**:

| Component | Exposure | Reachable by coerced agent? |
|-----------|----------|-----------------------------|
| Prometheus | `prometheus:PORT` on clawker-net, unauth | **Yes — full metrics disclosure including every agent's names, churn, topology hints** |
| Loki | `loki:PORT` on clawker-net, unauth | **Yes — read every other agent's logs, CP logs, firewall access logs** |
| Grafana | `grafana:PORT` on clawker-net, `GF_AUTH_ANONYMOUS_ORG_ROLE=Admin` | **Yes — anonymous Admin. Query all datasources.** |
| OTEL collector | `otel-collector:4317/4318` on clawker-net, unauth OTLP | **Yes — push poisoned telemetry into pipeline** |
| Jaeger | `jaeger:PORT` on clawker-net | **Yes — trace read access** |
| Promtail | Reads docker.sock directly (host-privileged) | N/A — not agent-reachable, but tails agent logs and ships them to Loki |

A single coerced agent can:
- Learn every other agent's name, project context, activity pattern
  (recon for lateral pivot)
- Read firewall access logs (learn blocked/allowed destinations,
  egress patterns)
- Read CP logs (understand internal state machine, find exploitable
  conditions)
- Push fake metrics/logs into the collector (mask malicious activity
  behind benign-looking telemetry, evade detection)
- Query Grafana to fingerprint the whole stack

## Why this exists

The monitor stack was designed for the "trusted observer" model — a
single-tenant local dev environment where observability consumers are
developers. clawker-net was treated as an internal trust zone. The
inverted threat model (coercible agents ON clawker-net) wasn't
primary at stack design time.

## Directions to explore (unbiased)

### Option A: separate monitor-net

Put Prometheus/Loki/Grafana/OTEL-collector on a second Docker network
(`clawker-monitor-net`). CP + agents stay on clawker-net. CP
dual-homes to reach monitor-net. Agents cannot join monitor-net
(CP owns network memberships — security property).

Pros: clean structural fix, no per-service auth ceremony, consistent
with CP's existing per-purpose network strategy.

Cons: need to multi-home CP, possibly promtail. Change to `compose.yaml.tmpl`.
Docker-level auth on network membership (who can `docker network
connect`) assumes user isn't a threat — which matches our model.

### Option A+: separate monitor-net + Oathkeeper/Kratos reverse proxy (RECOMMENDED once final-phase webui lands)

CP's final phase adds a user webui backed by Kratos (identity) +
Oathkeeper (reverse proxy auth gateway). Ory stack already running
in CP (Branch 1+). Anonymous Grafana admin was always a local-dev
stopgap.

Shape:
- Monitor stack (Prometheus, Loki, Grafana, OTEL collector) moves to
  a new `clawker-monitor-net`. Agents never join this network.
- CP multi-homes: clawker-net + clawker-monitor-net. OTEL collector
  dual-receives (see push-side note below).
- User reaches Grafana / logs / metrics **only through CP's
  published port** → Oathkeeper reverse proxy → Kratos-validated
  session → Grafana/Loki/Prometheus APIs.
- Coerced agent on clawker-net cannot reach `clawker-monitor-net`,
  cannot reach Oathkeeper from within (different listener or
  different ACL), cannot present a Kratos session.
- Grafana runs with `GF_AUTH_PROXY_ENABLED=true` + Oathkeeper
  injects user header — users don't log in to Grafana separately.

Pros: single sign-on via existing Ory stack; no per-component cred
provisioning; kills anonymous Grafana admin at the root cause;
consistent with webui phase architecture.

Cons: requires webui phase decisions made now (or a lightweight
Oathkeeper rule set pre-dating full webui); network multi-homing
on CP; cert material for the new network.

### Option B: authenticate read paths only

- Prometheus: HTTP basic auth + web.config.file
- Loki: HTTP basic or multi-tenant mTLS
- Grafana: kill anonymous admin, require login
- Provision creds to CLI (reads via Grafana) only

**Cannot auth the write path (OTLP receiver).** Claude Code in every
agent container pushes telemetry unauth'd to the collector — gating
OTLP breaks the pipeline by design. Auth therefore applies to read
paths (Prometheus/Loki/Grafana) only. Write-path poisoning by a
coerced agent remains a residual risk that auth alone cannot fix;
needs push-side filtering or provenance verification at the collector.

Pros: defense in depth against disclosure; works even if network
isolation is breached later.

Cons: every read component needs auth config + cred management;
write-side poisoning unfixed by this option alone.

### Option C: both (defense in depth)

Separate monitor-net AND per-component auth. Most secure, most
complexity.

### Option D: gate monitor stack behind CP

CP becomes an observability proxy: Prometheus/Loki/Grafana reachable
only via CP-authenticated mTLS tunnel. CLI dials CP, CP forwards to
stack. Agents have no path in.

Pros: single choke point, reuses CP auth stack.

Cons: major re-architecture; CP becomes bottleneck; invalidates
current `clawker monitor *` UX.

## Decisions made

- None yet. Directions above are candidates.

## Unknowns

- Is multi-homing CP onto both networks the right split, or should
  there be a dedicated metrics-pusher with its own cert material?
- Where does Promtail fit? It reads docker.sock (host-privileged) but
  needs to ship to Loki. Same network as Loki but not reachable by
  agents.
- Grafana usability: anonymous admin was chosen for local-dev UX.
  Login flow complicates `clawker monitor up` as "just works".
- How does this interact with OTEL bridge in `*logger.Logger`? Today
  every clawker process with OTEL config can push to the collector;
  does clawkerd (phase 5+) also push telemetry? If yes, per-agent
  auth to the collector is a new design concern.

## Why Option A+ is the target

The Ory stack (Hydra + Oathkeeper + Kratos) is already running in
CP as of Branch 1. Oathkeeper is a reverse proxy auth gateway;
Kratos is the identity provider. Both are placeholders today — real
rules/identities arrive with the **final-phase webui** (user browses
CP state, logs in via Kratos-backed flow).

The monitor stack read paths fold naturally into the same auth:
- User reaches Grafana/Loki/Prometheus **only through CP's published
  port** → Oathkeeper → Kratos session check → upstream component
- Grafana accepts auth-proxy header (`GF_AUTH_PROXY_ENABLED`) —
  standard pattern, zero Grafana login UI
- Loki / Prometheus fronted by the same Oathkeeper, user header
  injected on every request
- No per-component creds. Single sign-on. Coerced agent on
  clawker-net has no Kratos session, no way in.

This collapses three problems (Grafana anonymous admin, Prometheus
unauth, Loki unauth) into one: webui auth topology. It's going to
happen anyway. Monitor-stack containment just rides the webui phase.

Ordering across phases:

| Phase | Monitor write | Monitor read | State |
|-------|---------------|--------------|-------|
| Today | unauth OTLP, everyone pushes | clawker-net unauth | broken |
| watcher PR | + trusted OTLP receiver (mTLS), CP uses it | unchanged | push poisoning fixed for CP data only |
| monitor-net PR (this brainstorm) | unchanged | stack moved off clawker-net; Oathkeeper rules drafted even if webui incomplete | disclosure fixed |
| Final webui phase | unchanged | user browses through webui → Oathkeeper → dashboards | full containment |

## Urgency

This stack ships today. Every clawker installation with
`clawker monitor up` running has this gap. Not a demo-time risk
(local dev), but any multi-agent setup where one agent is exposed to
untrusted content = exploitable recon/exfil path.

Should land **before** clawkerd agent registration goes active
(Branch 4+), because once agents can actually do work, they become
interesting targets for coercion.

## Next step

Open a real brainstorm session (`/brainstorm`) or direct discussion
to pick an option and spec it. Probably Option A (separate monitor-net)
+ mTLS on OTEL collector for push. Light version of C.
