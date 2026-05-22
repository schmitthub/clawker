# netlogger subpackage

Userspace consumer of the BPF `events_ringbuf`. Drains per-decision-point egress records, enriches each with `{container_id, agent, project, domain}` attribution looked up by `cgroup_id`, and pushes the result through an OTel log sink to the trusted-infra OTLP receiver. Lives under `internal/controlplane/firewall/ebpf/` because it is the userspace half of the ebpf egress event emitter — the BPF programs in `bpf/clawker.c` submit records into the pinned ringbuf; this package shapes them into the OTLP log stream the monitoring backend consumes.

Task scope shipped on this branch:

- **Task 2**: scaffold — ringbuf reader, LabelCache, ReverseDNSMap, processor + Sink interface, lifecycle.
- **Task 3 (current)**: OTel sink + circuit breaker; CP main constructs the provider via `controlplane.NewOtelLoggerProvider` and wires it through `Deps.OtelLoggerProvider`; drain hook places `Service.Stop(stopCtx)` before `ebpfMgr.FlushAll()`.
- **Task 4**: E2E test + Mintlify doc.

## Pipeline shape

```
events_ringbuf (kernel)
        │
        ▼
reader goroutine          (ringbuf.ReadInto, copies each record into a fresh []byte)
        │
        │ non-blocking send; default→drop newest; bumps QueueDropped
        ▼
queue chan []byte         (buffered, defaultQueueBuffer = 8192)
        │
        ▼
processor goroutine       (parseEvent → enrich via LabelCache + ReverseDNSMap → sink.Emit)
        │
        ▼
Sink                      (otelSink in production, nopSink for tests / degraded boot)
        │
        ▼
*sdklog.LoggerProvider → BatchProcessor → circuitExporter → otlploggrpc → otel-collector (mTLS, OtelInfraPort)
```

The reader MUST NOT block on the queue. Back-pressure from the queue into the kernel converts userspace queue drops (counted by `QueueDropped`) into kernel-fault drops (counted by `events_drops` in BPF), which are more expensive to attribute and harder to alert on. Drop-newest at the queue boundary is the bounded-back-pressure half of the contract.

## Files

| File | Purpose |
|------|---------|
| `netlogger.go` | `Service` struct, `New(Deps)`, `Start(ctx)`, `Stop(ctx)`, overseer subscriptions for enroll + evict, `handleEnroll` (one docker ContainerInspect per enroll). `Deps.OtelLoggerProvider` is the production sink anchor; nil routes to internal `nopSink`. |
| `event.go` | `Event` struct, `Verdict` enum, `parseEvent([]byte) (Event, error)` — decodes ringbuf records via `binary.NativeEndian` against the bpf2go-generated `ebpf.EgressEvent` |
| `cache.go` | `LabelCache` — slice + dual-index (`byCgroup`/`byCont`) + invalid-flag + free-list. Mutex-guarded. Single eviction call drops both indices atomically so cgroup-id reuse can't return stale labels |
| `reverse_dns.go` | `ReverseDNSMap` — periodic walk of pinned `dns_cache` map. v1 limitation: dns_cache stores only hashes, so Lookup returns `""` until a follow-up branch populates strings. Walk seam is injectable for tests (no real BPF map needed) |
| `reader.go` | `reader` — ringbuf drain goroutine. Recovers; bumps RingbufReceived/RingbufErrors/QueueDropped |
| `processor.go` | `processor` — queue-consumer goroutine. Recovers; bumps QueueReceived/ParseErrors/EmitSucceeded |
| `sink.go` | `Sink` interface + internal `nopSink`. No public sink constructors — the OTel-backed sink is constructed in `New` when `Deps.OtelLoggerProvider` is non-nil, and the nopSink is the test/degraded default. |
| `otel_sink.go` | `otelSink` + `newOtelSink(provider)` — emits every Event field as an attribute on a `*otellog.Record`; scope `clawker.netlogger`, event.name `ebpf.egress` |
| `circuit.go` | `circuitExporter` + `NewCircuitExporter(inner, CircuitOptions)` — wraps `sdklog.Exporter`; after N consecutive Export failures (default 3) trips permanently and drops records on the floor with a single `event=netlogger_collector_lost` log line. No probe loop; reconnect requires CP restart. |
| `metrics.go` | `Metrics` struct declaring the six pipeline Prom counters. Counters created unregistered; scrape wiring is deferred to a follow-up PR (see TODO at the top of the file). |

## OTel sink + provider wiring

