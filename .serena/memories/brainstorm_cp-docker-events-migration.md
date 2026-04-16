# Brainstorm: CP Docker Events Migration

> **Status:** Design locked. Ready to forge.
> **Created:** 2026-04-16
> **Last Updated:** 2026-04-16
> **Rolls up to:** Branch 3 of CP initiative (`cp-initiative-status`)
> **Related memories:**
> - `brainstorm_the-controlplane-and-clawkerd` — broader CP roadmap
> - `clawker-pantheon` — Morgoth/Sauron/Nazgûl/Eye mental model + elven-craft (dependencies we bend to our purpose)
> - `cp-initiative-status` — branch sequencing
>
> **Workflow stance:** no `/cspec`, no `/ctdd`, no correctless ceremony
> for this initiative. Forge direct from this memory + the pantheon
> memory. Design is locked; translation into code doesn't need another
> spec layer.
>
> **Phase arc within the pantheon:**
> 1. **This PR** — Sauron opens his eyes. Worldview + Docker event subscription + elven-craft wired in. No acts.
> 2. **Phase 4** — Morgoth starts speaking to Sauron. `AnnounceAgent` RPC; `CLIClaim` fields begin to populate; attestation goes live.
> 3. **Phase 5** — Rings forged and handed out. `clawkerd` in every managed agent; PKCE handshake; per-agent certs minted; Nazgûl bound; `AgentClaim` fields populate.
> 4. **Phase 6** — The Eye opens. BPF ring buffer feeds kernel truth into the same worldview.
> 5. **Phase 7+** — Sauron acts. `AgentCommandService.RunCommand`, hot reload, kill switch. The One Ring binds all prior craft.

## Framing

Pantheon:

- **CLI = Morgoth.** The user's will made manifest. Root of trust for
  every clawker action. Issues commands, declares intent, destroys at
  pleasure. Speaks the realm into existence and out of it.
- **CP = Sauron.** Morgoth's lieutenant. Carries out the work locally,
  sees the whole realm at wire speed, enforces policy, verifies claims.
  Has independent power but ultimately defers to CLI — reports, waits
  for orders, attests.
- **clawkerd = Nazgûl (phase 5).** Sauron's bound servants inside each
  agent container. Report their inner truth up to Sauron, not directly
  to Morgoth.
- **BPF ring buffer = the Eye (phase 6).** Kernel-level omniscience.
  Sees what processes actually do, at wire speed.

The CP's authority comes from its sight. It fuses multiple truth
streams into one worldview:

| Source | What it reports | Phase |
|--------|-----------------|-------|
| Docker `/events` + `List`/`Inspect` | Observed truth from the daemon — what Docker thinks exists | **this PR** |
| CLI gRPC | Declared truth — what CLI told CP it is doing / about to do / has done | data model this PR; RPC wiring phases 4+ |
| clawkerd gRPC | Attested truth — what the agent's inner process reports about itself | phase 5 |
| BPF ring buffer | Kernel truth — actual syscalls and network behavior | phase 6 |

Today the CP is blind on all four. It polls Docker on 30s ticks for a
slice of (1), doesn't have the RPC surface for (2) or (3), and hasn't
wired (4). This initiative lights up (1) as a live stream and lays the
data model scaffolding so (2) plugs in additively when AnnounceAgent
lands. The worldview is **not events-shaped** — it is a reconciliation
engine over multiple incoming truth streams, seeded today by Docker
but designed from day one to carry CLI declarations and, later,
clawkerd attestations and BPF observations.

The core move this PR makes: the CP stops being a reactive query
proxy and becomes a continuously-updated model of its realm. Everything
that happens clawker-related flows through the model. RPC handlers
read from it, write to it, and wait on it. Anomaly detection falls out
of state-transition rules on the model, not ad-hoc code paths.


## Problem

The CP currently derives container-state knowledge from repeated
point-in-time queries. This has three separate costs, each real:

