# eBPF Network Event Emitter (netlogger)

**Branch:** `feat/ebpf-logging`
**Status:** Design locked 2026-05-21. Ready for sequential execution.

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: BPF ringbuf + per-decision-point emit + drop counters + kernel rate limiter | `pending` | — |
| Task 2: netlogger subpackage scaffold — ringbuf reader, LabelCache, reverse-DNS, processor (no OTLP) | `pending` | — |
| Task 3: Generic OTel client in `internal/controlplane/` + netlogger OTel sink + CP main wiring + drain hook | `pending` | — |
| Task 4: E2E test + docs | `pending` | — |

## Key Learnings

(Agents append here as they complete tasks.)

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task.
2. Update the Progress Tracker in this memory.
3. Append any key learnings to the Key Learnings section.
4. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
5. Commit all changes from this task with a descriptive commit message.
6. Present the handoff prompt from the task's Wrap Up section to the user.
7. Wait for the user to start a new conversation with the handoff prompt.

Each task is self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### What this initiative ships

Per-decision-point eBPF egress event emitter for clawker. BPF programs already enforce egress at cgroup connect/sendmsg hooks; today they only invoke `metric_inc()` (silent counters). This branch adds:

1. A `BPF_MAP_TYPE_RINGBUF` map (`events_ringbuf`) populated at every decision point with a fixed-size `struct egress_event` carrying `{verdict, dst_ip, dst_port, l4_proto, cgroup_id, domain_hash, ts_ns}`.
2. A BPF-side **token-bucket rate limiter** keyed by `cgroup_id` so a misbehaving container can't monopolize the ringbuf (Cilium pattern — `bpf/lib/ratelimit.h`).
3. A BPF-side **kernel-fault drop counter** (`BPF_MAP_TYPE_PERCPU_ARRAY`, 1 entry) bumped when `bpf_ringbuf_reserve` returns NULL.
4. A new userspace subpackage `internal/controlplane/firewall/ebpf/netlogger/` that drains the ringbuf, enriches each event with `{container_id, agent, project, domain}` looked up by `cgroup_id`, and emits as OTLP log records.
5. A generic OTel-client constructor at `internal/controlplane/otelclient.go` so future subsystems (sysexec events, etc.) construct their own `*sdklog.LoggerProvider` against the existing infra OTel receiver without duplicating the SDK wiring.

### What this initiative explicitly does NOT ship

- **No sock_ops / TCP_CLOSE per-connection byte tracking.** This is by design and matches production practice — see "Why decision-time emit is the right scope" below. The event struct intentionally has NO `bytes_in` / `bytes_out` / `duration_ns` fields. Roundtrip data for ALLOWED connections comes from the L7 layer (Envoy access logs); for BYPASSED connections it is genuinely unrecoverable and operators correlate against other sources by 5-tuple.
- **No Envoy access-log OTLP rewiring.** Separate branch.
- **No CoreDNS `log` plugin → filelog receiver pivot.** Separate branch.
- **No OpenSearch backend migration.** Separate branch.
- **No new mTLS certs minted.** netlogger reuses CP's existing `otelcerts.Service.LoadTLSConfig` (per-handshake ephemeral leaf — same path the CP zerolog→OTLP bridge already uses).
- **No new `service.name`.** Resource attribute stays `service.name=clawker-cp`. Stream is discriminated by OTel **instrumentation scope** name (`clawker.netlogger`) and a record attribute `event.name=ebpf.egress.flow`. This is the user's explicit framing: "CP emitting additional service events, not a new service".

### Why decision-time emit is the right scope (grounded in research)

This is not a deferral — it's how production observability tools work:

- **Cilium Hubble** emits `trace_sock_notify` at `cgroup/connect` and `cgroup/sendmsg` — connect time, **not** TCP_CLOSE. The struct (`bpf/lib/trace_sock.h`) has no byte-count fields. Per-connection bytes/duration are sourced from the **L7 proxy layer** (Envoy ALS records) and correlated downstream by 5-tuple, not from sock_ops STATE_CB.
- **Tetragon** emits at sensor hook points (`pkg/observer/observer_linux.go`) — per-decision, per-syscall. No TCP_CLOSE roundtrip enrichment.
- **The `cilium/ebpf examples/tcprtt_sockops/` reference is a tutorial for RTT measurement, not a production observability pattern.** Nobody ships it as security telemetry.

The "one entry per logical flow" requirement resolves cleanly under this model: the flow record from the operator's point of view is the Envoy ALS log (for L7 flows) or the netlogger BPF record (for DENIED before Envoy and BYPASSED that skips Envoy). Each source emits independently — no ingest-time composer goroutine — and cross-source 5-tuple correlation is a human / dashboard operation at query time.

**Operator workflow**: query `clawker.netlogger` stream for the BPF decision record; pivot by 5-tuple to the Envoy access-log stream for bytes/duration/HTTP-method. For BYPASSED connections, only the netlogger record exists — that's the documented limitation of bypass mode. Fixing the bypass-mode forensic blind-spot (BPF still observes traffic even when Envoy and CoreDNS enforcement is skipped) is the headline win of this branch even without byte data.

### Reference research (read before starting any task)

Every design call in this initiative is grounded in one of these. Don't reinvent — copy the pattern.