**Resource attribution.** `service.name=ebpf-egress` so the OS routing/connector pipeline drops netlogger records into their own data stream, separate from `clawker-cp` (the CP zerolog bridge). Identity-layer reuse stays: same `otelcerts.Service`, same per-handshake leaf mint, same gRPC endpoint on `OtelInfraPort`.

**Instrumentation scope.** Records carry scope name `clawker.netlogger` and event name `ebpf.egress`. Future netlogger-emitted event types (e.g. sock-state) can share the provider but use a distinct scope/event-name so subscribers within the stream can filter cleanly.

**Record shape (strict directive).** Every field on `Event` lands as an attribute on every emitted record. Empty strings and zero numbers are emitted verbatim — never dropped. Adding a field to `Event` is a contract change that requires updating `otelSink.Emit` in the same diff. Attribute keys today:

| Attribute | Type | Source |
|-----------|------|--------|
| `event.name` | string | constant `"ebpf.egress"`. Mirrors what `rec.SetEventName(...)` populates on the OTLP LogRecord, but the OS OTLP exporter does NOT currently project `LogRecord.event_name` into the SS4O document — emit as an attribute too so OSD can filter by it. Keep `SetEventName` for downstream consumers that DO honor the OTLP field (Loki, future OS releases). |
| `source` | string | constant `"ebpf"` |
| `verdict` | string | `Event.Verdict.String()` (`allowed`/`denied`/`bypassed`) |
| `container_id` | string | `Event.ContainerID` (empty on cache miss) |
| `agent` | string | `Event.Agent` |
| `project` | string | `Event.Project` |
| `cgroup_id` | string | `strconv.FormatUint(Event.CgroupID, 10)` — opaque kernel identifier; emitted as string so the OS index template maps it as `keyword` (group/filter dimension) instead of `long` (metric). Sending a JSON number to a keyword field is officially supported via numeric→string coercion but operator UIs treat numerics as metrics by default, which is wrong for ID-shaped fields. |
| `bpf_ts_ns` | int64 | `Event.BPFTsNs` (raw `bpf_ktime_get_ns`) |
| `dst_ip` | string | `Event.DstIP.String()` |
| `dst_port` | string | `strconv.FormatUint(uint64(Event.DstPort), 10)` — opaque port identifier; emitted as string for the same reason as `cgroup_id` (keyword dimension, not metric). OSD formats numeric fields with thousands separators ("4,318") which is wrong for an ID-shaped axis. |
| `l4_proto` | string | SOCK_STREAM / SOCK_DGRAM / SOCK_RAW name |
| `l4_proto_code` | int | raw SOCK code (resilient to renames) |
| `ipv6` | bool | native IPv6 |
| `ipv4_mapped` | bool | `::ffff:x.x.x.x` |
| `dst_host` | string | `Event.Domain` populated via `ReverseDNSMap.Lookup(Event.DomainHash)`; `""` for direct-IP connects or domains outside the firewall rule set |

**Trust lane.** Endpoint is the infra OTel receiver (`OtelInfraPort`). Plaintext endpoints are rejected at CP-main wiring time (`event=netlogger_unavailable` with `step=OTLP endpoint is plaintext`) — infra emitters never cross into the untrusted agent lane.

**Collector-unavailable posture.**

1. **Startup preflight**: `controlplane.NewOtelLoggerProvider` performs a one-shot TLS dial (`PreflightTimeout=20s`) against the OTLP endpoint. If the dial fails, the constructor returns an error and CP main emits `event=netlogger_unavailable` with `step=NewOtelLoggerProvider` — `netloggerSvc` stays nil, the BatchProcessor goroutine is never started, telemetry is dropped on the floor for the rest of the CP lifetime.
2. **Runtime circuit breaker**: `circuitExporter` wraps the OTLP exporter inside the BatchProcessor. After 3 consecutive `Export` failures the breaker permanently trips: subsequent `Export` calls return nil immediately (so the BatchProcessor records a successful export and the queue drains via natural drop-oldest) and a single `event=netlogger_collector_lost` line fires.
3. **No background reconnect.** Telemetry availability is binary per-CP-lifetime: either the collector was up at boot and stayed up enough to keep the circuit closed, or netlogger is dropping. Operators recover by restarting CP once the monitoring stack returns.

## Public API