1. **Blindness between polls.** The `AgentWatcher` poll loop runs at
   30s, with a miss-streak of 2 and a 60s grace, so drain-to-zero takes
   2–3 minutes worst case. Anything that happens inside a poll window —
   crash, external kill, churn — is invisible to the CP until the next
   tick, and may be entirely missed if the state returns to what it was
   before the next poll.

2. **Work re-done on every query.** `Stack.Status` re-walks the daemon
   looking for Envoy + CoreDNS containers. `FirewallInit` re-walks it
   looking for agents. Every `/healthz` probe re-walks it looking for
   subsystem containers. The daemon answers the same question over and
   over because the CP has no memory.

3. **No provenance.** When the CLI calls `AnnounceAgent` (phase 4) and
   clawkerd later calls `Register` (phase 5), the CP has no independent
   check that the container claiming to be agent X is in fact the
   container the CLI announced. Today this is fine because neither RPC
   exists yet; by phase 5 it is a gaping hole.

Docker `/events` fixes all three by letting the CP know, in real time,
every lifecycle transition for every clawker-labelled resource on the
host.

## What "every clawker resource and everything that touches one" means

The CP watches two populations:

1. **Direct clawker resources** — anything carrying `dev.clawker.managed=clawker`
2. **Associated resources** — anything Docker does that *involves* a
   direct clawker resource, even if the associated resource itself is
   not labelled

The second class is load-bearing. Server-side label filter alone
catches (1) but misses (2). Examples of (2) that must not escape CP:

- `network connect non-clawker-net clawker-agent-1` — event is on a
  non-clawker network. Agent joining an un-firewalled network is an
  **exfiltration risk**. CP must see it.
- `volume mount shared-data clawker-agent-1` — volume is not clawker
  but is being mounted into a clawker container. Cross-resource
  entanglement.
- `docker attach` / `docker exec` from the host into a clawker
  container — container is clawker, actor context is external.
- External tool spawns a non-clawker container sharing a clawker
  volume. Association invisible from label filter.

### Subscription strategy

**No server-side label filter.** Subscribe to `/events` unfiltered;
dispatch decides relevance per event:

```
moby.Events(ctx, /* no filter */) → receive goroutine
  ↓
  dispatch(ev):
    1. actor.ID in worldview.byID?        → process as own-resource event
    2. create event + actor.attributes has clawker-managed label?
                                           → add to worldview, process
    3. event attributes reference a known clawker ID?
       (e.g., network.connect has `container=<id>` in attributes)
                                           → process as association event
    4. otherwise                           → drop
```

Receive goroutine only enqueues; dispatch runs on a worker pool with
bounded queue. Event rate on typical hosts is low; hot path is
`map[string]*Resource` lookup.

### What the worldview must track beyond individual resources

Omnipresence implies association, not just existence. The data model
carries edges, not only nodes:

- Resource → attached networks
- Resource → mounted volumes
- Resource → spawned exec sessions
- Resource → parent/child relationships where Docker exposes them

Edges populated from the initial inspect-on-list pass at boot and
maintained from events thereafter.

Queryable views this unlocks:

- "What non-clawker networks is agent X on?" → exfil detection
- "Which clawker resources share volume V?" → blast radius
- "What is the CP's own container currently attached to?" →
  self-inspection

### Full resource-type scope (direct + associated)

| Type | Direct actions | Association actions (non-labelled actors we still care about) |
|------|----------------|--------------------------------------------------------------|
| Container | create, start, die, destroy, kill, oom, pause, unpause, rename, health_status, restart, exec_create, exec_start, exec_die, attach, detach | any action whose attributes reference a known clawker container ID |
| Network | create, destroy | connect, disconnect where `container=<clawker-id>` in attributes (CRITICAL — exfil detection) |
| Volume | create, destroy | mount, unmount where container attribute is a clawker ID |
| Image | pull, push, delete, tag, untag, load, save | used-by events where container reference is clawker |
| Plugin | install, enable, disable, remove | usage by clawker resource (rare) |


## The senses feed one worldview

