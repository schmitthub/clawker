# netlogger subpackage

Userspace consumer of the BPF `events_ringbuf`. Drains per-decision-point egress records, enriches each with `{container_id, agent, project, domain}` attribution looked up by `cgroup_id`, and hands the result to a `Sink`. Lives under `internal/controlplane/firewall/ebpf/` because it is the userspace half of the ebpf egress event emitter — the BPF programs in `bpf/clawker.c` submit records into the pinned ringbuf; this package shapes them into the OTLP log stream the monitoring backend consumes.

Task scope:

- **Task 2 (this branch)**: scaffold — ringbuf reader, LabelCache, ReverseDNSMap, processor + Sink interface, lifecycle. No OTLP wiring yet (`nopSink` / `stdoutSink` only).
- **Task 3**: OTel sink + generic `NewOtelLoggerProvider` + circuit breaker + CP main wiring + drain hook.
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
       Sink               (nopSink | stdoutSink | otelSink-in-Task-3)
```

The reader MUST NOT block on the queue. Back-pressure from the queue into the kernel converts userspace queue drops (counted by `QueueDropped`) into kernel-fault drops (counted by `events_drops` in BPF), which are more expensive to attribute and harder to alert on. Drop-newest at the queue boundary is the bounded-back-pressure half of the contract.

## Files

| File | Purpose |
|------|---------|
| `netlogger.go` | `Service` struct, `New(Deps)`, `Start(ctx)`, `Stop(ctx)`, overseer subscriptions for enroll + evict, `handleEnroll` (one docker ContainerInspect per enroll) |
| `event.go` | `Event` struct, `Verdict` enum, `parseEvent([]byte) (Event, error)` — decodes ringbuf records via `binary.NativeEndian` against the bpf2go-generated `ebpf.EgressEvent` |
| `cache.go` | `LabelCache` — slice + dual-index (`byCgroup`/`byCont`) + invalid-flag + free-list. Mutex-guarded. Single eviction call drops both indices atomically so cgroup-id reuse can't return stale labels |
| `reverse_dns.go` | `ReverseDNSMap` — periodic walk of pinned `dns_cache` map. v1 limitation: dns_cache stores only hashes, so Lookup returns `""` until a follow-up branch populates strings. Walk seam is injectable for tests (no real BPF map needed) |
| `reader.go` | `reader` — ringbuf drain goroutine. Recovers; bumps RingbufReceived/RingbufErrors/QueueDropped |
| `processor.go` | `processor` — queue-consumer goroutine. Recovers; bumps QueueReceived/ParseErrors/EmitSucceeded |
| `sink.go` | `Sink` interface + `nopSink` + `stdoutSink` (JSON-per-line — break-glass + Task-2 acceptance) |
| `metrics.go` | `Metrics` struct declaring the six Prom counters. Counters created unregistered; Task 3 wires `MustRegister` |

## Public API

```go
type Sink interface { Emit(ctx context.Context, ev Event) }

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