```go
type Service struct { /* unexported */ }

type Deps struct {
    Mgr                *ebpf.Manager
    Bus                *overseer.Overseer
    Docker             ContainerInspecter
    Cfg                config.Config
    OtelLoggerProvider *sdklog.LoggerProvider // nil → nopSink (degraded / test)
    Log                *logger.Logger
    QueueBuffer        int
    ReverseDNSInterval time.Duration
    StopTimeout        time.Duration
}

func New(Deps) (*Service, error)
func (*Service) Start(context.Context) error
func (*Service) Stop(context.Context) error
func (*Service) Metrics() *Metrics
func (*Service) LabelCache() *LabelCache

type Event struct {
    Timestamp   time.Time
    BPFTsNs     uint64
    CgroupID    uint64
    ContainerID string
    Agent       string
    Project     string
    DstIP       netip.Addr
    DstPort     uint16
    L4Proto     uint8
    IsIPv6      bool
    IsMapped    bool
    DomainHash  uint32
    Domain      string
    Verdict     Verdict
}

type Sink interface { Emit(ctx context.Context, ev Event) }
```

`Sink` is the internal pipeline interface — `otelSink` and `nopSink` are the only implementations and both are unexported. The Service chooses between them in `New` based on `Deps.OtelLoggerProvider`.

## LabelCache design notes

The cache resolves a kernel-attested `cgroup_id` to userspace `{container_id, agent, project}`. Hydrated by `ebpf.EBPFContainerEnrolled` overseer events published by `firewall.Handler.FirewallEnable` after a successful BPF install; evicted by `dockerevents.DockerEvent` with `Type=container, Action ∈ {die, destroy}`.

Storage:

- Slice of `labelEntry{cgroupID, containerID, agent, project, invalid}`
- `byCgroup map[uint64]int` — primary lookup index
- `byCont map[string]int` — eviction lookup index
- `free []int` — recycled slot indices (avoids unbounded growth across long CP uptime)

Single mutex. Eviction MUST drop both index entries in the same critical section so a kernel cgroup-id reuse cannot return stale labels in the gap between "previous container died" and "new container enrolled on the reused cgroup_id". The `invalid` flag is defensive — production removes index entries on evict, so a Lookup hitting an invalid entry indicates a wiring bug elsewhere.

Why not `sync.Map`: two-key atomic update (cgroup_id index AND container_id index) is the load-bearing operation; a single mutex makes that trivial and the lookup contention is low because LabelCache is read once per BPF event (rate-bounded to ~640/sec/cgroup).

Why not an LRU: cgroup_id reuse is event-driven, not time-driven. An LRU would either evict live entries (wiping attribution mid-traffic) or hand back dead labels for reused cgroup_id values.

## ReverseDNSMap

`dns_cache` stores `{IPv4 → {domain_hash, expire_ts}}`. The domain string lives only on the control plane side (the firewall rule set + the internal hostnames CoreDNS serves out of band). ReverseDNSMap holds the inverse `hash → domain` table, rebuilt every refresh tick by hashing the live set returned by `Deps.Domains` — a closure over `firewall.Handler.AllResolvableDomains` in production wiring. The walk over the pinned dns_cache stays as a triage signal: hashes present in dns_cache but absent from the DomainSource emit `event=netlogger_reverse_dns_unattributed` (race after rule remove / dnsbpf stale entry / unknown source).

`Lookup(hash)` returns the domain string when known, `""` otherwise. Empty cases:
- `hash == 0` — direct-IP connect, BPF saw no DNS context.
- Nil `Deps.Domains` (degraded mode before CP main wiring lands).
- Hash absent from DomainSource (race or stale; logged on refresh).

The hash function is `internal/controlplane/firewall/ebpf.DomainHash` (FNV-1a of the lowercased domain) — the same call dnsbpf uses when it writes `dns_cache`. The collision floor is tracked separately; see `initiative_route_identity_allocator`.

## Prom counters (in-process today; scrape deferred)

Declared in `metrics.go`. The six counters below are incremented on every relevant pipeline event but are NOT registered with a `prometheus.Registerer` because CP has no `/metrics` HTTP endpoint today. A follow-up PR wires scraping along with the additional dimensions the initiative listed for Task 3 (kernel-side gauges, OTel-export success/failure counters via a counting-exporter wrap).

| Counter | Bumped where | Meaning |
|---------|--------------|---------|
| `clawker_netlogger_ringbuf_received_total` | reader.drain | Successful ringbuf record reads |
| `clawker_netlogger_ringbuf_errors_total` | reader.drain | Non-ErrClosed read failures |
| `clawker_netlogger_queue_dropped_total` | reader.drain | Records dropped because the queue was full |
| `clawker_netlogger_queue_received_total` | processor.run | Records pulled off the queue |
| `clawker_netlogger_parse_errors_total` | processor.run | Records that failed to decode |
| `clawker_netlogger_emit_succeeded_total` | processor.run | Sink.Emit returns |