The point of this initiative is not "make the AgentWatcher faster". The
point is the CP maintains an authoritative, always-current, in-memory
model of the clawker footprint on this host. Every consumer inside the
CP — existing (`AgentWatcher`, `Stack.Status`, `FirewallInit`), imminent
(`AnnounceAgent`, `Register`), or future (audit, policy, kill-switch) —
reads from this model instead of re-querying the daemon.

That is what gives the CP authority. Not sight on one axis at polling
frequency, but continuous sight across every axis at wire speed.

## Why the CP can subscribe directly to moby

The docker-client layering rule (`.claude/rules/docker-client.md`) is
there to protect user-facing mutation paths from accidentally touching
non-clawker resources on the user's machine. `pkg/whail` is the
label-jail; `internal/docker` is the consumer-layer wrapper; callers
route through `internal/docker`.

None of that threat model applies here:

- Events is **read-only**. There is nothing to mutate, nothing to
  accidentally delete, nothing to "escape" onto.
- The CP runs as a privileged infra daemon inside a controlled
  container, not as a user-facing command.
- The rule grants **explicit exception**: *"Standalone daemon packages
  and the CP daemon entrypoint may import `github.com/moby/moby/client`
  directly. These are long-running daemon or infrastructure processes
  that need lightweight Docker API access (events, exec, container
  lifecycle) without whail's label isolation overhead."*

CP subscribes to moby directly. No whail, no `internal/docker`.

## Docker events API — load-bearing facts (from moby/moby deepwiki)

- **Daemon buffers last 256 events only.** Slow consumer / disconnect
  beyond this window = unrecoverable loss.
- **Messages channel is unbuffered.** Consumer backpressure blocks the
  receive goroutine; dispatch must never block the moby receive.
- **No auto-resume.** Disconnect → `Err` fires → caller must re-invoke
  `Events()`. `Since=` retrieves missed events only if still inside the
  256-slot buffer — never trust it across real gaps.
- **List + subscribe is the canonical reconciliation pattern.** Daemon
  internally handles the mutex race between snapshot and stream so
  there's no gap within one subscribe call. Periodic full re-list is
  still needed as a safety net against buffer overflow + daemon restart.
- **Label filter is server-side.** `Add("label", "dev.clawker.managed=clawker")`
  is cheap and precise; daemon never ships us unrelated events.
- **Event types span all Docker resource types.** One subscription
  covers containers, networks, volumes, images. Filter-by-Type at
  dispatch inside the CP.

## Open Items / Questions

### Altitude-1: purpose, authority, and shape of the new capability

- **Is the CP's worldview a passive cache or an active enforcement
  surface?** Passive = "CP knows, consumers query". Active = "CP sees
  something wrong, CP acts". v1 is almost certainly passive with
  structured logging of anomalies. But the decision of where the line
  is drawn — and what trips active action when it arrives — shapes the
  API surface we commit to now.
- **What does the CP do when reality disagrees with expectation?**
  Concrete cases:
  - Container bearing clawker labels appears with no CLI announcement
    (phase 4+). Ghost. Log? Kill? Alert?
  - Firewall stack container dies unexpectedly. Restart? Degrade health?
  - `clawker-net` network deleted while CP is running. Any recovery, or
    self-shutdown?
  - CP's own container receives a `die` event (external kill).
    Graceful-drain-before-SIGKILL or nothing to do?
  These are policy questions with real security and reliability impact.
  They don't all need answering in this PR, but the API must not
  preclude any reasonable answer.
- **Trust hierarchy between sensory sources (phases 5, 6).** Docker
  says container X is running. clawkerd heartbeat for X is 60s stale.
  BPF ring buffer shows no syscalls from X in 60s. Who is authoritative
  for "is X working"? Sibling brainstorm's line — *Docker for "does it
  exist"; clawkerd for "is it working"; BPF for "what did it do"* — is
  the right frame but has edge cases we'll hit.

### Altitude-2: scope of v1

- **Resource types subscribed in v1.** Containers are required.
  Networks + volumes are cheap to add to the same subscription (same
  filter, same dispatch) — worth doing now, or deferred?
