# Controlplane OTel Gateway Package

The CP's OTLP gateway for agent telemetry. The agent SDK exports to a loopback relay inside its container; clawkerd carries each batch to `AgentService.ExportTelemetry`; `controlplane/agent.Handler.ExportTelemetry` resolves and verifies the peer; this package decodes the body by signal, stamps the CP-resolved identity onto every resource, and forwards the batch to the collector's `otlp/agents` receiver over the infra mTLS lane.

The package holds no identity logic and does not import `controlplane/agent`: `Identity` is plain values the caller already verified. `agent` imports `otelgateway` for the contract (`Identity`, `ErrMalformedBody`), never the reverse.

## Files

| File | Purpose |
|------|---------|
| `gateway.go` | `Gateway` (`New`, `Forward`, `Close`), `Options`, `Identity`, `Metrics`, the breaker, the attribute stamp |
| `gateway_test.go` | Black-box tests against an in-process mTLS fake collector serving the three OTLP collector services |

## Contract

- `Forward(ctx, signal, body, id)`: `ErrUnknownSignal` and `ErrMalformedBody` are client faults (handler → `InvalidArgument`); `ErrBatchTooLarge` (handler → `ResourceExhausted`) and `ErrBusy` (handler → `Unavailable`) are the caps below. Any other error is a forward failure while the breaker is closed (handler → `Unavailable`, relay → retryable status). With the breaker open the batch is dropped and `nil` is returned.
- Caps (the wire cap bounds bytes, not objects): every nested message is counted on the wire before decode against the request schema (`Options.MaxMessages`, default 100,000; nesting past `protowire.DefaultRecursionLimit` is `ErrMalformedBody`); the stamped batch must fit the collector's 8 MiB receiver cap; at most `Options.MaxConcurrent` (default 8) batches decode at once, the rest get `ErrBusy` at once. The agent listener also caps streams per connection (`controlplane/server`).
- Stamp: for every `Resource` in the batch (a nil resource is created), delete client copies of `project`, `agent`, `container_id`, `ingest_source` and every `clawker.*` key; append the four with the CP values and `ingest_source=cp_gateway`. A global-scope agent stamps `project=""` so a client value cannot fill the gap. Other attributes and the payload are untouched.
- Forward: typed OTLP collector service clients on one `grpc.ClientConn` per CP process, dialed lazily with the `cp` infra leaf (`otelcerts.Service.LoadTLSConfig("cp")`). No SDK provider in the path: re-batching or re-resourcing would undo the stamp. Each forward is bounded by `Options.ForwardTimeout` (default 10s).
- Breaker (monitoring is optional): `Options.TripThreshold` (default 3) consecutive forward failures open it; open = drop with success + one probe per `Options.ProbeInterval` (default 1m, must exceed `ForwardTimeout`); a probe success closes it. Only `Unavailable` and `DeadlineExceeded` with a live caller context count: a collector verdict on one batch (`InvalidArgument`, `ResourceExhausted`) or a caller that left surfaces as an error and leaves the breaker alone, so one agent cannot trip it for everyone. Events: `otel_gateway_tripped` (warn, once per trip), `otel_gateway_recovered` (info), `otel_gateway_forward_failed` (debug, closed-state failures), `otel_gateway_partial_success` (warn).
- `Metrics` counters (`clawker_otel_gateway_{exported,forward_failures,malformed,dropped,rejected}_total` by `signal`) are created unregistered like the netlogger counters: the CP exposes no scrape endpoint.

## Failure rules

CP safety applies (`controlplane/AGENTS.md`): `New` returns errors and never dials eagerly, so a monitoring stack that is not running never delays CP boot; `Forward` never panics and never blocks past the forward timeout. The orchestrator (`internal/controlplane/cmd.go` `buildOtelGateway`) degrades to a nil gateway with `event=otel_gateway_unavailable`.

## Imports

**Uses**: `api/agent/v1` (`Signal`), `internal/logger`, `go.opentelemetry.io/proto/otlp/{collector/{logs,metrics,trace},common,resource}/v1`, `google.golang.org/grpc`, `google.golang.org/protobuf/proto`, `github.com/prometheus/client_golang/prometheus`.

**Used by**: `controlplane/agent` (contract types), `internal/controlplane` (construction).