Two additional dimensions are deferred with the scrape wiring: `clawker_netlogger_ringbuf_kernel_drops_total` (sum across CPUs of `events_drops`) and `clawker_netlogger_ratelimit_drops_total{cgroup_id}` (per-cgroup `ratelimit_drops`).

## Lifecycle and drain ordering

`Service.Start`:

1. Open the ringbuf.Reader via `ringbuf.NewReader(mgr.EventsRingbuf())`.
2. Subscribe to the overseer bus (`EBPFContainerEnrolled` + filtered `dockerevents.DockerEvent`) — BEFORE launching the consumer goroutines so any event that lands in the spin-up window still hydrates the cache.
3. Launch the reader goroutine, the processor goroutine, and the reverse-DNS refresher.

No explicit cache backfill is needed at startup. `firewall.Handler.FirewallInit` already re-enrolls every running managed agent — each re-enrollment publishes `EBPFContainerEnrolled`, which the subscription path hydrates. One code path covers both runtime and startup; the cache is naturally complete by the time the first BPF event arrives.

`Service.Stop`:

1. Unsubscribe from the overseer bus (so no new events feed the cache mid-teardown).
2. Close the ringbuf.Reader — `reader.drain` returns on `ringbuf.ErrClosed` and closes the queue. The processor drains remaining queued records, then returns.
3. Cancel the inner ctx (the reverse-DNS refresher exits on the next tick; subscriber goroutines exit on the next select).
4. Wait on the goroutines with a bounded timeout (`defaultStopTimeout = 5s`); beyond that we proceed so netlogger is never the long pole during CP drain-to-zero.

The CP wiring (see `cmd/clawker-cp/main.go::drainCallbackBody`) places `netloggerSvc.Stop(stopCtx)` BEFORE `ebpfMgr.FlushAll()` so the BatchProcessor flushes in-flight OTLP batches before the BPF maps the reader holds are torn down. Stop is wrapped in a `if netloggerSvc != nil` so the degraded path (boot-time provider failure) doesn't NPE.

## CP no-panic discipline

Every goroutine recovers (`defer func() { if r := recover(); r != nil { log.Error()... } }()`). The constructor returns `(nil, error)` on missing required deps — CP main logs `event=netlogger_unavailable` with the failing `step` field and degrades. No `panic()`, `log.Fatal()`, or `os.Exit()` in any code path. The structured logs are the operator's surface — they will not see panic stacks. See the root `CLAUDE.md` for the canonical rationale.

## Test seams

- `Sink` interface — pipeline tests use a hand-rolled `recordingSink` (see `processor_test.go`) to capture Emit calls. otelSink tests build a real `*sdklog.LoggerProvider` with a `SimpleProcessor` wrapping a `recordingExporter` (see `otel_sink_test.go`) so the SDK code paths get exercised without an OTLP gRPC dial.
- `readerSource` interface — reader tests use `fakeRingbuf` that scripts a sequence of records + errors. Skips the CAP_BPF dependency entirely.
- `ReverseDNSMap.walk` function field — tests inject a stub that returns scripted hashes without touching cilium/ebpf.
- `ContainerInspecter` interface — Service tests use `fakeInspecter` with a map of canned `ContainerInspectResult` rows. Pattern matches `internal/controlplane/agent/peer_lookup_moby.go`.
- `newTestService` helper in `netlogger_test.go` — wires `subscribeBus` without invoking `Start` (which would require CAP_BPF). Exercises the bus subscription path end-to-end on the in-process overseer bus.
- `circuitExporter` tests use a `flakyExporter` that returns a scripted sequence of errors. No SDK or network involvement.

## Imports

- **Uses**: `internal/config`, `internal/logger`, `internal/controlplane/dockerevents`, `internal/controlplane/firewall/ebpf` (for `EgressEvent`, `EBPFContainerEnrolled`, `Manager` accessors), `internal/controlplane/overseer`, `github.com/cilium/ebpf/ringbuf`, `github.com/moby/moby/client` + `api/types/container,events` (daemon-side; permitted under the docker-client.md exception), `go.opentelemetry.io/otel/log`, `go.opentelemetry.io/otel/sdk/log`.
- **Imported by**: `cmd/clawker-cp/main.go` (Task 3 boot wiring) — constructs `*sdklog.LoggerProvider` via `controlplane.NewOtelLoggerProvider`, wraps with `NewCircuitExporter`, hands to `netlogger.New` via `Deps.OtelLoggerProvider`.