- **Consumers migrated in v1 vs staged.** The CP ships with three
  existing polling/list sites today. Which migrate under this
  initiative vs which wait?
  - `AgentWatcher` drain-to-zero — must migrate; this is what makes the
    event model observable
  - `Stack.Status` — could migrate (worldview knows stack containers
    exist); could stay (cheap `ContainerList`, works fine)
  - `reenrollAgents` — could consume worldview's agent list at boot
    instead of its own `ContainerList`; could stay
- **Phase-4 AnnounceAgent path.** Is the attestation wiring for
  AnnounceAgent part of *this* initiative (even though the RPC isn't
  built yet) or is it follow-on? If the worldview is shaped to serve
  AnnounceAgent from day one, the API locks in earlier; if not, we risk
  retrofitting.

### Altitude-3: mechanics

- **Stream health → CP health.** Repeated reconnect failure = blind CP.
  Does `/healthz` go 503? Crash loop? Alert and continue?
- **Backpressure between moby receive and consumer dispatch.** Moby's
  `Messages` channel is unbuffered. The receive goroutine must never
  block on a slow consumer. Worker pool, bounded queues with
  drop-oldest for non-critical consumers, bounded queues with
  block-and-queue for critical ones?
- **Retention per resource type.** Container lifecycle events are
  high-frequency-short-lived. Volume lifecycle events are rare and
  matter for hours. Flat ring buffer won't work — per-type retention
  policy needed.
- **Startup list race.** On CP boot: `ContainerList` to seed worldview,
  then subscribe. Daemon guarantees subscribe sees post-snapshot events
  with no gap — relies on that, doesn't try to merge `Since=` across
  it.
- **Anomaly classification.** What events get logged as anomalies vs
  routine? Every `start` is routine during normal operation; `start`
  with no matching announcement is anomaly (phase 4+). Needs taxonomy.
- **Package location.** `internal/controlplane/dockerevents/`,
  `internal/controlplane/worldview/`, or something else? Naming is
  load-bearing because this package becomes the place phase 5 + 6 wire
  their senses into.

## Decisions Made

- **Subscribe direct to moby client**, not through whail /
  `internal/docker`. Rule `docker-client.md` pre-authorizes CP daemon
  packages; read-only stream has no label-jail threat model to satisfy.
- **This initiative rolls up under CP Branch 3** for tracking only.
  No `/cspec` layer between this brainstorm and the forge — the design
  here is the contract.
- **Scope of subscription is every clawker-labelled resource on the
  host**, not just agent containers. One subscription, dispatched
  internally. This is the CP seeing everything clawker, not the CP
  replacing one poll loop.
- **Purpose is attestation + authority, not latency optimization.** The
  value of this work is that the CP can verify claims against its own
  senses and detect anomalies — drain-to-zero getting faster is an
  incidental benefit, not the reason to do the work.
- **Always list + subscribe; never trust `Since=` across disconnects.**
  Periodic full re-list as safety net against 256-slot buffer overflow.
- **No server-side label filter on the subscription.** Receive all
  Docker events; dispatch decides relevance by cross-reference against
  worldview's known-clawker-ID set + label presence on create events +
  attribute scan for association. Label filter alone misses association
  events (non-clawker network connecting a clawker agent, non-clawker
  volume mount on a clawker container, external `docker attach`/`exec`
  into a clawker container) — these are load-bearing for exfil
  detection and blast-radius analysis.
- **Worldview tracks association edges, not only node existence.**
  Resource→networks, Resource→volumes, Resource→exec sessions,
  Resource→parent/child. Populated from initial inspect + maintained
  from events. This is what makes "omnipresent" mean something.
- **Storage: in-memory only, k8s-informer pattern.** No SQLite, no
  bolt, no embedded DB. Docker daemon is the authoritative persistence
  layer; CP's worldview is derivable from `ContainerList` on restart.
  State is cheap to rebuild (N ≈ tens of resources per host). SQLite
  would add format-migration + corruption-recovery surface for zero
  value.
- **Audit trail and metrics are separate concerns from the worldview
  store.** Audit → zerolog file logger today, flows to Loki when
  monitor subsystem lands (phase 9). Metrics → Prometheus client on
  CP, scrape endpoint exposed. Neither lives in the worldview's data
  structure.
