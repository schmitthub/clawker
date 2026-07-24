# netlogger subpackage

Userspace consumer of the BPF `events_ringbuf`. Drains per-decision-point egress records, enriches each with `{container_id, agent, project, domain}` attribution looked up by `cgroup_id`, and pushes the result through an OTel log sink to the trusted-infra OTLP receiver. Lives under `internal/controlplane/firewall/ebpf/` because it is the userspace half of the ebpf egress event emitter — the BPF programs in `bpf/clawker.c` submit records into the pinned ringbuf; this package shapes them into the OTLP log stream the monitoring backend consumes.

The package ships as a per-decision-point egress event emitter with an OTel sink and a circuit breaker; CP main constructs the `*sdklog.LoggerProvider` via `controlplane.NewOtelLoggerProvider`, wraps it with `NewCircuitExporter`, and wires it through `Deps.OtelLoggerProvider`. The drain hook places `Service.Stop(stopCtx)` before `ebpfMgr.FlushAll()`.

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
| `reverse_dns.go` | `ReverseDNSMap` — periodic refresh of the `identity → dst` table from `IdentitySource` (a snapshot closure over the firewall IdentityAllocator); walks the pinned `dns_cache` as a triage-only signal for unattributed identities. Walk seam is injectable for tests (no real BPF map needed) |
| `reader.go` | `reader` — ringbuf drain goroutine. Recovers; bumps RingbufReceived/RingbufErrors/QueueDropped |
| `processor.go` | `processor` — queue-consumer goroutine. Recovers; bumps QueueReceived/ParseErrors/EmitSucceeded |
| `sink.go` | `Sink` interface + internal `nopSink`. No public sink constructors — the OTel-backed sink is constructed in `New` when `Deps.OtelLoggerProvider` is non-nil, and the nopSink is the test/degraded default. |
| `otel_sink.go` | `otelSink` + `newOtelSink(provider)` — emits every Event field as an attribute on a `*otellog.Record`; scope `clawker.netlogger`, event.name `ebpf.egress` |
| `circuit.go` | `circuitExporter` + `NewCircuitExporter(inner, CircuitOptions)` — wraps `sdklog.Exporter`; after N consecutive Export failures (default 3) trips permanently and drops records on the floor with a single `event=netlogger_collector_lost` log line. No probe loop; reconnect requires CP restart. |
| `metrics.go` | `Metrics` struct declaring the six pipeline Prom counters. Counters are created unregistered and bumped in-process; CP has no `/metrics` scrape endpoint, so values are not visible outside the CP process. |

## OTel sink + provider wiring

**Resource attribution.** `service.name=ebpf-egress` so the OS routing/connector pipeline drops netlogger records into their own data stream, separate from `clawkercp` (the CP zerolog bridge). Identity-layer reuse stays: same `otelcerts.Service`, same per-handshake leaf mint, same gRPC endpoint on `OtelInfraPort`.

**Instrumentation scope + event taxonomy.** Records carry scope name `clawker.netlogger`. `event.name` is per-emit-site so dashboards can filter by record kind without inspecting `flags`:

| BPF program | event.name |
|-------------|------------|
| `clawker_connect4` / `clawker_connect6` | `ebpf.egress.connect` |
| `clawker_sendmsg4` / `clawker_sendmsg6` | `ebpf.egress.sendmsg` |
| `clawker_sock_create` | `ebpf.egress.sock_create` |

`emit_site` enum is encoded in `flags` bits 3-4 (2 bits, 3 values used; 1 reserved). Userspace `parseEvent` decodes `(rec.Flags & EgressEmitMask) >> 3` into `Event.EmitSite`, which the OTel sink renders via `EmitSite.EventName()`. Future netlogger-emitted event types (e.g. sock-state) can either share `ebpf.egress.*` namespace or take a new scope.

**Record shape (strict directive with per-code-path carve-outs).** Every field on `Event` lands as an attribute on every emitted record. Empty strings and zero numbers are emitted verbatim — never dropped. Carve-outs: optional attributes are OMITTED when their source value is absent so OS Discover renders empty cells and operators partition cleanly via `_exists_:attributes.<key>` / `NOT _exists_:attributes.<key>`. Adding a field to `Event` is a contract change that requires updating `otelSink.Emit` in the same diff.

Schema discipline:
  - **Resource layer** (routing + provenance) — `service.name=ebpf-egress` (drives collector routing/trusted dispatch + dedicated index), `ingest_source=netlogger` (stamped by `resource/netlogger` processor post-routing as the trust-lane attribution). These are NOT re-emitted on every record. Per-record dupes of process identity (`source`, `component`) were dropped — `service.name` + `ingest_source` already discriminate.
  - **Per-record layer** = event taxonomy (`event.name`) + payload.