| Source | File path (upstream) | What it teaches |
|--------|---------------------|-----------------|
| Cilium | `bpf/lib/policy_log.h:28-44` | Event struct layout: explicit padding, C99 compound literal zero-init, helper-side `bpf_ntohs` byte-swap |
| Cilium | `bpf/lib/ratelimit.h` | Token-bucket rate limiter in `LRU_HASH`, intentionally racy (non-atomic), per-key state, separate metrics map |
| Cilium | `pkg/monitor/agent/agent.go::handleEvents` | Reader goroutine: `defer Close()`, context-cancel loop body, error tolerance |
| Cilium | `pkg/maps/eventsmap/cell.go` | Buffer sizing posture: modest fixed, dial rate limits on drops (don't grow buffer) |
| Tetragon | `pkg/observer/observer_linux.go:64-180` | The full ringbuf consumer pattern — reader goroutine → bounded channel with non-blocking send (drop newest) → processor goroutine. **The reference implementation.** |
| Tetragon | `pkg/cgidmap/cgidmap.go:36-217` | Cgroup→container metadata cache: slice + dual-index maps + `invalid` flag for soft-delete on cgroup-id reuse, single `sync.Mutex` |
| Tetragon | `pkg/observer/metrics.go:27-117` | Five-counter Prom convention: received, errors, kernel-lost, queue-received, queue-lost |
| cilium/ebpf | `examples/ringbuffer/main.go:55-90` | Canonical Go ringbuf reader: `Close()` from signal goroutine → `ErrClosed` on Read |
| cilium/ebpf | `examples/tcprtt_sockops/` (full example) | sock_ops attach via `ebpf.AttachCGroupSockOps`. Reference only — production observability does NOT use sock_ops STATE_CB for byte tracking; tutorial pattern. |
| cilium/ebpf | `ringbuf/reader.go:147` | `ReadInto(*Record)` preferred over `Read()` for hot path (reuses RawSample backing slice) |
| OTel Go SDK | `sdk/log/batch.go:193` (`OnEmit`), `:305` (`queue.Enqueue`) | BatchProcessor ring-buffer drops oldest on overflow — drop-not-block built in |
| OTel Go SDK | `internal/shared/otlp/retry/retry.go.tmpl:22-26` | Default `MaxElapsedTime=1min` retry — **TOO LONG** for our use case; tune to 10s so dead-collector doesn't pin the export goroutine |
| OTel Go SDK | `sdk/log/provider.go:78-129` | Logger scope cache — one `*sdklog.LoggerProvider` per process, `provider.Logger("scope")` returns cached per-scope instance |

### Existing clawker primitives to reuse (do not reinvent)

| Primitive | Path | Why |
|-----------|------|-----|
| `ebpf.Manager` | `internal/controlplane/firewall/ebpf/manager.go` | Already owns BPF lifetime, pinned maps, `OpenPinned()`. We add accessor methods for the new pinned maps. |
| `dockerevents.DockerEvent` bus + `overseer.Overseer` | `internal/controlplane/dockerevents/`, `internal/controlplane/overseer/` | Already publishes container start/die/destroy with full `Actor.Attributes` including `dev.clawker.{project,agent}` labels. **LabelCache subscribes to this bus** for invalidation — we do NOT add a separate Docker-events subscriber. |
| `otelcerts.Service` | `internal/controlplane/otelcerts/otelcerts.go::LoadTLSConfig` | Already in use by CP zerolog→OTLP bridge. `LoadTLSConfig(svc)` returns a `*tls.Config` with `GetClientCertificate` that re-mints per handshake. We call `LoadTLSConfig("netlogger")` — same issuer, same root CA, no new on-disk material. |
| `consts.MonitoringServiceOtelCollector` + `cfg.Settings().Monitoring.OtelInfraPort` | `internal/config/` | OTLP endpoint for trusted-lane infra push. Already routed cross-network. |
| `logger.Logger` | `internal/logger/logger.go` | **Only** for netlogger's own degraded-path structured logs (`event=netlogger_unavailable`, drop-counter periodic summaries). **NEVER** for the network event records themselves — those go direct OTLP via the new `*sdklog.LoggerProvider`. |
| CP no-panic discipline | root `CLAUDE.md` + `internal/controlplane/CLAUDE.md` | Hard rule: no `panic()`, no `log.Fatal()`, no `os.Exit()` from netlogger code path. Constructor returns `(nil, error)`; main degrades to `event=netlogger_unavailable`. Every long-lived goroutine wraps with `defer recover()` (template: overseer stats heartbeat in `cmd/clawker-cp/main.go`). |

### Rules

- Read `CLAUDE.md`, `internal/controlplane/CLAUDE.md`, `internal/controlplane/firewall/CLAUDE.md`, and `internal/controlplane/firewall/ebpf/CLAUDE.md` before starting any task.
- Use Serena tools (`mcp__serena__*`) for code navigation — `find_symbol`, `get_symbols_overview`, `find_referencing_symbols`, `replace_symbol_body`. Do not Read whole files to discover symbols.
- Use `internal/config/mocks/configmocks` for config doubles in tests. Use `internal/controlplane/firewall/ebpf/mocks.EBPFManagerMock` for manager mocking.
- Every BPF map change requires running `make ebpf` to regenerate `clawker_*_bpfel.go` bindings. Generated files are gitignored.
- Per `.claude/rules/testing.md`: do NOT run `go test ./...` inside a clawker container (`$CLAWKER_AGENT` set) — the e2e suite tears down the host CP. Use targeted package tests + `make test`.
- Pre-commit hooks (installed via `scripts/install-hooks.sh`) run unit tests automatically. Don't double-run before commit.
- Tests use real implementations as far as possible (`internal/testenv.New(t)`, etc.). Mock only at external boundaries.

---

## Task 1: BPF — events_ringbuf + per-decision-point emission + drop counters + kernel rate limiter

**Creates/modifies:**
- `internal/controlplane/firewall/ebpf/bpf/common.h` — add maps + `struct egress_event` + `submit_event` helper + token-bucket rate limiter
- `internal/controlplane/firewall/ebpf/bpf/clawker.c` — wire `submit_event` into all 7 cgroup programs at decision points; rework `enter_enforced` to surface bypass state so ACTION_BYPASS records emit
- `internal/controlplane/firewall/ebpf/manager.go` — accessor methods + Load/OpenPinned updates for the new pinned maps
- `internal/controlplane/firewall/ebpf/types.go` — `EgressEvent` Go-side struct (mirrors C ABI), verdict enum constants
- `internal/controlplane/firewall/ebpf/clawker_{x86,arm64}_bpfel.{go,o}` — regenerated by `make ebpf`
- `internal/controlplane/firewall/ebpf/manager_test.go` — synthetic-write tests for the new map accessors
- `internal/controlplane/firewall/ebpf/CLAUDE.md` — document new maps + accessors + the endianness convention

**Depends on:** None. Foundation task.

### Background — what BPF C is doing today

`bpf/clawker.c` has 7 cgroup programs (`connect4`, `sendmsg4`, `recvmsg4`, `connect6`, `sendmsg6`, `recvmsg6`, `sock_create`). Every decision point currently calls `metric_inc(cgroup_id, hash, port, action)` (defined in `common.h`) which writes into a hashmap with action ∈ `{ACTION_ALLOW, ACTION_DENY}`. There is no userspace consumer of `metrics_map` today except the break-glass `cmd/ebpf-manager` CLI.

`enter_enforced(...)` (in `common.h`) is the per-syscall preamble. It checks the bypass flag and **short-circuits with `return 1` (allow)** when bypass is set. This means today there is NO record of bypass-mode traffic — the "forensic black hole" this branch closes. We need `enter_enforced` to surface "bypassed" as a signal callers can act on, not silently allow.

### Design — grounded in research

**Map additions** (all in `common.h`, all pinned by name):

```c
// 1) Event channel — modest fixed size, dial rate limits on drops.
//    Buffer must be power-of-2 page-size multiple (cilium/ebpf NewReader rejects otherwise).
//    Start at 256 KiB = 64 pages × 4 KiB.  Tunable but ratchet up only after observing drops.
//    Rationale: Cilium pkg/maps/eventsmap/cell.go uses kernel-default per-CPU sizing;
//    we use a single ringbuf because we only have one userspace reader and the records
//    are tiny (~32 bytes).
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 256 * 1024);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} events_ringbuf SEC(".maps");

// 2) Kernel-fault drop counter — bumped when bpf_ringbuf_reserve returns NULL.
//    PERCPU_ARRAY avoids the contention metric_inc has on its global HASH.
//    Userspace sums across CPUs via *ebpf.Map.Lookup(uint32(0), &perCPU).
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __type(key, __u32);
    __type(value, __u64);
    __uint(max_entries, 1);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} events_drops SEC(".maps");

// 3) Rate-limit state — token bucket per cgroup_id (Cilium bpf/lib/ratelimit.h pattern).
//    LRU_HASH so dead cgroups evict naturally.  Per-cgroup (not per-usage like Cilium)
//    because that matches the granularity of "noisy agent" we want to throttle.
//    Intentionally non-atomic: a small amount of bucket inaccuracy under racing CPUs is
//    cheaper than the cmpxchg cost on the hot path.
struct ratelimit_state {
    __u64 last_topup_ns;
    __u64 tokens;
};
struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
    __type(key, __u64);                   // cgroup_id
    __type(value, struct ratelimit_state);
    __uint(max_entries, 1024);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} ratelimit_state SEC(".maps");

// 4) Rate-limit drop counter — distinct from kernel-fault drops because the cause
//    (intentional vs ringbuf overflow) demands different operator response.
//    Cilium splits this exact way (cilium_bpf_ratelimit_dropped_total vs
//    cilium_lost_events_total).  Key is cgroup_id so userspace can attribute.
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);                   // cgroup_id
    __type(value, __u64);
    __uint(max_entries, 256);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} ratelimit_drops SEC(".maps");
```

**Event struct** (in `common.h`, fixed-size, explicit padding):

```c
// Endianness convention: ALL fields stored in HOST byte order.
// Callers MUST bpf_ntohs(port) / bpf_ntohl(ip) at the emit site if their input is
// network-order.  ctx->user_ip4 is already network-byte-order so it passes through
// unmodified (matches ContainerConfig.EnvoyIp etc. in this codebase).  dst_port from
// ctx->user_port IS network-order — caller swaps to host before passing.
// We pick caller-swap (not helper-swap) because the helper is shared across call
// sites that have already swapped for other reasons; doubling the swap is worse than
// requiring callers to be explicit.  Cilium policy_verdict_notify uses helper-swap
// and trace_sock_notify uses caller-swap — they ended up inconsistent.  We won't.
enum egress_verdict {
    EGRESS_VERDICT_ALLOWED  = 0,
    EGRESS_VERDICT_DENIED   = 1,
    EGRESS_VERDICT_BYPASSED = 2,
};

enum egress_flags {
    EGRESS_FLAG_IPV6        = 1 << 0,   // native IPv6 (not IPv4-mapped)
    EGRESS_FLAG_IPV4_MAPPED = 1 << 1,   // ::ffff:x.x.x.x
};

// 32 bytes.  All padding explicit and zero-initialized via compound literal.
struct egress_event {
    __u64 ts_ns;          // bpf_ktime_get_ns()
    __u64 cgroup_id;      // trust anchor — userspace cache key
    __u32 domain_hash;    // 0 if no DNS resolution (direct-IP connect or no cache hit)
    __u32 dst_ip;         // network byte order (matches ctx->user_ip4)
    __u16 dst_port;       // host byte order (caller swapped)
    __u8  verdict;        // enum egress_verdict
    __u8  flags;          // enum egress_flags bitmask
    __u8  l4_proto;       // SOCK_STREAM / SOCK_DGRAM / SOCK_RAW
    __u8  _pad[3];        // explicit padding — Cilium policy_log.h pattern
};
```

**Submit helper** (in `common.h`):

```c
// Returns 1 if event was submitted (or intentionally rate-limited), 0 never.
// The return value exists so the call site can chain it with the routing return
// without an extra local.
static __always_inline int
submit_event(__u64 cgroup_id, __u32 dst_ip, __u16 dst_port_host,
             __u8 l4_proto, __u8 verdict, __u8 flags)
{
    if (!ratelimit_check_and_take(cgroup_id)) {
        // Intentional drop — bump the rate-limit counter keyed by cgroup_id.
        // ratelimit_check_and_take handles the bump internally.
        return 1;
    }
    struct egress_event *ev = bpf_ringbuf_reserve(&events_ringbuf, sizeof(*ev), 0);
    if (!ev) {
        // Kernel-fault drop — buffer full.  Bump PERCPU counter.
        __u32 zero = 0;
        __u64 *cnt = bpf_map_lookup_elem(&events_drops, &zero);
        if (cnt) __sync_fetch_and_add(cnt, 1);
        return 1;
    }
    *ev = (struct egress_event){
        .ts_ns       = bpf_ktime_get_ns(),
        .cgroup_id   = cgroup_id,
        .domain_hash = lookup_domain_hash_for_ip(dst_ip),
        .dst_ip      = dst_ip,
        .dst_port    = dst_port_host,
        .verdict     = verdict,
        .flags       = flags,
        .l4_proto    = l4_proto,
    };
    bpf_ringbuf_submit(ev, 0);
    return 1;
}

// Token-bucket per cgroup_id.  Bucket size 64, refill 64 tokens / 100ms.
// Tunables live as constants here (no userspace config in v1).
#define RATELIMIT_BURST       64ULL
#define RATELIMIT_REFILL_NS   100000000ULL   // 100 ms
#define RATELIMIT_TOKENS_PER  64ULL

static __always_inline bool
ratelimit_check_and_take(__u64 cgroup_id)
{
    __u64 now = bpf_ktime_get_ns();
    struct ratelimit_state *st = bpf_map_lookup_elem(&ratelimit_state, &cgroup_id);
    if (!st) {
        struct ratelimit_state fresh = { .last_topup_ns = now, .tokens = RATELIMIT_BURST - 1 };
        bpf_map_update_elem(&ratelimit_state, &cgroup_id, &fresh, BPF_NOEXIST);
        return true;
    }
    // Racy refill — Cilium does the same (bpf/lib/ratelimit.h).
    if (now - st->last_topup_ns >= RATELIMIT_REFILL_NS) {
        __u64 add = RATELIMIT_TOKENS_PER;
        if (st->tokens + add > RATELIMIT_BURST) add = RATELIMIT_BURST - st->tokens;
        st->tokens += add;
        st->last_topup_ns = now;
    }
    if (st->tokens == 0) {
        __u64 *drops = bpf_map_lookup_elem(&ratelimit_drops, &cgroup_id);
        if (drops) { __sync_fetch_and_add(drops, 1); }
        else { __u64 one = 1; bpf_map_update_elem(&ratelimit_drops, &cgroup_id, &one, BPF_ANY); }
        return false;
    }
    st->tokens--;
    return true;
}
```

**Reworking `enter_enforced`**: today this function returns nonzero (truthy) when the call site should short-circuit allow. We need it to also tell the caller "bypass is active so emit an `EGRESS_VERDICT_BYPASSED` event before allowing":

```c
// Returns:
//   ENTER_NOT_MANAGED — container not in container_map, do nothing (caller returns 1)
//   ENTER_BYPASSED    — container is bypassed; CALLER must submit_event(BYPASSED) then allow
//   ENTER_ENFORCED    — proceed with normal routing decision
enum enter_state { ENTER_NOT_MANAGED, ENTER_BYPASSED, ENTER_ENFORCED };
```

Then in each program (e.g. `clawker_connect4`):

```c
struct container_config *cfg;
__u64 cgroup_id;
enum enter_state st = enter_enforced(&cfg, &cgroup_id, true);
if (st == ENTER_NOT_MANAGED) return 1;
if (st == ENTER_BYPASSED) {
    submit_event(cgroup_id, ctx->user_ip4, bpf_ntohs(ctx->user_port),
                 ctx->type, EGRESS_VERDICT_BYPASSED, 0);
    return 1;
}
struct route_result r = decide_connect(ctx, cfg, cgroup_id, ctx->user_ip4, bpf_ntohs(ctx->user_port));
__u8 verdict = (r.verdict == V_DENY) ? EGRESS_VERDICT_DENIED : EGRESS_VERDICT_ALLOWED;
submit_event(cgroup_id, ctx->user_ip4, bpf_ntohs(ctx->user_port),
             ctx->type, verdict, 0);
return apply_v4(ctx, r);
```

Mirror for `connect6`, `sendmsg4`, `sendmsg6`, `sock_create` (sock_create has no port — pass 0). `recvmsg4`/`recvmsg6` are response-side and do not emit events on this branch (they don't represent egress decisions).

**`lookup_domain_hash_for_ip(dst_ip)`** — uses the existing pinned `dns_cache` (key=IPv4, value={domain_hash, expire_ts}). Cilium's `trace_sock_notify` does the analogous lookup. Returns 0 if not present (direct-IP connect). Cilium's `dns_cache` GC already evicts stale entries — no extra work needed.

**Manager.go accessors**:

```go
func (m *Manager) EventsRingbuf() *ebpf.Map      // returns m.objs.EventsRingbuf
func (m *Manager) EventsDrops() *ebpf.Map         // returns m.objs.EventsDrops
func (m *Manager) RatelimitDrops() *ebpf.Map      // returns m.objs.RatelimitDrops
func (m *Manager) DNSCache() *ebpf.Map            // returns m.objs.DnsCache (already exists, just expose)
```

These are read-only access — netlogger never writes to these maps. Returning the raw `*ebpf.Map` is the same pattern `LookupContainer` uses internally and what cilium/ebpf documents.

Update `Load()` and `OpenPinned()` to include the new maps. Update `FlushAll()` to walk `ratelimit_state` and `ratelimit_drops` and drain them (rate-limit state is per-cgroup; an agent restart should start with a fresh bucket).

### Implementation steps

1. Add the four new maps + `struct egress_event` + verdict/flags enums + `submit_event` helper + `ratelimit_check_and_take` to `bpf/common.h`. Compile-fail loud — use `_Static_assert(sizeof(struct egress_event) == 32, "...")`.
2. Replace `enter_enforced` return type with `enum enter_state` per the design above. Update every call site in `clawker.c`. Confirm `enter_enforced(&cfg, &cgroup_id, false)` (the `lookup_only=false` recvmsg path) still returns NOT_MANAGED on bypass — bypass doesn't apply to response-side rewrites.
3. Add `submit_event` calls at every decision point in `clawker.c`. For `connect6`/`sendmsg6`, pass the IPv4-mapped low word as `dst_ip` and set `EGRESS_FLAG_IPV4_MAPPED`. For native IPv6 (already DENY-only today), pass `dst_ip=0` and set `EGRESS_FLAG_IPV6`. For `sock_create`, pass `dst_ip=0, dst_port=0`.
4. Run `make ebpf` to regenerate `clawker_*_bpfel.{go,o}`. Confirm the bpf2go-generated Go struct for `clawkerEgressEvent` has `structs.HostLayout` and matches the C layout byte-for-byte.
5. Add Manager accessor methods in `manager.go`. Add the new maps to the `OpenPinned` map name list and the schema-detection loop in `Load`.
6. Extend `FlushAll` to drain `ratelimit_state` and `ratelimit_drops` (iterate keys, delete). Match the existing `container_map` / `bypass_map` drain pattern in the same file.
7. Add `EgressEvent` Go-side struct in `types.go` aliased to the bpf2go type, plus exported verdict/flag constants. Mirror `BPFContainerConfig = clawkerContainerConfig` pattern.
8. Manager test additions: synthetic-write to `events_ringbuf` via `*ebpf.Map.Update` is not how ringbuf works — instead test (a) that `Load()` successfully pins all four new maps, (b) that accessor methods return non-nil after `Load`, (c) that `FlushAll` clears `ratelimit_state` + `ratelimit_drops`. Real ringbuf event verification belongs in Task 4 E2E.
9. Update `internal/controlplane/firewall/ebpf/CLAUDE.md`: add the four maps to the pinned-maps table, document the endianness convention, document `enter_state` enum, note the new accessor methods.

### Acceptance Criteria

```bash
# BPF compiles + bpf2go regen succeeds
make ebpf
test -f internal/controlplane/firewall/ebpf/clawker_x86_bpfel.o
test -f internal/controlplane/firewall/ebpf/clawker_arm64_bpfel.o

# Manager test passes
go test ./internal/controlplane/firewall/ebpf -v -run TestManager

# Full firewall package tests still pass (we didn't break existing behavior)
go test ./internal/controlplane/firewall/... -v -count=1

# Sanity: the bpf2go-generated egress_event Go struct exists with HostLayout
grep -q "structs.HostLayout" internal/controlplane/firewall/ebpf/clawker_x86_bpfel.go
grep -q "EgressEvent\|egressEvent" internal/controlplane/firewall/ebpf/clawker_x86_bpfel.go

# bpf-side struct size assertion compiles (verifies layout discipline)
grep -q "_Static_assert.*egress_event.*32" internal/controlplane/firewall/ebpf/bpf/common.h
```

### Wrap Up

1. Update Progress Tracker: Task 1 → `complete`.
2. Append key learnings — particularly any verifier complaints encountered, the kernel version `make ebpf` ran against, and any tuning of `RATELIMIT_BURST` / `RATELIMIT_REFILL_NS` you made.
3. Run completion gate: `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer`. Fix every finding.
4. Commit. Conventional message: `feat(ebpf): events_ringbuf + per-decision-point egress event emission`.
5. **STOP.** Do not begin Task 2. Present this handoff to the user:

> **Next agent prompt:** "Continue the eBPF network event emitter initiative. Read the Serena memory `initiative_ebpf-netlogger` — Task 1 is complete. Begin Task 2: netlogger subpackage scaffold."

---

## Task 2: netlogger subpackage — ringbuf reader + LabelCache + reverse DNS + processor (no OTLP)

**Creates:**
- `internal/controlplane/firewall/ebpf/netlogger/netlogger.go` — top-level `Service` struct, `New(Deps)`, `Start(ctx)`, `Stop(ctx) error`
- `internal/controlplane/firewall/ebpf/netlogger/event.go` — `Event` struct (enriched), parser using bpf2go type + `binary.NativeEndian`
- `internal/controlplane/firewall/ebpf/netlogger/cache.go` — `LabelCache` cgroup_id → `{container_id, agent, project}`, Tetragon `cgidmap` pattern
- `internal/controlplane/firewall/ebpf/netlogger/reverse_dns.go` — periodic `dns_cache` scan, `domain_hash → domain` map under RWMutex
- `internal/controlplane/firewall/ebpf/netlogger/reader.go` — ringbuf drain goroutine
- `internal/controlplane/firewall/ebpf/netlogger/processor.go` — channel consumer goroutine, enriches + emits via `Sink`
- `internal/controlplane/firewall/ebpf/netlogger/sink.go` — `Sink` interface, `nopSink` (for tests), `stdoutSink` (for Task-2 acceptance — replaced by OTel sink in Task 3)
- `internal/controlplane/firewall/ebpf/netlogger/metrics.go` — Prom counters declared but not registered yet (Task 3 wires registration)
- `internal/controlplane/firewall/ebpf/netlogger/CLAUDE.md` — package reference doc
- Test files for each of the above

**Depends on:** Task 1 (Manager accessors must exist).

### Background — the Tetragon pattern we're copying

Tetragon's `pkg/observer/observer_linux.go:64-180` is the reference implementation. Architecture:

```
        ┌─────────────────────┐
        │  events_ringbuf     │  (kernel)
        └──────────┬──────────┘
                   │
                   ▼
        ┌─────────────────────┐
        │  reader goroutine   │  blocking ReadInto, defer Close → ErrClosed shutdown
        │  reader.go::drain   │  bumps RingbufReceived / RingbufErrors
        └──────────┬──────────┘
                   │  non-blocking send, default→drop newest, bumps QueueLost
                   ▼
        ┌─────────────────────┐
        │  queue chan []byte  │  buffered, size 8192 (Tetragon default 65535 — we're smaller scale)
        └──────────┬──────────┘
                   │
                   ▼
        ┌─────────────────────┐
        │  processor goroutine│  drains queue, parses + enriches + emits via Sink
        │  processor.go::run  │  bumps QueueReceived / ParseErrors
        └─────────────────────┘
```

**Critical**: the kernel reader MUST NOT block on the channel. A blocked reader stalls the ringbuf which causes upstream `bpf_ringbuf_reserve` failures (counted as kernel-fault drops by Task 1's `events_drops`). Drop-newest on channel-full is the documented Tetragon posture (`pkg/observer/observer_linux.go:122-129`).

### Design

**`Sink` interface** (in `sink.go`):

```go
// Sink consumes enriched egress events.  Implementations MUST NOT block —
// the processor goroutine is single-threaded and blocking sink slows the
// drain pipeline.  OTel SDK BatchProcessor satisfies this contract by design
// (OnEmit returns immediately, batches in a goroutine).
type Sink interface {
    Emit(ctx context.Context, ev Event)
}

// nopSink discards all events.  Used by tests that don't care about emission.
type nopSink struct{}
func (nopSink) Emit(context.Context, Event) {}

// stdoutSink writes JSON-per-line to an io.Writer.  Used as Task 2's acceptance
// sink so the pipeline is end-to-end testable before Task 3 wires OTel.
// REPLACED in Task 3 by otelSink; do not ship to production with stdoutSink.
type stdoutSink struct { w io.Writer; mu sync.Mutex }
```

**`Event` struct** (in `event.go`):

```go
// Event is the enriched record the processor emits to a Sink.  Lifetime is
// per-Emit; the processor reuses the underlying byte slice for the next
// record, so sinks MUST copy any fields they retain.
type Event struct {
    Timestamp time.Time

    // Trust-anchored kernel attribution.
    CgroupID uint64

    // Userspace-enriched container identity (empty if cgroup_id miss).
    ContainerID string
    Agent       string  // dev.clawker.agent label
    Project     string  // dev.clawker.project label (empty for global-scope agents)

    // Network 4-tuple (dst only — src is the container's own IP, redundant).
    DstIP    netip.Addr
    DstPort  uint16
    L4Proto  uint8   // SOCK_STREAM / SOCK_DGRAM / SOCK_RAW
    IsIPv6   bool    // native IPv6
    IsMapped bool    // IPv4-mapped over IPv6

    // Domain attribution (empty if direct-IP connect or no DNS cache hit).
    DomainHash uint32
    Domain     string

    Verdict Verdict  // Allowed / Denied / Bypassed
}

type Verdict uint8
const (
    VerdictAllowed  Verdict = 0
    VerdictDenied   Verdict = 1
    VerdictBypassed Verdict = 2
)
```

Parser uses the bpf2go-generated `clawkerEgressEvent` type and `binary.NativeEndian` per cilium/ebpf's `tcprtt_sockops` example. The bpf2go-generated struct has `structs.HostLayout` so the field offsets match the C ABI exactly. Do NOT define a parallel Go struct.

**`LabelCache`** (in `cache.go`) — mirror Tetragon's `cgidmap` pattern:

```go
// LabelCache resolves cgroup_id to container identity.  Backed by a slice +
// dual-index maps + invalid flag (Tetragon pkg/cgidmap/cgidmap.go pattern),
// guarded by a single mutex.  Invalidation is event-driven via dockerevents.
//
// Why not sync.Map: we need atomic "evict by cgroup_id AND container_id" on
// die/destroy, which requires the dual-index lookup under a single mutex.
//
// Why not LRU: cgroup IDs are reused by the kernel when a cgroup is destroyed
// and a new one is created in its slot.  We rely on docker die/destroy events
// to mark entries invalid before the kernel reuses the ID.  An LRU would risk
// returning stale labels for a reused cgroup_id whose old entry hadn't aged out.
type LabelCache struct {
    mu       sync.Mutex
    entries  []labelEntry
    free     []int               // recycled slots
    byCgroup map[uint64]int      // cgroup_id -> entries idx
    byCont   map[string]int      // container_id -> entries idx
    log      *logger.Logger
}

type labelEntry struct {
    cgroupID    uint64
    containerID string
    agent       string
    project     string
    invalid     bool
}

// Lookup returns the labels for a cgroup_id or zero-value+false on miss.
// Safe under load — single mutex hold per call.
func (c *LabelCache) Lookup(cgroupID uint64) (containerID, agent, project string, ok bool)

// AddOrUpdate is called when a container's start event arrives on the
// dockerevents bus.  The caller resolves cgroup_id from the container ID
// via firewall.EBPFCgroupPath + ebpf.CgroupID before invoking.
func (c *LabelCache) AddOrUpdate(cgroupID uint64, containerID, agent, project string)

// EvictByContainerID is called when a container's die/destroy event arrives.
// Marks the entry invalid (so cgroup-id-reuse can't read stale labels) and
// frees the slot for recycle.  Tetragon's cgidmap pattern.
func (c *LabelCache) EvictByContainerID(containerID string)
```

**Wiring to dockerevents bus** — `Service.Start` subscribes the LabelCache to the bus via the `overseer.Overseer` interface. The handler dispatches on `DockerEvent.Type == ContainerEventType` and `Action`:
- `ActionStart` / `ActionRestart`: resolve cgroup_id, call `AddOrUpdate` with `Actor.Attributes["dev.clawker.agent"]` + `["dev.clawker.project"]`. Global-scope agents have project="" — pass through as empty. Use the raw Docker label values verbatim; do NOT synthesize from cgroup name or `AgentFullName`, because the downstream dashboard variable resolution (Prom-sourced) keys on the same raw label strings and any drift means panels go blank.
- `ActionDie` / `ActionStop` / `ActionDestroy`: call `EvictByContainerID(Actor.ID)`.

**Startup backfill**: `Service.Start` walks `manager.DumpContainers()` (already exists in Task 1 dependency — it returns `[]ContainerEntry{CgroupID, Config}`) and seeds the cache by Docker inspecting each container_id for labels. This handles the case where containers existed before netlogger started (CP restart, etc.).

**`ReverseDNSMap`** (in `reverse_dns.go`):

```go
// ReverseDNSMap is the userspace mirror of the pinned dns_cache map, inverted
// from "IP -> domain_hash" to "domain_hash -> domain".  Periodically refreshed
// by walking the pinned map and reading entries.  RWMutex because lookups are
// hot-path (every event) but rebuilds are infrequent (every 5s).
type ReverseDNSMap struct {
    mu     sync.RWMutex
    byHash map[uint32]string
    log    *logger.Logger
}

// Lookup returns "" for hash=0 or no entry (direct-IP connect).
func (m *ReverseDNSMap) Lookup(hash uint32) string

// refresh walks the dns_cache map and rebuilds byHash.  Caller arranges the
// ticker; this method does one pass.  Errors are logged at Debug — the cache
// will retry on next tick.
func (m *ReverseDNSMap) refresh(ctx context.Context, dnsCache *ebpf.Map)
```

**Reverse-mapping caveat**: `dns_cache` keys by IP, values are `{domain_hash, expire_ts}`. We only have the HASH from `dns_cache` values, not the original domain string — domains live in CoreDNS's process memory. For v1, accept this limitation: when an enriched Event has DomainHash != 0 but no matching domain in our reverse-map (because we never observed the hash→domain mapping), emit `Domain=""` and let the operator filter on DomainHash. The dnsbpf plugin already populates `dns_cache` with hashes; a follow-up branch can add a domain-string population path (e.g. a separate domains map keyed by hash). Document this in `CLAUDE.md`.

**Reader goroutine** (in `reader.go`):

```go
// drain reads ringbuf records and forwards raw bytes to the queue channel.
// Single goroutine — ringbuf.Reader serializes internally and concurrent
// readers would just contend on Reader.mu.
// Shutdown: caller closes the Reader via Stop(); ReadInto returns ErrClosed.
func (r *reader) drain(ctx context.Context) {
    defer func() {
        if rec := recover(); rec != nil {
            r.log.Error().Interface("panic", rec).
                Str("event", "netlogger_reader_panic").
                Msg("netlogger ringbuf reader panicked — netlogger will be unavailable")
        }
    }()
    var rec ringbuf.Record
    for {
        if err := r.rb.ReadInto(&rec); err != nil {
            if errors.Is(err, ringbuf.ErrClosed) { return }
            r.metrics.ringbufErrors.Inc()
            r.log.Warn().Err(err).Str("event", "netlogger_ringbuf_error").Msg("ringbuf read error")
            continue
        }
        r.metrics.ringbufReceived.Inc()
        // MUST COPY — rec.RawSample is reused on next ReadInto.
        // Allocation per record is acceptable: events are ~32 bytes,
        // Go's small-object allocator handles this well.  Tetragon does
        // the same (observer_linux.go:122 copies record.RawSample into a
        // new slice before channel send).
        buf := make([]byte, len(rec.RawSample))
        copy(buf, rec.RawSample)
        select {
        case r.queue <- buf:
        default:
            r.metrics.queueDropped.Inc()
        }
    }
}
```

**Processor goroutine** (in `processor.go`):

```go
func (p *processor) run(ctx context.Context) {
    defer func() {
        if rec := recover(); rec != nil {
            p.log.Error().Interface("panic", rec).
                Str("event", "netlogger_processor_panic").
                Msg("netlogger processor panicked — netlogger will be unavailable")
        }
    }()
    for {
        select {
        case <-ctx.Done():
            return
        case raw, ok := <-p.queue:
            if !ok { return }
            p.metrics.queueReceived.Inc()
            ev, err := parseEvent(raw, p.cache, p.revDNS)
            if err != nil {
                p.metrics.parseErrors.Inc()
                p.log.Debug().Err(err).Msg("parse egress event")
                continue
            }
            p.sink.Emit(ctx, ev)
            p.metrics.emitSucceeded.Inc()
        }
    }
}
```

**Service.Stop semantics**:
1. `r.rb.Close()` — interrupts the reader's blocking `ReadInto` with `ErrClosed`. Reader goroutine returns.
2. `close(s.queue)` — processor's range-over-channel terminates after draining remaining queued events.
3. Wait on both goroutines via WaitGroup with a timeout (5s — beyond that we proceed with shutdown to honor INV-B2-007 drain ordering, processor leakage is acceptable on timeout because the OS will reap on CP container exit).
4. Drop the reverse-DNS ticker (no special teardown — it watches `ctx.Done()`).
5. Drop the dockerevents subscription (overseer's Subscribe returns an unsubscribe func — call it).

### Implementation steps

1. Create `internal/controlplane/firewall/ebpf/netlogger/` directory. Add `CLAUDE.md` skeleton (sections to be filled as files land).
2. Implement `Event` + parser in `event.go`. Reference the bpf2go-generated `clawkerEgressEvent` type from the parent `ebpf` package directly. Test: feed synthetic byte slices (constructed via `binary.Write` against the same struct definition) and assert all fields decode correctly. Verify endianness with a fixed IPv4 example.
3. Implement `LabelCache` in `cache.go` using slice + dual-index + invalid pattern. Unit test concurrent `AddOrUpdate` + `Lookup` + `EvictByContainerID` under race detector (`go test -race`). Specifically test the cgroup-id reuse scenario: AddOrUpdate(cgID=42, contA), EvictByContainerID(contA), AddOrUpdate(cgID=42, contB), Lookup(cgID=42) returns contB labels.
4. Implement `ReverseDNSMap` in `reverse_dns.go`. Test with a fake `*ebpf.Map` (use a test-only `Iterable` interface so tests don't need a real BPF map) or via the existing `ebpf.Map` test seam in `manager_test.go`.
5. Implement reader + processor + Sink interface + nopSink + stdoutSink. Tests use the nopSink to drive the pipeline; stdoutSink test feeds a synthetic event, decodes the JSON output, asserts fields.
6. Top-level `Service` in `netlogger.go`:
   - `Deps` struct: `Mgr *ebpf.Manager`, `Bus *overseer.Overseer`, `Docker docker.Client` (for backfill), `Log *logger.Logger`, `Sink Sink`. Future: `OtelLoggerProvider *sdklog.LoggerProvider` (Task 3 adds).
   - `New(Deps) (*Service, error)`: validate required deps, construct LabelCache, ReverseDNSMap, reader, processor. No goroutines started.
   - `Start(ctx context.Context) error`: subscribe to bus, backfill cache, start reader + processor + reverse-DNS ticker. Return nil; degraded paths inside the goroutines per CP no-panic.
   - `Stop(ctx context.Context) error`: drain per the order above.
7. Test the full pipeline end-to-end using a real `ebpf.Map` via the bpf2go test helpers (load the BPF spec in a test, write synthetic events to events_ringbuf via... actually no, ringbuf is kernel-write only from userspace tests. Use a different approach: bypass the ringbuf, call `processor.handle(rawBytes)` directly. Pipeline E2E lands in Task 4.)
8. Write `CLAUDE.md` for the package covering: architecture diagram (reader → channel → processor → sink), the five drop counter dimensions, the Tetragon cgidmap pattern reuse, the OTel-deferred-to-Task-3 note.

### Acceptance Criteria

```bash
# Package compiles
go build ./internal/controlplane/firewall/ebpf/netlogger/...

# Unit tests pass under race detector
go test -race ./internal/controlplane/firewall/ebpf/netlogger/... -v -count=1

# Specifically: LabelCache cgroup-id reuse test passes
go test ./internal/controlplane/firewall/ebpf/netlogger -v -run TestLabelCache_CgroupIDReuse

# stdoutSink end-to-end pipeline test passes (no real BPF needed)
go test ./internal/controlplane/firewall/ebpf/netlogger -v -run TestPipeline_StdoutSink

# No imports of `internal/logger` for emitting Event records (only for degraded-path)
! grep -rE "log\.Info\(\)\.Str.*verdict|log\.Info\(\)\.Str.*dst_ip" internal/controlplane/firewall/ebpf/netlogger/

# No direct otlploggrpc import in netlogger yet (Task 3 brings it in via the generic client)
! grep -r "otlploggrpc" internal/controlplane/firewall/ebpf/netlogger/

# CP main wiring not yet touched
! grep -q "netlogger" cmd/clawker-cp/main.go
```

### Wrap Up

1. Update Progress Tracker: Task 2 → `complete`.
2. Append key learnings — channel buffer size you chose and why, any flakiness observed in race tests, parse-error edge cases discovered.
3. Run completion gate: `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer`.
4. Commit: `feat(netlogger): ringbuf reader + label cache + reverse-DNS + processor`.
5. **STOP.** Handoff:

> **Next agent prompt:** "Continue the eBPF network event emitter initiative. Read the Serena memory `initiative_ebpf-netlogger` — Tasks 1 and 2 are complete. Begin Task 3: Generic OTel client + netlogger OTel sink + CP main wiring."

---

## Task 3: Generic OTel client + netlogger OTel sink + CP main wiring + drain hook

**Creates/modifies:**
- `internal/controlplane/otelclient.go` — new file, generic `NewOtelLoggerProvider(opts) (*sdklog.LoggerProvider, error)`
- `internal/controlplane/otelclient_test.go` — unit tests for the generic constructor
- `internal/controlplane/firewall/ebpf/netlogger/otel_sink.go` — new file, `otelSink` implementing `Sink` using a `otellog.Logger` from a shared provider
- `internal/controlplane/firewall/ebpf/netlogger/otel_sink_test.go` — uses an in-process `sdklog.Exporter` test double to assert record shape
- `internal/controlplane/firewall/ebpf/netlogger/netlogger.go` — `Deps` gains `OtelLoggerProvider *sdklog.LoggerProvider`
- `cmd/clawker-cp/main.go` — boot wiring + degraded path + drain hook
- `internal/controlplane/firewall/ebpf/netlogger/metrics.go` — register Prom counters (now that we have a Sink that can error)
- `internal/controlplane/CLAUDE.md` — document `otelclient.go` and new netlogger wiring
- `internal/controlplane/firewall/ebpf/netlogger/CLAUDE.md` — add OTel sink section

**Depends on:** Tasks 1 and 2.

### Background — what the OTel SDK gives us for free

From research (`sdk/log/batch.go:193,305`):

- `BatchProcessor.OnEmit` is non-blocking. Records go into a ring buffer. **On overflow, the oldest record is dropped and an internal counter increments**. We do not need to implement drop-on-overflow ourselves.
- The internal drop counter is NOT exposed as a stable metric. The pattern from the OTel-SDK research is to **wrap the `sdklog.Exporter`** and count `Export()` calls (success vs error) into Prom counters of our own.
- `otlploggrpc.New(WithRetry(...))` defaults to `MaxElapsedTime=1min`. For a dead infra collector this means each batch sits in the exporter for ~1min before failing — refilling the 2048-slot queue dozens of times in that window. **We set `MaxElapsedTime=10s`** so the exporter fails fast and the queue drop telemetry reflects reality.
- `otel.SetErrorHandler` is process-global. Wire it once in `otelclient.go` to route SDK-internal errors (export failures, retry exhaustion, parse failures inside the SDK) through `logger.Logger.Warn` with `event=otel_sdk_error`. Operators grep the same surface they already use for CP degradation.

### Design — `internal/controlplane/otelclient.go`

```go
// Package controlplane — new sibling file otelclient.go.
//
// NewOtelLoggerProvider constructs a *sdklog.LoggerProvider configured to push
// OTLP log records to clawker's infra OTel receiver over mTLS.  It centralizes
// the SDK wiring so future subsystems (netlogger today, sysexec events
// tomorrow, anything else) construct their own provider with their own
// tuning without duplicating the otlploggrpc setup, the retry policy, or
// the error handler wiring.
//
// One provider per process is the typical case (resource attributes are
// process-scoped); callers obtain per-subsystem instrumentation scopes via
// provider.Logger(scopeName).  Constructing multiple providers in the same
// process is supported but the global otel.SetErrorHandler is shared.
//
// Trust-lane note: TLSConfig MUST be sourced from otelcerts.Service.LoadTLSConfig
// so cert rotation is honored per-handshake.  Endpoint MUST be the infra
// receiver (OtelInfraPort), not the agent-lane receiver on OtelCollectorPort.
// Per the trust-lane-separation feedback in MEMORY.md, infra services must
// never cross into the untrusted agent lane.
type OtelClientOptions struct {
    // Endpoint is "host:port" — bare, no scheme.  e.g. "clawker-otelcol:4319".
    Endpoint string

    // TLSConfig is sourced from otelcerts.Service.LoadTLSConfig("<svc>").
    // Required — the infra lane mandates mTLS.  Insecure is intentionally
    // not supported here (callers wanting insecure should not use this helper).
    TLSConfig *tls.Config

    // ServiceName is the OTel resource attribute service.name.  For the CP-side
    // emitters that share this binary, use "clawker-cp" — the streams are
    // discriminated by instrumentation scope (provider.Logger("netlogger") etc.),
    // not by service.name.
    ServiceName string

    // MaxQueueSize controls the BatchProcessor ring buffer.  Default 2048
    // (the SDK default).  Tune up for high-volume sources after measuring drops.
    MaxQueueSize int

    // ExportInterval is the BatchProcessor flush interval.  Default 1s.
    ExportInterval time.Duration

    // ExportTimeout is the per-export deadline.  Default 30s.
    ExportTimeout time.Duration

    // RetryMaxElapsedTime caps the otlploggrpc retry loop.  Default 10s
    // (vs SDK default 1m) so a dead collector fails fast and the queue
    // drop counter reflects reality.  Set to zero to disable retry.
    RetryMaxElapsedTime time.Duration

    // Log is used for the otel.SetErrorHandler wiring.  Required.
    Log *logger.Logger

    // ExporterWrap is an optional decorator for the underlying sdklog.Exporter.
    // The standard use case is wrapping with a counting exporter for Prom
    // metrics — see netlogger's metrics.go for the pattern.
    ExporterWrap func(sdklog.Exporter) sdklog.Exporter
}

func NewOtelLoggerProvider(opts OtelClientOptions) (*sdklog.LoggerProvider, error) {
    // Validate.  No silent defaults for required fields.
    if opts.Endpoint == ""      { return nil, fmt.Errorf("otelclient: Endpoint required") }
    if opts.TLSConfig == nil    { return nil, fmt.Errorf("otelclient: TLSConfig required") }
    if opts.ServiceName == ""   { return nil, fmt.Errorf("otelclient: ServiceName required") }
    if opts.Log == nil          { return nil, fmt.Errorf("otelclient: Log required") }

    // Process-global error handler.  Idempotent — calling twice replaces.
    // Multiple callers of NewOtelLoggerProvider in the same process land
    // on the same handler; deliberate, the SDK design.
    otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
        opts.Log.Warn().Err(err).Str("event", "otel_sdk_error").Msg("OTel SDK error")
    }))

    // Build exporter with our retry policy.
    exporterOpts := []otlploggrpc.Option{
        otlploggrpc.WithEndpoint(opts.Endpoint),
        otlploggrpc.WithTLSCredentials(credentials.NewTLS(opts.TLSConfig)),
    }
    if opts.RetryMaxElapsedTime > 0 {
        exporterOpts = append(exporterOpts, otlploggrpc.WithRetry(otlploggrpc.RetryConfig{
            Enabled:         true,
            InitialInterval: time.Second,
            MaxInterval:     5 * time.Second,
            MaxElapsedTime:  opts.RetryMaxElapsedTime,
        }))
    } else {
        // RetryMaxElapsedTime=0 means disable retry — but the SDK API doesn't
        // expose disable; passing Enabled:false achieves it.
        exporterOpts = append(exporterOpts, otlploggrpc.WithRetry(otlploggrpc.RetryConfig{Enabled: false}))
    }
    if opts.ExportTimeout > 0 {
        exporterOpts = append(exporterOpts, otlploggrpc.WithTimeout(opts.ExportTimeout))
    }

    var exporter sdklog.Exporter
    exporter, err := otlploggrpc.New(context.Background(), exporterOpts...)
    if err != nil { return nil, fmt.Errorf("otelclient: build OTLP exporter: %w", err) }
    if opts.ExporterWrap != nil { exporter = opts.ExporterWrap(exporter) }

    // Build processor.
    processorOpts := []sdklog.BatchProcessorOption{}
    if opts.MaxQueueSize > 0   { processorOpts = append(processorOpts, sdklog.WithMaxQueueSize(opts.MaxQueueSize)) }
    if opts.ExportInterval > 0 { processorOpts = append(processorOpts, sdklog.WithExportInterval(opts.ExportInterval)) }
    if opts.ExportTimeout > 0  { processorOpts = append(processorOpts, sdklog.WithExportTimeout(opts.ExportTimeout)) }
    processor := sdklog.NewBatchProcessor(exporter, processorOpts...)

    // Build provider with resource attributes.
    res, err := sdkresource.Merge(sdkresource.Default(), sdkresource.NewSchemaless(
        semconv.ServiceName(opts.ServiceName),
    ))
    if err != nil { return nil, fmt.Errorf("otelclient: build resource: %w", err) }
    return sdklog.NewLoggerProvider(
        sdklog.WithResource(res),
        sdklog.WithProcessor(processor),
    ), nil
}
```

### Design — `netlogger/otel_sink.go`

```go
// otelSink emits Events as OTel log records via a *otellog.Logger obtained
// from a shared *sdklog.LoggerProvider.  The provider's BatchProcessor is
// non-blocking (drops on overflow) so Emit returns immediately.
//
// One Sink instance per netlogger.Service.  The Logger is constructed once
// in New and reused — provider.Logger() is mutex-guarded internally so
// caching avoids contention on the hot path.
type otelSink struct {
    logger otellog.Logger
}

func newOtelSink(provider *sdklog.LoggerProvider) *otelSink {
    return &otelSink{
        // scope name "clawker.netlogger" — discriminates this stream from
        // CP's zerolog→OTLP bridge ("clawker") in the OS index downstream.
        logger: provider.Logger("clawker.netlogger"),
    }
}

func (s *otelSink) Emit(ctx context.Context, ev Event) {
    var rec otellog.Record
    rec.SetEventName("ebpf.egress.flow")
    rec.SetTimestamp(ev.Timestamp)
    rec.SetObservedTimestamp(time.Now())
    rec.SetSeverity(otellog.SeverityInfo)
    rec.SetSeverityText("INFO")
    rec.SetBody(otellog.StringValue("ebpf egress flow"))

    // At-source attribution rules:
    // - container_id always present (event is container-scoped)
    // - agent / project present when LabelCache hit; empty string on miss
    //   (no synthesis from cgroup name — raw Docker label values only)
    rec.AddAttributes(
        otellog.String("source", "ebpf"),
        otellog.String("verdict", verdictString(ev.Verdict)),
        otellog.String("container_id", ev.ContainerID),
        otellog.String("agent", ev.Agent),
        otellog.String("project", ev.Project),
        otellog.Int64("cgroup_id", int64(ev.CgroupID)),  // signed because OTel attr is int64; cgroup_id fits
        otellog.String("dst_ip", ev.DstIP.String()),
        otellog.Int("dst_port", int(ev.DstPort)),
        otellog.String("l4_proto", l4ProtoString(ev.L4Proto)),
        otellog.Bool("ipv6", ev.IsIPv6),
        otellog.Bool("ipv4_mapped", ev.IsMapped),
        otellog.String("dst_host", ev.Domain),               // empty for direct-IP / no DNS cache hit
        otellog.Int64("domain_hash", int64(ev.DomainHash)),  // 0 if no DNS resolution
    )
    s.logger.Emit(ctx, rec)
}
```

### CP main wiring

Boot sequence additions in `cmd/clawker-cp/main.go`, after `ebpfMgr.Load()`, after `bus + feeder` setup, before `AgentWatcher.Start()`:

```go
// Step 9c: netlogger — OTLP egress event emitter.
// Degrades to event=netlogger_unavailable on construction failure.
// Stops via drain hook BEFORE ebpfMgr.FlushAll() so in-flight events drain.
var netloggerSvc *netlogger.Service
{
    var degradeErr error
    var provider *sdklog.LoggerProvider

    // Source TLS config from existing otelcerts.Service (no new cert mint).
    if otelCertsSvc == nil {
        degradeErr = fmt.Errorf("otelcerts unavailable")
    } else {
        tlsCfg, err := otelCertsSvc.LoadTLSConfig("netlogger")
        if err != nil {
            degradeErr = fmt.Errorf("LoadTLSConfig: %w", err)
        } else {
            endpoint := /* hostport for OtelInfraPort — same shape as the
                          existing logger.New endpoint resolution */
            provider, err = controlplane.NewOtelLoggerProvider(controlplane.OtelClientOptions{
                Endpoint:            endpoint,
                TLSConfig:           tlsCfg,
                ServiceName:         "clawker-cp",
                MaxQueueSize:        2048,
                ExportInterval:      time.Second,
                ExportTimeout:       30 * time.Second,
                RetryMaxElapsedTime: 10 * time.Second,
                Log:                 log,
                ExporterWrap:        netlogger.NewCountingExporterWrap(promCounters),
            })
            if err != nil { degradeErr = fmt.Errorf("NewOtelLoggerProvider: %w", err) }
        }
    }

    if degradeErr == nil {
        netloggerSvc, degradeErr = netlogger.New(netlogger.Deps{
            Mgr:                ebpfMgr,
            Bus:                bus,
            Docker:             dockerCli,
            Log:                log,
            OtelLoggerProvider: provider,
        })
    }

    if degradeErr != nil {
        log.Error().Err(degradeErr).
            Str("event", "netlogger_unavailable").
            Str("component", "netlogger").
            Msg("netlogger degraded — egress events will not be exported; firewall enforcement unaffected")
        netloggerSvc = nil
    } else {
        if err := netloggerSvc.Start(ctx); err != nil {
            log.Error().Err(err).
                Str("event", "netlogger_unavailable").
                Str("component", "netlogger").
                Msg("netlogger Start failed — degraded")
            netloggerSvc = nil
        }
    }
}
```

**Drain hook** — the AgentWatcher drain callback today calls:
```
ActionQueue.Close → grpcServer.GracefulStop → handler.CancelAllBypassTimers → Stack.Stop → ebpfMgr.FlushAll
```

Insert `netloggerSvc.Stop(stopCtx)` (with 5s deadline) BEFORE `ebpfMgr.FlushAll()`. Order matters: the ringbuf reader must finish draining and the BatchProcessor must flush before we tear down the BPF maps. Use a fresh `context.WithTimeout(context.Background(), 5*time.Second)` because the outer ctx may already be cancelled.

Wrap with nil check (`if netloggerSvc != nil`) — degraded path has nil here.

### `NewCountingExporterWrap` for Prom counters

```go
// In netlogger/metrics.go:
type countingExporter struct {
    inner       sdklog.Exporter
    exportTotal prometheus.Counter  // success
    exportError prometheus.Counter  // failure
}

func (c *countingExporter) Export(ctx context.Context, recs []sdklog.Record) error {
    err := c.inner.Export(ctx, recs)
    if err != nil { c.exportError.Add(float64(len(recs))); return err }
    c.exportTotal.Add(float64(len(recs)))
    return nil
}
func (c *countingExporter) Shutdown(ctx context.Context) error  { return c.inner.Shutdown(ctx) }
func (c *countingExporter) ForceFlush(ctx context.Context) error { return c.inner.ForceFlush(ctx) }

func NewCountingExporterWrap(reg prometheus.Registerer) func(sdklog.Exporter) sdklog.Exporter {
    successCtr := prometheus.NewCounter(prometheus.CounterOpts{Name: "clawker_netlogger_otel_export_succeeded_total"})
    errorCtr   := prometheus.NewCounter(prometheus.CounterOpts{Name: "clawker_netlogger_otel_export_failed_total"})
    reg.MustRegister(successCtr, errorCtr)
    return func(inner sdklog.Exporter) sdklog.Exporter {
        return &countingExporter{inner: inner, exportTotal: successCtr, exportError: errorCtr}
    }
}
```

Plus the previously-declared netlogger Prom counters (registered in this task):
- `clawker_netlogger_ringbuf_received_total`
- `clawker_netlogger_ringbuf_errors_total`
- `clawker_netlogger_ringbuf_kernel_drops_total` (gauge-summed across CPUs, refreshed periodically from `events_drops` PERCPU_ARRAY)
- `clawker_netlogger_queue_received_total`
- `clawker_netlogger_queue_dropped_total`
- `clawker_netlogger_parse_errors_total`
- `clawker_netlogger_emit_succeeded_total`
- `clawker_netlogger_ratelimit_drops_total{cgroup_id}` (gauge-summed periodically from `ratelimit_drops` HASH; cgroup_id label is high-cardinality but bounded by the number of live containers)

### Implementation steps

1. Add `internal/controlplane/otelclient.go` with `NewOtelLoggerProvider` + `OtelClientOptions`. Match the existing CP package import style. Unit test with a fake `*tls.Config`, verify error paths (missing fields), verify the resource attribute is set.
2. Add `netlogger/otel_sink.go` with `otelSink`. Unit test with an in-process `sdklog.Exporter` test double (or `sdktest.Recorder` if available) — assert the emitted `otellog.Record` has the expected attribute set for an Event with each verdict, with and without LabelCache hits.
3. Add `netlogger/metrics.go` with the counter declarations + `NewCountingExporterWrap`. Wire counter increments at the sites declared in Task 2 (reader.go, processor.go, OtelSink). Add periodic gauge refresh goroutine in `Service.Start` for `events_drops` and `ratelimit_drops`.
4. Update `netlogger.Deps` to include `OtelLoggerProvider *sdklog.LoggerProvider`. In `New`, if provider is non-nil construct `otelSink`; otherwise use `nopSink` (for tests). `stdoutSink` from Task 2 is removed in this task — no longer needed.
5. Wire CP main per the boot-sequence design. Confirm degraded paths emit `event=netlogger_unavailable`. Confirm drain hook is in the AgentWatcher callback BEFORE `ebpfMgr.FlushAll()`.
6. Update `internal/controlplane/CLAUDE.md` to document `otelclient.go` as a new file in the package surface table and add netlogger to the boot-sequence numbered list.
7. Update `internal/controlplane/firewall/ebpf/netlogger/CLAUDE.md` with the OTel sink section, the record shape, the eight Prom counters, and the trust-lane note (infra endpoint, not agent endpoint).
8. Build a small integration test (still no real BPF) that constructs a Service with `OtelLoggerProvider` pointing at a test gRPC OTLP server, feeds synthetic events through the processor, asserts the test collector received records. Use the OTel SDK's own test helpers from `go.opentelemetry.io/otel/sdk/log/logtest` if present, otherwise stand up a minimal gRPC server with `grpc.NewServer` registering `collogspb.LogsServiceServer`.

### Acceptance Criteria

```bash
# Compiles
go build ./internal/controlplane/... ./cmd/clawker-cp/

# Generic OTel client tests pass
go test ./internal/controlplane -v -run TestNewOtelLoggerProvider -count=1

# netlogger tests pass under race detector
go test -race ./internal/controlplane/firewall/ebpf/netlogger/... -v -count=1

# CP startup test passes — verifies the netlogger wiring + degraded path
go test ./internal/controlplane -v -run TestStartup -count=1

# AgentWatcher drain test passes — verifies Stop ordering (netlogger.Stop before ebpfMgr.FlushAll)
go test ./internal/controlplane -v -run TestAgentWatcher_Drain -count=1

# event=netlogger_unavailable structured log emits on degraded path
go test ./internal/controlplane -v -run TestNetloggerDegraded -count=1

# No panic / log.Fatal in any code path reachable from netlogger
! grep -rE 'panic\(|log\.Fatal|os\.Exit' internal/controlplane/firewall/ebpf/netlogger/

# Prom counters registered
go test ./internal/controlplane/firewall/ebpf/netlogger -v -run TestPromMetricsRegistered -count=1
```

### Wrap Up

1. Update Progress Tracker: Task 3 → `complete`.
2. Append key learnings — degraded-path behavior under realistic failure modes (collector unreachable, cert expired, etc.), Prom registry conflicts if any.
3. Run completion gate: `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer`.
4. Commit: `feat(netlogger): otel sink + cp wiring + generic otel client`.
5. **STOP.** Handoff:

> **Next agent prompt:** "Continue the eBPF network event emitter initiative. Read the Serena memory `initiative_ebpf-netlogger` — Tasks 1–3 are complete. Begin Task 4: E2E test + docs."

---

## Task 4: E2E test + docs

**Creates/modifies:**
- `test/e2e/netlogger_test.go` — E2E test exercising allow / deny / bypass paths and asserting OTLP records arrive
- `docs/firewall.mdx` (or new `docs/observability.mdx`) — Mintlify doc covering netlogger
- All CLAUDE.md updates from previous tasks reviewed for consistency

**Depends on:** Tasks 1, 2, 3.

### E2E test design

Running the full `clawker monitor up` stack in an e2e suite is heavy and adds flake surface (OpenSearch warmup, index template race). Two viable shapes:

**Option A (preferred): in-process OTLP test collector.** Stand up a `grpc.Server` registering `collogspb.LogsServiceServer` in the test process. Override the netlogger's OTel endpoint to point at this server. Drive the firewall through normal CLI commands (`harness.Run("firewall", "up")`, `harness.Run("run", "alpine", "wget", "blocked.example.com")`). Assert records arrive at the test collector with expected attributes.

**Option B: monitoring stack roundtrip.** `harness.Run("monitor", "up")` then assert via the OpenSearch query API. Heavier, slower, more flaky. Use only if Option A can't validate the trust-lane mTLS handshake.

Pick Option A. Trust-lane validation can be a separate targeted test that constructs the gRPC server with the same `otelcerts.Service` config the production wiring uses.

### Test outline

```go
func TestNetlogger_E2E(t *testing.T) {
    h := harness.New(t)
    defer h.Cleanup()

    // Stand up in-process OTLP test collector.
    collector := newTestOTLPCollector(t)
    defer collector.Stop()

    // Configure CP to push to the test collector.
    // Override OtelInfraPort + endpoint via test-only env vars or config helper.
    h.SetEnv("CLAWKER_OTEL_INFRA_ENDPOINT", collector.Addr())

    // Start CP via normal CLI path.
    h.Run("controlplane", "up")
    defer h.Run("controlplane", "down")

    // Create project + agent.
    h.Run("init", "test-project")
    h.Run("run", "--detach", "--name", "test-agent", "alpine", "sleep", "60")

    // Trigger ALLOW (allowed.example.com is on default allow list).
    h.RunInContainer("test-agent", "wget", "-q", "https://allowed.example.com")

    // Trigger DENY (random IP, not in any rule).
    h.RunInContainer("test-agent", "wget", "-T", "2", "http://203.0.113.1")  // RFC 5737 test addr

    // Trigger BYPASSED.
    h.Run("firewall", "bypass", "test-agent", "30s")
    h.RunInContainer("test-agent", "wget", "-q", "http://203.0.113.2")

    // Wait for OTel batch flush.
    collector.WaitForRecords(t, 3, 10*time.Second)

    // Assertions.
    records := collector.Records()
    assert.NotEmpty(t, records)
    foundVerdicts := map[string]bool{}
    for _, rec := range records {
        verdict := rec.Attribute("verdict")
        foundVerdicts[verdict] = true
        // Every record must have container attribution.
        assert.NotEmpty(t, rec.Attribute("container_id"))
        assert.Equal(t, "test-agent", rec.Attribute("agent"))
        assert.Equal(t, "test-project", rec.Attribute("project"))
        // Scope name discriminates the stream.
        assert.Equal(t, "clawker.netlogger", rec.ScopeName())
        // service.name is shared with CP (not a separate service).
        assert.Equal(t, "clawker-cp", rec.ResourceAttribute("service.name"))
    }
    assert.True(t, foundVerdicts["allowed"])
    assert.True(t, foundVerdicts["denied"])
    assert.True(t, foundVerdicts["bypassed"])
}
```

Additional targeted tests:
- `TestNetlogger_E2E_CollectorDown`: stop the test collector mid-stream, assert that CP keeps running, that subsequent firewall RPCs succeed, and that `clawker_netlogger_otel_export_failed_total` increments.
- `TestNetlogger_E2E_DrainOrdering`: verify that on `controlplane down`, netlogger drains BEFORE ebpfMgr.FlushAll (assert via test collector receiving the final in-flight events).

### Documentation

**`docs/observability.mdx`** (new) — user-facing Mintlify page covering:
- What netlogger is and what events it emits (verdict, attribution, network 4-tuple, domain)
- How to point it at a custom OTel collector (env var override)
- The Prom counters and how to read them
- Trust-lane note: infra-only mTLS, not the agent-lane receiver
- Known limitation: ALLOWED records on this branch carry zero bytes/duration; sock_ops follow-up is tracked separately

**`internal/controlplane/firewall/ebpf/netlogger/CLAUDE.md`** — package reference, fully fleshed out:
- Architecture diagram (reader → channel → processor → sink)
- The four pinned BPF maps and their semantics
- The five Prom counter dimensions (kernel-fault drops, ratelimit drops, queue drops, parse errors, export errors)
- LabelCache cgidmap pattern citation (Tetragon `pkg/cgidmap/cgidmap.go`)
- Reverse-DNS limitation (hash-only until follow-up populates domains)
- Sink interface contract (non-blocking)
- Trust-lane (infra endpoint, mTLS via `otelcerts.Service.LoadTLSConfig`)
- Test seams (`Sink` interface for unit tests, in-process OTLP collector for E2E)

**Update existing CLAUDE.md files** (incremental from Tasks 1–3 to confirm consistency):
- root `CLAUDE.md` — no change expected (CP no-panic doc already covers the pattern)
- `internal/controlplane/CLAUDE.md` — confirm `otelclient.go` is in the file table, confirm netlogger is in the startup sequence
- `internal/controlplane/firewall/CLAUDE.md` — note that netlogger is a sibling subsystem under the firewall domain
- `internal/controlplane/firewall/ebpf/CLAUDE.md` — confirm the four new maps + accessor methods are documented, confirm `enter_state` enum is documented

### Acceptance Criteria

```bash
# E2E tests pass (Docker required)
go test ./test/e2e/... -v -run TestNetlogger -timeout 10m

# Full e2e suite passes (no regressions to existing firewall flows)
go test ./test/e2e/... -v -timeout 15m

# Mintlify docs build
go run ./cmd/gen-docs --doc-path docs --markdown --website
test -f docs/observability.mdx

# All CLAUDE.md files reference netlogger consistently
grep -l netlogger internal/controlplane/CLAUDE.md \
                  internal/controlplane/firewall/CLAUDE.md \
                  internal/controlplane/firewall/ebpf/CLAUDE.md \
                  internal/controlplane/firewall/ebpf/netlogger/CLAUDE.md

# Unit + e2e all green (final gate)
make test
go test ./test/e2e/... -v -timeout 15m
```

### Wrap Up

1. Update Progress Tracker: Task 4 → `complete`.
2. Append key learnings — flakiness in the E2E test, any tuning required to make it deterministic, any Mintlify warnings.
3. Run completion gate: `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer`.
4. Commit: `test(netlogger): e2e + docs`.
5. Open a PR with the four commits. Title: `feat(ebpf): network event emitter (netlogger)`. Body: link to this initiative memory, summary of what shipped and what's deferred (see Appendix).

---

## Appendix — Out-of-scope items captured for follow-up branches

1. **Domain string population.** Today's reverse-DNS only has hashes. Add a `domain_strings` map (key=domain_hash, value=fixed-size char array or string-table reference) populated by CoreDNS dnsbpf plugin so netlogger's `ReverseDNSMap` can return real strings instead of empty.
2. **Envoy native OTLP access logs.** Reuses the same generic `NewOtelLoggerProvider` (now in CP). Envoy emits directly to the infra collector — no CP-side reader. This is the byte-count source for ALLOWED L7 flows; pairs with netlogger by 5-tuple at query time.
3. **CoreDNS log plugin → filelog receiver.** Replaces the current coredns `otel` plugin's gRPC push with a stdout-tailed filelog receiver.
4. **OpenSearch backend migration.** The netlogger emits to whatever OTel collector is configured — backend choice is orthogonal.

These form a coherent next initiative if the broader SIEM pivot is taken on later.

**Not on this list (intentionally):** sock_ops / TCP_CLOSE roundtrip byte tracking. See "Why decision-time emit is the right scope" in the Context section — this isn't deferred, it's the wrong shape for production observability per Cilium / Tetragon practice. Bytes come from Envoy ALS, not BPF sock_ops.