- **v1 scope is strictly foundational. No takeover, no enforcement, no
  consumer migrations.** "Sauron opens his eyes. Does not yet act."
  The PR builds the observability engine and lays the groundwork for
  future phases. Everything that exists today keeps working exactly as
  it does today; worldview is purely additive.

  **In this PR:**
  - Worldview package with types, in-memory store, indexes, association
    edges
  - Unfiltered Docker `/events` subscription with reconnect + backoff
  - Receive-goroutine → bounded queue → worker pool → worldview
    mutation pipeline
  - Initial `ContainerList` + per-resource `Inspect` seed pass at
    startup (to populate edges)
  - Periodic full re-list safety net (~5 min)
  - Consumer API surface (`Get`, `List`, `Await`, `Subscribe`) — built
    and tested even though no in-tree caller uses it yet
  - Prometheus metrics + `/metrics` endpoint
  - Structured audit logging via zerolog (every lifecycle transition,
    every ghost sighting, every stream disconnect)
  - Startup sequencing wired into `cmd/clawker-cp/main.go`
  - Data-model shape for `CLIClaim`, `AgentClaim`, `Attestation` —
    **fields exist, zero-valued, no writers in this PR**
  - Unit tests, Docker integration tests in `test/e2e/`
  - Package `CLAUDE.md`, updates to `.claude/docs/KEY-CONCEPTS.md` and
    `ARCHITECTURE.md`

  **Deferred to follow-up PRs (phases 4+):**
  - `AgentWatcher` polling → worldview consumption
  - `Stack.Status` / `discoverOrEmpty` → worldview consumption
  - `reenrollAgents` → worldview snapshot
  - `AnnounceAgent` RPC wiring (writes `CLIClaim`)
  - `Register` RPC wiring (writes `AgentClaim`)
  - Ghost / divergent response policy (v1 is log-only)
  - BPF ring buffer feeding the same worldview
  - Any actual enforcement action (restart, kill, quarantine)


## Storage shape (in-memory, k8s-informer-flavored)

```go
package worldview  // or dockerevents/ — name TBD

type ResourceType string  // "container" | "network" | "volume" | "image"
type Lifecycle string       // "created" | "running" | "stopped" | "destroyed" ...

type Resource struct {
    // Identity
    ID, Name  string
    Type      ResourceType
    Labels    map[string]string

    // Observed (Docker events + inspect) — the daemon's truth
    Lifecycle Lifecycle
    FirstSeen time.Time
    LastSeen  time.Time
    History   []Transition       // bounded ring, last ~50 transitions
    Edges     Edges              // networks, volumes, parents, exec sessions

    // Declared (CLI / clawkerd RPCs) — populated by RPC handlers, not events
    CLIClaim   *CLIClaim          // what CLI announced (phase 4+ wiring)
    AgentClaim *AgentClaim        // what clawkerd attested (phase 5+)

    // Derived (state machine over Observed × Declared)
    Attestation AttestationState
}

type AttestationState string  // aligned | divergent | orphan | ghost | pending
// aligned   — CLI/agent declared it, Docker confirms it, all match
// divergent — declared X, Docker shows Y (lying or buggy caller)
// orphan    — declared, no Docker observation within TTL
// ghost     — Docker shows clawker-labelled resource with no declaration (security event)
// pending   — one side has arrived, waiting on the other within TTL

type Transition struct {
    Action    string               // moby events.Action
    At        time.Time
    From, To  Lifecycle
    Attributes map[string]string   // event actor.attributes
}

type Worldview struct {
    mu        sync.RWMutex
    byID      map[string]*Resource
    byType    map[ResourceType]map[string]*Resource
    byPurpose map[string]map[string]*Resource
    // further indexes added as consumers need them (k8s Indexer pattern:
    // map[indexName]map[indexKey][]*Resource with named key-extractor funcs)

    subs     []*subscription   // bounded delta channels, drop-oldest for non-critical consumers
    metrics  *prom.Registry    // counters, gauges, histograms — scraped via /metrics
    log      *logger.Logger    // zerolog for audit trail
}
```