Attribute keys today:

| Attribute | Type | Source | Omit-when |
|-----------|------|--------|-----------|
| `event.name` | string | per-emit-site via `Event.EmitSite.EventName()` — `ebpf.egress.{connect,sendmsg,sock_create}`. The OS OTLP exporter does not project `LogRecord.event_name` into the SS4O document; netlogger emits `event.name` as an attribute too so OSD can filter by it. `SetEventName` is kept for consumers that honor the OTLP field (e.g. Loki). | never |
| `action` | string | `Event.Verdict.String()` (`allowed`/`denied`/`bypassed`) — the internal `Verdict` type stays the kernel-accurate name; only the emitted OTel attribute is renamed to `action` for parity with CoreDNS + Envoy and alignment with ECS / OCSF / Cloudflare / AWS VPC Flow Logs convention. | never |
| `container_id` | string | `Event.ContainerID` (empty on cache miss) | never (empty string emitted) |
| `agent` | string | `Event.Agent` | never (empty string emitted) |
| `project` | string | `Event.Project` | never (empty string emitted) |
| `cgroup_id` | string | `strconv.FormatUint(Event.CgroupID, 10)` — opaque kernel identifier; emitted as string so the OS index template maps it as `keyword` (group/filter dimension) instead of `long` (metric). | never |
| `bpf_ts_ns` | int64 | `Event.BPFTsNs` (raw `bpf_ktime_get_ns`) | never |
| `dst_ip` | string | `Event.DstIP.String()` — IPv4 dotted-quad or IPv6 colon form. OS index template maps `dst_ip` as `type: ip`, accepts both. | `!Event.DstIP.IsValid()` (sock_create — NoDst=true; defensive when parseEvent left it zero) |
| `dst_port` | string | `strconv.FormatUint(uint64(Event.DstPort), 10)` — opaque port identifier, keyword-mapped (see cgroup_id rationale). | `Event.NoDst` (sock_create has no port) |
| `l4_proto` | string | SOCK_STREAM / SOCK_DGRAM / SOCK_RAW name | never |
| `l4_proto_code` | int | raw SOCK code (resilient to renames) | never |
| `ipv6` | bool | native IPv6 destination — full 16-byte address carried in `dst_ip` | never |
| `ipv4_mapped` | bool | `::ffff:x.x.x.x` dual-stack destination | never |
| `no_dst` | bool | `Event.NoDst` — sock_create event with no destination | never |
| `dst_host` | string | `Event.Domain` populated via `ReverseDNSMap.Lookup(Event.Identity)` | `Event.Domain == ""` (direct-IP connect, domain outside firewall rules, stale dnsbpf entry) |
| `identity` | string | `strconv.FormatUint(uint64(Event.Identity), 10)` — CP-allocated route identity for the resolved domain. Operators correlate userspace records with BPF `dns_cache` / `route_map` entries when `dst_host` is empty. | never |

**Address representation.** Follows the Cilium / Tetragon convention: BPF's `struct egress_event.dst_ip` is a flat 16-byte slot in network byte order. IPv4 destinations occupy the first 4 bytes (the same shape as `ctx->user_ip4`) with the remaining 12 bytes zero; IPv6 destinations fill all 16 bytes. `EgressFlagIPv6` / `EgressFlagIPv4Mapped` / `EgressFlagNoDst` discriminate. Userspace `parseEvent` switches on flags: NoDst leaves the address invalid, IPv6 decodes via `netip.AddrFrom16`, default decodes the low 4 bytes via `netip.AddrFrom4`. `netip.Addr.String()` produces the right shape for either family, and OS `type: ip` mapping accepts both string forms — single attribute name `dst_ip` handles all cases (no `dst_ipv4`/`dst_ipv6` split).

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
    Identities         IdentitySource        // nil → degraded mode (dst_host always "")
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
    DstIP       netip.Addr // invalid when NoDst=true; v4 or v6 otherwise
    DstPort     uint16
    L4Proto     uint8
    IsIPv6      bool
    IsMapped    bool
    NoDst       bool       // sock_create — no destination exists
    EmitSite    EmitSite   // which BPF program submitted the event; drives event.name
    Identity    ebpf.RouteIdentity
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

`dns_cache` stores `{IPv4 → {identity, expire_ts, source}}`. The destination string lives only on the control plane side — the firewall IdentityAllocator's table, which also keys `route_map` and feeds the Corefile's `dnsbpf <identity>` directives. ReverseDNSMap holds the `identity → dst` table, rebuilt every refresh tick by snapshotting `Deps.Identities` — a closure over `IdentityAllocator.Snapshot` in production wiring. Attribution is a direct read of the allocation, not a hash inversion — both sides read one allocation by construction, and IP-literal seeds are covered because the allocator assigns identities to IP/CIDR dsts too. The walk over the pinned dns_cache stays as a triage signal: identities present in dns_cache but absent from the IdentitySource emit `event=netlogger_reverse_dns_unattributed` (race after rule remove / dnsbpf stale entry / unknown source).

