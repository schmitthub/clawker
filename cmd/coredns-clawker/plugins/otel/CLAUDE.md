# otel CoreDNS Plugin

Emits one structured `dns.query` log record per DNS query handled by CoreDNS, exported over OTLP/gRPC + mTLS to the CP-only collector receiver. Installed as the `otel` directive in every server block of the firewall Corefile (the first directive — runs after every other plugin has produced its final rcode + answer set).

Runtime owner: `internal/controlplane/firewall.Stack` builds `clawker-coredns:latest`, manages its lifecycle, bind-mounts the mTLS material (leaf cert + key + CA) under `/etc/clawker/auth/coredns/`, and sets `CLAWKER_COREDNS_OTEL_ENDPOINT` in the container env. Endpoint host is `consts.MonitoringServiceOtelCollector` (clawker-net hostname) + port `cfg.Settings().Monitoring.OtelInfraPort`.

Purpose: feed the monitoring stack with a per-query log stream that includes client IP, zone, query name, qtype, rcode, answer count + answers, duration, and resolver error. The plaintext stdout `log` directive is kept alongside for `docker logs clawker-coredns` triage only — the collector does not scrape it (no filelog receiver in the OpenSearch pipeline).

## Files

| File | Purpose |
|------|---------|
| `otel.go` | `Handler` (CoreDNS `plugin.Handler` + `Emitter`), `QueryEvent`, `Options`, `otelEmitter` (OTLP/gRPC + mTLS), `noopEmitter`, `NewEmitter`, TLS config builder. |
| `setup.go` | Caddy controller callback; registers the plugin via `plugin.Register(pluginName, setup)`; builds the shared emitter on first call via `ensureSharedEmitter` (mutex-guarded, retries on failure); reads `CLAWKER_COREDNS_OTEL_ENDPOINT`; degrades to `noopEmitter` when unset. |
| `log.go` | CoreDNS-style logger (thin wrapper around `coredns/coredns/plugin/pkg/log`). |
| `otel_test.go` | Unit tests using a `cannedHandler` downstream stub + `recordingEmitter` fake. |

## Key Types

```go
type Emitter interface {
    Emit(ctx context.Context, event QueryEvent) error
}

type Handler struct {
    Next    plugin.Handler
    Zone    string   // Corefile zone (e.g., "github.com." — trailing dot stripped before emit)
    Action  string   // clawker firewall verdict for every query this handler serves
                     // ("allowed" for named zones; "denied" for the catch-all `.` zone
                     // whose templated NXDOMAIN response IS the firewall block).
                     // Derived via ActionForZone() at construction time, not per-query.
    Emitter Emitter  // shared per-process; nil-tolerant via noopEmitter
}

// ActionForZone — exported helper. Tests use it to derive the expected
// Action value for table-driven cases so the test harness mirrors setup()
// production wiring exactly.
func ActionForZone(zone string) string

type QueryEvent struct {
    Timestamp   time.Time
    Duration    time.Duration
    ClientIP    string
    Zone        string
    QueryName   string
    QueryType   string
    RCode       string
    Action      string // copied from Handler.Action at emit time
    Answers     []string
    AnswerCount int
    Err         error
}

type Options struct {
    Endpoint       string   // host:port — gRPC dial target
    CACertFile     string   // PEM, CLI root CA
    ClientCertFile string   // PEM, leaf signed by the infra intermediate CA (minted by otelcerts.Service, dispatched via firewall.Stack.ensureInfraClientCerts)
    ClientKeyFile  string   // PEM, leaf private key
    Timeout        time.Duration
    MaxQueueSize   int
    ExportInterval time.Duration
}

const (
    pluginName            = "otel"
    envEndpoint           = "CLAWKER_COREDNS_OTEL_ENDPOINT"
    defaultClientCertPath = "/etc/clawker/auth/coredns/client.pem"
    defaultClientKeyPath  = "/etc/clawker/auth/coredns/client.key"
    defaultCACertPath     = "/etc/clawker/auth/coredns/ca.pem"
)
```

## ServeDNS flow

```
plugin.NextOrFailure(h.Next) into a nonwriter   ──►  capture downstream response without writing it
QueryEvent  built from request + nonwriter.Msg  ──►  zone, qname, qtype, rcode, answers, duration, err
Emitter.Emit(ctx, event)                        ──►  noopEmitter or otelEmitter (OTLP/gRPC)
w.WriteMsg(nw.Msg)                              ──►  forward the original response upstream
```