**Indexes grown on demand.** Ship v1 with `byID`, `byType`, `byPurpose`.
When phase-4 AnnounceAgent needs "find container by announced ID"
that's just `byID`; when phase-6 policy needs "find containers on
network X", add a `byNetwork` index.

**History ring per resource** (~50 transitions) supports "what happened
to this container recently" queries without unbounded memory.

**Consumer API surface (sketch, to be firmed in `/cspec`) — resource-general, not container-specific:**

```go
// Synchronous reads
Get(id string) (*Resource, bool)
List(filter Filter) []*Resource             // Filter: Type, Purpose, Labels, Lifecycle
ListByType(t ResourceType) []*Resource

// Await arbitrary predicate — spans any resource type, any action.
// Container example (phase-4 AnnounceAgent): pred = r.ID==X && t.Action=="start"
// Network example: pred = r.Labels["purpose"]=="agent" && t.Action=="disconnect"
// Volume example:  pred = r.Name==workspaceV && t.Action=="mount"
Await(ctx context.Context, pred Predicate) (*Resource, error)

// Streaming (audit/policy consumers, phase-6)
Subscribe(filter Filter) <-chan Delta        // bounded, non-blocking, drop-oldest
Unsubscribe(ch <-chan Delta)
```

`Predicate` is `func(*Resource, Transition) bool`. No container-specific
methods like `WaitForStart` — consumers compose the predicate for the
resource type and action they care about. This keeps the API surface
stable as new consumers target networks, volumes, images without
needing new API.

**Metrics exposed** (Prometheus conventions):

- `docker_events_total{type, action}` — counter
- `docker_events_reconnects_total` — counter
- `docker_events_connected` — gauge (0/1)
- `clawker_resources{type, purpose}` — gauge, current count
- `docker_events_dispatch_seconds{type, action}` — histogram, receive→consumer-visible latency

## Patterns referenced

- **k8s client-go `cache.SharedIndexer`** — the canonical in-memory
  watch-driven store. Don't depend on client-go (heavyweight, k8s
  types), but its shape is the right one: thread-safe indexed map,
  watch stream populates, consumers query + subscribe.
- **containerd metadata store** — uses boltDB but only because
  containerd's state is *authoritative* locally (the kernel view is
  derived from it). CP's state is *derived* from Docker's view, so no
  persistence needed.
- **Envoy xDS** — in-memory, push-driven, re-synced on reconnect. Same
  philosophy as this design.
- **Prometheus `client_golang`** — de facto Go metrics library, one
  registry per process, scrape endpoint. No question.


## Conclusions / Insights

- The sensory-sources framing from the sibling brainstorm was right on
  the first pass. Docker events is sense #1. Phases 5 and 6 add #2 and
  #3. Design this initiative's data model and dispatch to carry sources
  #2 and #3 in the future without rewriting — the worldview is not
  events-shaped, it's just seeded by events today.
- "Polling → events" was the wrong framing for this initiative.
  "Blindness → sight" is the right one. The polling replacements fall
  out of that; they are not the thing being built.
- The CP's authority over the realm is derived from its sight. An
  unsighted CP is a CP in name only. This initiative is the first time
  the CP actually earns its title.

## Gotchas / Risks

- **Backpressure from consumer to moby receive.** The receive goroutine
  must never block on consumer dispatch. Breaks daemon event flow,
  potentially forces daemon-side buffer overflow, drops events to every
  other consumer on the machine. Dispatch via bounded queues or worker
  pool; receive goroutine only appends to queues, never invokes
  consumers directly.
- **256-slot daemon buffer.** Loss is possible across disconnects. Full
  re-list on every reconnect; never trust `Since=`.
- **Docker daemon restart.** Stream terminates; events during downtime
  are lost to us. Full re-list on reconnect is the only recovery.
- **CP's own container is in the event stream.** Label filter will
  match the CP itself. Self-exclusion needs to be explicit either at
  dispatch or at label filter (`purpose != controlplane` — moby filter
  supports inequality via `!=`).