type Service struct { /* unexported */ }
func New(Deps) (*Service, error)
func (*Service) Start(context.Context) error
func (*Service) Stop(context.Context) error
func (*Service) Metrics() *Metrics
func (*Service) LabelCache() *LabelCache
```

`Deps` carries `Mgr *ebpf.Manager`, `Bus *overseer.Overseer`, `Docker ContainerInspecter`, `Cfg config.Config`, `Sink Sink`, plus optional `Log`, `QueueBuffer`, `ReverseDNSInterval`, `StopTimeout`. Task 3 adds `OtelLoggerProvider *sdklog.LoggerProvider` for the OTel sink path.

## Strict directive (load-bearing across all four files)

Every field on `Event` lands as an attribute in the Task-3 OTel sink. Empty/zero values are emitted verbatim — sinks NEVER drop a field because its value is zero. Adding a field to `Event` is a contract change that requires updating `otelSink.Emit` (Task 3), the E2E asserter (Task 4), and the `stdoutRecord` JSON shape in `sink.go`. Operators decide which fields matter at dashboard/query time; the emitter's job is to keep the schema dense.

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

## ReverseDNSMap v1 limitation

`dns_cache` stores `{IPv4 → {domain_hash, expire_ts}}`. The domain string lives only in CoreDNS process memory — the BPF side has access only to the hash. ReverseDNSMap therefore tracks the SET of observed hashes (periodic walk of the pinned map) but Lookup returns `""` for every hash until a follow-up branch populates strings via a new `domain_strings` map. Until then, operators filter on the numeric `domain_hash` attribute on each emitted record. The scaffolding is in place; the follow-up branch flips Lookup's return value without touching the public API.

## Prom counters

Declared in `metrics.go`, NOT yet registered (Task 3 wires `MustRegister` with the CP-global registry):

| Counter | Bumped where | Meaning |
|---------|--------------|---------|
| `clawker_netlogger_ringbuf_received_total` | reader.drain | Successful ringbuf record reads |
| `clawker_netlogger_ringbuf_errors_total` | reader.drain | Non-ErrClosed read failures |
| `clawker_netlogger_queue_dropped_total` | reader.drain | Records dropped because the queue was full |
| `clawker_netlogger_queue_received_total` | processor.run | Records pulled off the queue |
| `clawker_netlogger_parse_errors_total` | processor.run | Records that failed to decode |
| `clawker_netlogger_emit_succeeded_total` | processor.run | Sink.Emit returns |

Two additional dimensions land in Task 3 (gauges refreshed from BPF maps): `clawker_netlogger_ringbuf_kernel_drops_total` (sum across CPUs of `events_drops`) and `clawker_netlogger_ratelimit_drops_total{cgroup_id}` (per-cgroup `ratelimit_drops`).

## Lifecycle and drain ordering

`Service.Start`:

1. Open the ringbuf.Reader via `ringbuf.NewReader(mgr.EventsRingbuf())`.
2. Subscribe to the overseer bus (`EBPFContainerEnrolled` + filtered `dockerevents.DockerEvent`) — BEFORE launching the consumer goroutines so any event that lands in the spin-up window still hydrates the cache.
3. Launch the reader goroutine, the processor goroutine, and the reverse-DNS refresher.

No explicit cache backfill is needed at startup. `firewall.Handler.FirewallInit` already re-enrolls every running managed agent — each re-enrollment publishes `EBPFContainerEnrolled`, which the subscription path hydrates. One code path covers both runtime and startup; the cache is naturally complete by the time the first BPF event arrives.

`Service.Stop`:

1. Unsubscribe from the overseer bus (so no new events feed the cache mid-teardown).
2. Cancel the inner ctx (the reverse-DNS refresher exits on the next tick).
3. Close the ringbuf.Reader — `reader.drain` returns on `ringbuf.ErrClosed` and closes the queue. The processor drains remaining queued records, then returns.
4. Wait on the goroutines with a bounded timeout (`defaultStopTimeout = 5s`); beyond that we proceed so netlogger is never the long pole during CP drain-to-zero.

The Task-3 CP wiring places `netloggerSvc.Stop(stopCtx)` BEFORE `ebpfMgr.FlushAll()` so the BatchProcessor flushes in-flight OTLP batches before the BPF maps are torn down.

## CP no-panic discipline

Every goroutine recovers (`defer func() { if r := recover(); r != nil { log.Error()... } }()`). The constructor returns `(nil, error)` on missing required deps — CP main logs `event=netlogger_unavailable` and degrades. No `panic()`, `log.Fatal()`, or `os.Exit()` in any code path. The structured logs are the operator's surface — they will not see panic stacks. See the root `CLAUDE.md` for the canonical rationale.

## Test seams

- `Sink` interface — unit tests use `nopSink` or a hand-rolled recording sink; never construct an `otelSink` (it requires a live OTel collector dial).
- `readerSource` interface — reader tests use `fakeRingbuf` that scripts a sequence of records + errors. Skips the CAP_BPF dependency entirely.
- `ReverseDNSMap.walk` function field — tests inject a stub that returns scripted hashes without touching cilium/ebpf.
- `ContainerInspecter` interface — Service tests use `fakeInspecter` with a map of canned `ContainerInspectResult` rows. Pattern matches `internal/controlplane/agent/peer_lookup_moby.go`.
- `newTestService` helper in `netlogger_test.go` — wires `subscribeBus` without invoking `Start` (which would require CAP_BPF). Exercises the bus subscription path end-to-end on the in-process overseer bus.

## Imports

- **Uses**: `internal/config`, `internal/logger`, `internal/controlplane/dockerevents`, `internal/controlplane/firewall/ebpf` (for `EgressEvent`, `EBPFContainerEnrolled`, `Manager` accessors), `internal/controlplane/overseer`, `github.com/cilium/ebpf/ringbuf`, `github.com/moby/moby/client` + `api/types/container,events` (daemon-side; permitted under the docker-client.md exception).
- **Imported by**: `cmd/clawker-cp/main.go` (Task 3 boot wiring).