- Wraps downstream via `plugin/pkg/nonwriter` so the plugin can observe the message before forwarding. The original `dns.ResponseWriter` is only written to after Emit, so a slow/erroring exporter does not delay the DNS reply.
- `rcode` is sourced from `dns.RcodeToString[rcode]` first; if the downstream produced a message, the message's rcode wins (matches what the client will actually see).
- Emit failures are logged at Warning and never propagated — DNS resolution must not depend on telemetry.
- Resolver errors (downstream `NextOrFailure` returns error) still emit a `QueryEvent` with `Err` set + rcode `SERVFAIL`, then the error is returned to the server.

## Shared Emitter Lifecycle

`sharedEmitter` + `sharedEmitterMu` + `ensureSharedEmitter` implement a process-wide singleton:

- First zone to call `setup` (typically the catch-all `.` zone or the first forward zone, whichever the parser hits first) reads `CLAWKER_COREDNS_OTEL_ENDPOINT` and builds the emitter under the mutex.
- Empty endpoint → `noopEmitter`, with one Warning naming the env var. The plugin still installs in every server block; `Handler.Emit` is a no-op so the directive stays valid Corefile syntax even when telemetry is disabled.
- Non-empty endpoint → `otelEmitter` with OTLP/gRPC exporter + mTLS using the default cert paths above.
- Subsequent zones reuse the same emitter. **No `OnShutdown` close handler** — same reasoning as `internal/dnsbpf`: CoreDNS's `reload` plugin tears down and rebuilds all server blocks without restarting the process, so closing the provider on shutdown would permanently disable telemetry. The plugin deliberately leaks the provider for the lifetime of the process; the batch processor flushes on its own interval.
- **Retry on failure**: emitter construction failure is *not* cached. A reload after a transient failure (e.g. cert read mid-rotation) gets a fresh attempt. Only successful construction latches `sharedEmitter`. This is a deliberate departure from the dnsbpf plugin's `sync.Once` pattern, which would permanently latch the first error.
- If construction fails on a given attempt, `setup` returns `plugin.Error` and CoreDNS refuses to start that reload. The CP firewall stack treats startup failure as a hard error during `EnsureRunning`.

## mTLS Wiring

The plugin is the OTLP **client**. Material is issued + bind-mounted by `firewall.Stack`:

| Path inside container | Source on host |
|-----------------------|----------------|
| `/etc/clawker/auth/coredns/client.pem` | `OtelClientsDir/coredns/client.pem` — leaf signed by the infra intermediate CA via `otelcerts.Service`, minted on `firewall.Stack.EnsureRunning` and rotated on every `Reload` |
| `/etc/clawker/auth/coredns/client.key` | `OtelClientsDir/coredns/client.key` |
| `/etc/clawker/auth/coredns/ca.pem` | `OtelClientsDir/coredns/ca.pem` — copy of CLI root CA written alongside the leaf so coredns can verify the otel-collector server cert |

`buildTLSConfig`:
- Requires all three paths; returns error if any is empty.
- Validates the keypair eagerly at boot via `tls.LoadX509KeyPair`, then wires `tls.Config.GetClientCertificate` to **re-read the leaf from disk on every handshake**. Leaf rotation by `firewall.Stack.ensureInfraClientCerts` picks up automatically when gRPC reconnects — no CoreDNS container restart needed. CA bundle is loaded once via `os.ReadFile` + `pool.AppendCertsFromPEM` (CA rotation still requires a container restart, which `firewall.Reload` performs).
- `MinVersion: tls.VersionTLS12`.
- Server side is the CP-only `otlp/infra` receiver on `OtelInfraPort` (see `internal/controlplane/firewall/CLAUDE.md` → ALSConfig MTLS=true path; CoreDNS uses the same receiver as Envoy ALS for symmetry).

## OTel SDK shape