`Lookup(identity)` returns the dst string when known, `""` otherwise. Empty cases:
- `identity == 0` — direct-IP connect, BPF saw no DNS context.
- Nil `Deps.Identities` (degraded mode before CP main wiring lands).
- Identity absent from IdentitySource (race or stale; logged on refresh).

## Prom counters (in-process; scrape not wired)

Declared in `metrics.go`. The six counters below are incremented on every relevant pipeline event but are NOT registered with a `prometheus.Registerer` because CP has no `/metrics` HTTP endpoint. Counters are visible only for in-process introspection; nothing outside the CP process can scrape them.

| Counter | Bumped where | Meaning |
|---------|--------------|---------|
| `clawker_netlogger_ringbuf_received_total` | reader.drain | Successful ringbuf record reads |
| `clawker_netlogger_ringbuf_errors_total` | reader.drain | Non-ErrClosed read failures |
| `clawker_netlogger_queue_dropped_total` | reader.drain | Records dropped because the queue was full |
| `clawker_netlogger_queue_received_total` | processor.run | Records pulled off the queue |
| `clawker_netlogger_parse_errors_total` | processor.run | Records that failed to decode |
| `clawker_netlogger_emit_succeeded_total` | processor.run | Sink.Emit returns |

Two additional dimensions exist on the BPF maps but are not scraped here: `events_drops` (PERCPU_ARRAY of kernel-fault drop counts) and `ratelimit_drops` (per-cgroup intentional rate-limit drops). They surface via `Manager.EventsDrops()` / `Manager.RatelimitDrops()` for in-process inspection.

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

The CP wiring (see `cmd/clawkercp/main.go::drainCallbackBody`) places `netloggerSvc.Stop(stopCtx)` BEFORE `ebpfMgr.FlushAll()` so the BatchProcessor flushes in-flight OTLP batches before the BPF maps the reader holds are torn down. Stop is wrapped in a `if netloggerSvc != nil` so the degraded path (boot-time provider failure) doesn't NPE.

## CP no-panic discipline

Every goroutine recovers (`defer func() { if r := recover(); r != nil { log.Error()... } }()`). The constructor returns `(nil, error)` on missing required deps — CP main logs `event=netlogger_unavailable` with the failing `step` field and degrades. No `panic()`, `log.Fatal()`, or `os.Exit()` in any code path. The structured logs are the operator's surface — they will not see panic stacks. See the root `CLAUDE.md` for the canonical rationale.

## Test seams

- `Sink` interface — pipeline tests use a hand-rolled `recordingSink` (see `processor_test.go`) to capture Emit calls. otelSink tests build a real `*sdklog.LoggerProvider` with a `SimpleProcessor` wrapping a `recordingExporter` (see `otel_sink_test.go`) so the SDK code paths get exercised without an OTLP gRPC dial.
- `readerSource` interface — reader tests use `fakeRingbuf` that scripts a sequence of records + errors. Skips the CAP_BPF dependency entirely.
- `ReverseDNSMap.walk` function field — tests inject a stub that returns scripted identities without touching cilium/ebpf.
- `ContainerInspecter` interface — Service tests use `fakeInspecter` with a map of canned `ContainerInspectResult` rows. Pattern matches `internal/controlplane/agent/peer_lookup_moby.go`.
- `newTestService` helper in `netlogger_test.go` — wires `subscribeBus` without invoking `Start` (which would require CAP_BPF). Exercises the bus subscription path end-to-end on the in-process overseer bus.
- `circuitExporter` tests use a `flakyExporter` that returns a scripted sequence of errors. No SDK or network involvement.

## Imports

- **Uses**: `internal/config`, `internal/logger`, `internal/controlplane/dockerevents`, `internal/controlplane/firewall/ebpf` (for `EgressEvent`, `EBPFContainerEnrolled`, `Manager` accessors), `internal/controlplane/overseer`, `github.com/cilium/ebpf/ringbuf`, `github.com/moby/moby/client` + `api/types/container,events` (daemon-side; permitted under the docker-client.md exception), `go.opentelemetry.io/otel/log`, `go.opentelemetry.io/otel/sdk/log`.
- **Imported by**: `cmd/clawkercp/main.go` — constructs `*sdklog.LoggerProvider` via `controlplane.NewOtelLoggerProvider`, wraps with `NewCircuitExporter`, hands to `netlogger.New` via `Deps.OtelLoggerProvider`.
