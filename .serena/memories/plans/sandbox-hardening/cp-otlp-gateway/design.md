# Item 2: CP as OTLP ingest gateway (ruled 2026-10-24, option A2)

Part of `mem:plans/sandbox-hardening/initiative` (Tasks 4 and 5). Collector placement: `mem:plans/sandbox-hardening/monitoring-off-clawker-net/design`.

## Problem

The unprivileged agent process exports OTLP (`http/protobuf`) straight to the collector with client-chosen resource attributes. It holds no identity material: only clawkerd (root) has the CLI-minted leaf, and only for the one-time Register. Provenance of agent telemetry is therefore unverified.

## Ruling

- Agents get exactly one telemetry path: clawkerd loopback receiver → `AgentService.Export` over clawkerd's existing mTLS leaf → CP stamps identity → CP forwards to a dedicated collector receiver with its infra leaf.
- Rejected: relay over the CP→clawkerd Session stream (telemetry would share the containment command channel); a second CLI-minted leaf handed to the unprivileged user (new CN pin, new listener, 24h cert with no renewal, key readable by every container process); bearer token; peer-IP-only.

## Facts that fix the shape

- CLI mints the agent leaf (`internal/auth/agent_cert.go`). CP has no CLI root key. CP cannot issue agent-chain certs.
- clawkerd opens a gRPC client per Register and closes it (`clawkerd/register.go` `realDialAndRegister`). The Hydra assertion is single-use; no second bearer is possible. Register stays bearer-gated; the new RPC is `ScopePublic` (mTLS + identity gate only). `controlplane/auth/authz.go` already returns nil for `ScopePublic` before the bearer check.
- `IdentityInterceptor` runs on every AgentService RPC: CN pin `consts.ContainerClawkerd`, peer IP → `purpose=agent` container labels, SAN compare. It attaches `ResolvedContainer` to ctx. New handlers read it and add the registry thumbprint check (`Registry` lookup by container id, compare `sha256(leaf.Raw)`), the same check the dialer performs at Hello.
- Node OTLP HTTP exporters have no unix-socket support; the in-container transport is loopback TCP. eBPF allows loopback before any subnet rule.

## Wire (`api/agent/v1/agent.proto`)

```proto
rpc Export(ExportRequest) returns (ExportResult);

enum Signal { SIGNAL_UNSPECIFIED = 0; SIGNAL_LOGS = 1; SIGNAL_METRICS = 2; SIGNAL_TRACES = 3; }
message ExportRequest { Signal signal = 1; bytes body = 2; }  // body = serialized Export<Signal>ServiceRequest, http/protobuf payload as received
message ExportResult {}
```

- Unary per batch. Client streaming adds flow-control code the batch processor already provides. Reconsider only on measured overhead.
- `api/agent/v1/agent.go` `AgentMethodScopes` adds `"/.../Export": consts.ScopePublic` (type the sentinel into `AgentScope` the same way the admin side does).
- Body size cap: `grpc.MaxRecvMsgSize` on the agent listener set to a const (4 MiB matches the SDK default batch ceiling). Oversize → `ResourceExhausted`.

## CP handler