- Exporter: `otlploggrpc.New` with `WithEndpoint(opts.Endpoint)` + `WithTLSCredentials(credentials.NewTLS(tlsCfg))`. Optional `WithTimeout(opts.Timeout)`.
- Processor: `sdklog.NewBatchProcessor(exporter, opts...)` with optional `WithMaxQueueSize` + `WithExportInterval`.
- Provider: `sdklog.NewLoggerProvider(WithResource(...), WithProcessor(processor))`. Resource attribute `service.name=coredns` (schemaless).
- Each `Emit` builds an `otellog.Record`:
  - `EventName=dns.query`, `Severity=Info`, `SeverityText=INFO`, body `"CoreDNS query handled"`
  - Attributes: `client.address` (OTel-canonical, replaces colloquial `client_ip`), `zone`, `query_name`, `qtype`, `rcode`, `action`, `answer_count` (int), `duration_ms` (float64). No per-record `source` attribute — `service.name=coredns` (resource layer) + `ingest_source=coredns` (stamped post-routing by `resource/coredns`) cover provenance.
  - `action` is the clawker firewall verdict — `allowed` for named zones (forward upstream), `denied` for the catch-all `.` zone (templated NXDOMAIN). Distinct from `rcode`: an allowed-zone query that gets an upstream NXDOMAIN (typo) emits `rcode=NXDOMAIN, action=allowed` — clawker policy allowed, upstream said no record.
  - `answers` (slice of strings) added only when non-empty so empty NXDOMAIN responses don't carry an empty array attribute
  - `event.Err != nil` → `record.SetErr(event.Err)`
- SDK-side errors flow through `otel.SetErrorHandler` → CoreDNS Warning log; they never crash the plugin (consistent with the CP no-panic invariant — see root `CLAUDE.md`).

## Test seam

`Handler.Emitter` is the `Emitter` interface — `otel_test.go` constructs a `Handler` with `recordingEmitter` (captures `QueryEvent`s into a slice, optionally returns an injected `err`) and drives it via `cannedHandler` as `Next`. This exercises:

- Successful resolution path: response forwarded + one event emitted (`TestServeDNS_EmitsEventAndForwardsResponse`).
- Resolver error path: `SERVFAIL` rcode + `Err` populated on the event (`TestServeDNS_EmitsEventOnResolverError`).
- `remoteIP` host:port splitting (`TestRemoteIP`).

No live OTLP collector or real BPF/TLS material needed — the test surface is the `Emitter` interface, not the exporter pipeline.

## Imports

- `github.com/coredns/coredns/plugin` + `plugin/pkg/nonwriter` + `plugin/pkg/log` + `core/dnsserver`
- `github.com/coredns/caddy`
- `github.com/miekg/dns`
- `go.opentelemetry.io/otel` + `otel/attribute` + `otel/log` + `otel/sdk/log` + `otel/sdk/resource` + `otel/exporters/otlp/otlplog/otlploggrpc`
- `google.golang.org/grpc/credentials`

Imported by `cmd/coredns-clawker/main.go` (blank import for `init()` → `plugin.Register`) and nothing else.

## Gotchas

- The plugin must be **first** in the directive chain (set in `cmd/coredns-clawker/main.go` via `dnsserver.Directives = append([]string{"otel", "dnsbpf"}, ...)`). If it runs before `forward`/`template` execute, the event's answers + final rcode are missing.
- Do not close the LoggerProvider on `OnShutdown`. CoreDNS reload re-enters `setup`; the shared emitter is reused and the closed provider would silently drop every subsequent event.
- The plugin is process-scoped, not zone-scoped. A misconfigured `CLAWKER_COREDNS_OTEL_ENDPOINT` produces one Warning at boot (naming the env var), then silence — operators reading per-zone Corefile blocks may think otel is wired but the noopEmitter is doing nothing. Watch the boot log line.
- Endpoint env var has no schema prefix — it's host:port. mTLS is forced by the client TLS config; do not prefix `grpcs://` or similar.
- `otel.SetErrorHandler` is process-global and the plugin sets it under its own `sync.Once` so re-entrant `newProvider` calls (the retry-on-error path) don't clobber it. Any future co-resident OTel SDK use in the same binary must be aware that this handler is already wired to clog.
- The `Emit` error return on the `Emitter` interface exists for the test seam (`recordingEmitter` injects errors). Production `otelEmitter.Emit` always returns nil — export failures surface via `otel.SetErrorHandler`, not the call site. The `log.Warningf` in `Handler.ServeDNS` defending against Emit errors is unreachable today but kept for the test contract and to defend against future Emitter implementations.