- **Phase-4 attestation hole if we ship events without the
  AnnounceAgent-verification path planned.** If `AnnounceAgent` ships
  later and discovers the worldview API doesn't support "here's a
  pending slot, let me know when the matching `start` arrives", we
  rework the data model. Better to think through that API surface in
  this initiative even if the RPC doesn't land yet.
- **Two sources of truth during migration.** If polling call sites stay
  alive while the worldview comes up, both paths need to agree on what
  "running agent" means. Either cut polling fully in the PR that ships
  events, or gate polling behind a feature flag that defaults off in
  that PR.
- **Anomaly classification is policy work disguised as engineering
  work.** What counts as a ghost container, what response each class
  triggers, what threshold flips "log" to "alert" to "act" — these are
  security and reliability decisions that will grow fangs in phase 4+.
  The data model must be expressive enough to let policy grow.

## Unknowns

- Exact trust-hierarchy rules between Docker events, clawkerd heartbeats,
  and BPF ring buffer when they disagree (phases 5, 6 concern; design
  now so we don't lock ourselves out)
- API surface for consumers. Snapshot + typed deltas? Query interface?
  Event-sourced projections the consumer maintains? All three depending
  on consumer?
- Retention windows per resource type and how consumers declare their
  needs
- Whether the CP ever *acts* (not just observes) in v1 of this
  initiative — e.g., restarts a crashed firewall container on `die`
- Naming and package location

## Next Steps

No correctless workflow. No `/cspec`, no `/ctdd`, no `/cverify`. Forge
direct from this memory + `clawker-pantheon` + the existing `.claude/`
rules that govern package layout, testing, and docs.

When ready to forge:

1. Create package (name TBD — `worldview/` or `dockerevents/` or
   `realm/`; pick at forge time, no bikeshedding needed up front)
2. Types first: `Resource`, `Lifecycle`, `Transition`, `Edges`,
   `AttestationState`, `CLIClaim`, `AgentClaim`, `Filter`, `Predicate`,
   `Delta`, `Worldview`. Declared fields land with zero-value writers
   only (phase 4 wires the writers).
3. In-memory store with `byID`/`byType`/`byPurpose` indexes + bounded
   history ring per resource.
4. Moby `/events` subscription loop with reconnect + backoff + full
   re-list on (re)connect. Unfiltered — dispatch decides relevance.
5. Receive → bounded queue → worker pool → dispatch pipeline.
6. Association extraction: per `events.Action`, which
   `actor.attributes` keys carry resource-ID references. Table-driven.
7. Prometheus metrics registry + `/metrics` endpoint (wire into CP's
   existing HealthPort or new port — see `cmd/clawker-cp/main.go`).
8. Audit logger wired (zerolog file log, structured fields).
9. Startup sequencing in `cmd/clawker-cp/main.go` — worldview comes up
   after Docker client (step 6b) and before the existing `AgentWatcher`
   goroutine (step 9b). Doesn't replace anything in this PR.
10. Test surfaces: unit tests for store/indexes/dispatch/predicates;
    Docker integration tests in `test/e2e/` that drive real containers
    and assert worldview state transitions; mock for the moby events
    stream under `<package>/mocks/` (moq-generated from an
    `EventSource` interface, per existing testing conventions).
11. Package `CLAUDE.md` written alongside the code, not after.
12. Root `CLAUDE.md` + `.claude/docs/KEY-CONCEPTS.md` +
    `.claude/docs/ARCHITECTURE.md` updated before the PR ships.

Follow-up PRs (after this forge):
- Phase 4: `AnnounceAgent` RPC, `CLIClaim` writer, attestation TTL
  sweep goroutine, `pending`→`orphan`/`ghost` transitions.
- Phase 5: Ring-handing. clawkerd in agent containers, PKCE, per-agent
  cert minting, `AgentClaim` writer.
- Consumer migrations (`AgentWatcher`, `Stack.Status`, `reenrollAgents`)
  — incremental; no forced bundling.