- Location: `controlplane/agent/export_handler.go` if the agent package may import `go.opentelemetry.io/proto/otlp/collector/{logs,metrics,trace}/v1`; otherwise a sibling `controlplane/ingest/` package that consumes `agent.ResolvedContainerFromContext` and `agent.Registry`. Decide by the package DAG rule in `CLAUDE.md`; do not widen `controlplane/agent` into a telemetry package if a sibling is cleaner.
- Steps: `ResolvedContainerFromContext` → registry entry for `ContainerID` → compare peer thumbprint (`peerLeafFromContext`, `sha256(Raw)`) → mismatch or missing row = `PermissionDenied` with the uniform "registration rejected" envelope and a distinct `event=` log field. Unmarshal body by signal. For every `ResourceSpans|Logs|Metrics.resource.attributes`: delete any client `project`, `agent`, `container_id`, `ingest_source`, `clawker.*`; append `project`, `agent` (from labels), `container_id`, `ingest_source=cp_gateway`. Re-marshal and send through the forwarder.
- Forwarder: one `grpc.ClientConn` per CP process to the collector agent receiver, mTLS via `otelcerts.Service.LoadTLSConfig` (the `cp-otel-client` infra leaf), endpoint `host.docker.internal:<OtelAgentIngestPort>`. Use the OTLP collector service clients directly (`logsv1.LogsServiceClient.Export` etc.); do not route through the SDK logger provider, which would re-batch and re-resource.
- Failure rules: constructor returns error, `internal/controlplane/cmd.go` degrades with `event=agent_ingest_unavailable` and serves `Export` returning `Unavailable`. Forward errors return `Unavailable` to clawkerd; never block, never panic. Recover in the handler like other RPC handlers.
- Metrics: counters for exported batches, rejected (by reason), forward failures, via the existing CP Prometheus registry.

## Collector receiver (`internal/monitor/templates/otel-config.yaml.tmpl`)

- New receiver `otlp/agents` on a new port setting `monitoring.otel_agent_ingest_port` (default 4320), gRPC, TLS with `client_ca_file` = infra intermediate (same file as `otlp/infra`). Published `127.0.0.1:<port>` in `compose.yaml.tmpl`.
- New processor `resource/agent_gateway`: `upsert ingest_source=cp_gateway`. CP sets it too; the processor makes the collector authoritative.
- Pipelines: `logs/agents`, `metrics/agents`, `traces/agents` with `[memory_limiter, resource/agent_gateway, (transform/metrics, deltatocumulative for metrics), batch]` → the existing `claude-code` exporters and the Prometheus exporter. The untrusted `otlp` 4317/4318 receivers and their `resource/untrusted_otlp` pipelines are removed in Task 6 once nothing on clawker-net exists to use them; until then they stay.
- Dashboards: `ingest_source` filter values gain `cp_gateway`; check saved searches that pin `untrusted_otlp`.

## clawkerd relay (`clawkerd/otlp_relay.go`)

- `net.Listen("tcp", "127.0.0.1:<consts.AgentOTLPRelayPort>")` (pick an unprivileged port const, e.g. 4318 on loopback so existing SDK defaults work when the endpoint env is unset). HTTP server: accept `POST /v1/logs`, `/v1/metrics`, `/v1/traces` with `Content-Type: application/x-protobuf`; `415` otherwise; `404` other paths; body limit = the same 4 MiB const; `gzip` `Content-Encoding` decoded (Node SDK sends gzip by default).
- Forward: `AgentServiceClient.Export` on a `grpc.ClientConn` dialed like Register (same TLS config, `consts.EnvClawkerdAgentAddr`), kept open for the process lifetime with gRPC's own reconnect. No bearer credentials on this conn.
- Response mapping: success → `200` empty `Export<Signal>ServiceResponse`; `Unavailable`/dial error → `503` with `Retry-After` so the SDK retries; `PermissionDenied` → `403` and a rate-limited `event=otlp_relay_rejected` log. Never block spawn, Session, or shutdown on the relay; `Shutdown` with a short timeout at exit.
- Start order: after `RegisterDone` (identity row exists); until then the listener is up and returns `503` so early SDK batches retry.
- Env: `internal/bundler/dockerfile.go` `OtelEndpoint` becomes the loopback URL const; `OtelCollectorURL` callers for the agent endpoint go away. `OTEL_RESOURCE_ATTRIBUTES` may still carry `project=`/`agent=` for local SDK use; CP overwrites them.
- Tests: `httptest` against the relay with a fake `AgentServiceClient`; content-type, path, size, gzip, and error-mapping cases; loopback-only bind assertion.

## Out of scope

- Any auth or scoping for the host proxy.
- Telemetry from the CLI (host loopback path unchanged).
- Collector-side authentication of agents (agents never reach the collector after Task 6).
