# eBPF Network Event Emitter (netlogger)

**Branch:** `feat/ebpf-logging`
**Status:** Design locked 2026-05-21. Ready for sequential execution.

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: BPF ringbuf + per-decision-point emit + drop counters + kernel rate limiter | `complete` | claude-opus-4-7 |
| Task 2: netlogger subpackage scaffold — ringbuf reader, LabelCache, reverse-DNS, processor (no OTLP) | `complete` | claude-opus-4-7 |
| Task 3: Generic OTel client in `internal/controlplane/` + netlogger OTel sink + CP main wiring + drain hook | `complete` | claude-opus-4-7 |
| Task 4: docs + monitor-stack routing wiring | `complete` | claude-opus-4-7 |
| Task 5: UAT + commit + push + PR open | `in_progress` (uncommitted; pre-UAT triage shipped) | claude-opus-4-7 |

## Key Learnings

### Task 1 (2026-05-21)

- **bpf2go `-type` extraction needs a BTF anchor on ringbuf maps.** Without a typed reference reachable from BPF program signatures, `clang -g` strips the struct out of BTF and bpf2go fails with `Error: collect C types: not found`. Solution: declare `struct egress_event` BEFORE the `events_ringbuf` map definition and add `__type(value, struct egress_event)` to the map's anonymous struct. Tried two alternative anchors first — a `const volatile` global, and a `__attribute__((used))` global — both produced `type name "clawkerEgressEvent" is used multiple times` from bpf2go (BTF entries for the global + the program usage collide). `__type` is the canonical pattern; the ringbuf doesn't otherwise honor it (it has no kernel-side key/value typing), but the BTF entry is what bpf2go reads.
- **`-type` flag in `gen.go` is required for each custom-typed ringbuf record.** Add to the `go:generate` line: `-type egress_event`. Future netlogger event types (sock-state, etc.) will need the same pattern.
- **Build environment confirmed working on Debian bookworm + clang 14.** Makefile pins clang 18 on Ubuntu Noble, but the actual `go generate` invocation accepts older clang versions for BPF compilation. CI on the pinned Ubuntu runner remains the authoritative build.
- **CAP_BPF unavailable inside the clawker dev container.** `ebpf.NewMap` returns EPERM, so Manager.Load + accessor-after-Load tests can't run in `make test`. The new `requireBPF(t)` helper skips gracefully. The kernel-side correctness gate is `make ebpf` (verifier-bound bytecode generation) plus Task 4 E2E (full agent-container flow).
- **`__sync_fetch_and_sub`-and-restore is NOT wrap-safe under racing CPUs.** Three+ CPUs racing on `tokens==1` could leave the counter at `(u64)-2` despite a per-CPU restore. Switched to `__sync_val_compare_and_swap` with a `#pragma unroll` retry loop (`RATELIMIT_CAS_RETRIES=4`) and an explicit wrap-clamp. The CAS bound caps verifier-loop budget; sustained contention beyond that gets counted as a rate-limit drop instead of a silent emission.
- **`enter_enforced` must verify `container_map` BEFORE calling `metric_inc(ACTION_BYPASS)`.** The original ordering (metric_inc first, container_map lookup second) emitted an orphan metric when the lookup race-failed. Fixed by reordering — now the metric only fires once `container_map` confirms the container is still managed.
- **Strict directive caveat realized:** `sock_create` now emits a `submit_event(ALLOWED)` for every non-RAW socket creation (per "emit at every decision point"). Volume is bounded by the BPF token bucket (640 records/sec/cgroup at default `RATELIMIT_BURST=64` / `RATELIMIT_REFILL_NS=100ms` / `RATELIMIT_TOKENS_PER=64`). Operators filter on `verdict=allowed AND l4_proto != stream/dgram` at the dashboard layer; the BPF side does not discriminate.

### Task 2 (2026-05-21)

- **Bus ordering had to move earlier than step 9a.** The original `cmd/clawker-cp/main.go` constructed the overseer bus at step 9a (after `grpcServer.Serve`). FirewallEnable publishes on the bus, so RPCs that fire between Serve and bus.Start would silently drop. Fix: hoisted bus.New + bus.Start to step 8, immediately before NewHandler. The dockerevents feeder stays in step 9a — only bus construction moved. This invariant ("bus.Start BEFORE the gRPC listener accepts") is now load-bearing for any future bus-publishing RPC handler.
- **Event types live in the producer-adjacent package, not in `overseer/`.** The initiative doc said "overseer/types.go (or sibling)" — the established pattern (`dockerevents.DockerEvent` in dockerevents/, `agent.Session*` in agent/) is producer-package events. Put `EBPFContainerEnrolled` in `internal/controlplane/firewall/ebpf/events.go`. The firewall.Handler emit site already imports ebpf; netlogger consumer already imports ebpf. No new import edges either side.
- **Event type satisfies `overseer.Event` structurally — no import needed.** The ebpf package does NOT import `internal/controlplane/overseer`. Go's structural interface satisfaction means `EBPFContainerEnrolled.EventName / OccurredAt / MarshalZerologObject` is enough; `overseer.Publish[T overseer.Event]` resolves T at the call site.
- **`Service.Stop` ordering matters: close ringbuf BEFORE cancelling ctx.** Initial draft did cancel → close. That caused processor to exit via `case <-ctx.Done()` immediately while the reader was still pushing records onto the queue, defeating the documented "processor drains remaining buffered records before exiting" contract. Correct order: unsubscribe → close rb (reader exits on ringbuf.ErrClosed → closes queue → processor drains then returns on `!ok`) → cancel ctx (for reverse-DNS refresher and the subscriber goroutines that select on ctx.Done as belt-and-braces).
- **Subscriber goroutines need `select ctx.Done()` as belt-and-braces.** `Subscription.Unsubscribe()` posts to `unsubscribeCh` via `select { case <-o.stopCh: return; case ... }`. If the bus is closed first (during CP drain), Unsubscribe returns without closing `Subscription.C`, so a `for ev := range sub.C` loop leaks. Belt-and-braces: select on both `s.ctx.Done()` AND `<-sub.C` in every subscriber goroutine.
- **`Publish` returns false on bus closed / not started / full buffer — log it.** Overseer logs its own warn line, but adding a producer-side log line with `event=netlogger_enroll_publish_dropped` + `container_id` + `cgroup_id` makes blast-radius (one cgroup of unattributed records) locatable from the structured log surface. The bus's drop line carries the event name but not these identifiers.
- **ReverseDNSMap is honest about the v1 limitation.** `dns_cache` stores only `{IPv4 → {hash, expire_ts}}` — the domain string lives in CoreDNS process memory and is unavailable to userspace. Lookup returns "" for every hash; the refresh walk records the OBSERVED hash set so a future `(hash → string)` population path lights up Lookup without an API change. Documented in the type doc + the package CLAUDE.md.
- **`docker.ContainerInspectResult.Container.Config.Labels` is the right path.** First draft used `info.Config.Labels` — wrong; the moby type is `mobyclient.ContainerInspectResult{Container: mobycontainer.InspectResponse{Config: ...}}`. Match `internal/controlplane/agent/peer_lookup_moby.go` exactly.
- **CAP_BPF caveat from Task 1 applies to Service tests.** Real `Service.Start` opens a `ringbuf.NewReader` against a `*ebpf.Map`, which needs CAP_BPF (unavailable in the dev container). The test seam: `newTestService` in `netlogger_test.go` wires `subscribeBus` directly without calling `Start`. Pipeline tests bypass the kernel entirely — `fakeRingbuf` implements the `readerSource` interface and serves scripted records.
- **`overseer.Subscribe[T]` panics if T is an interface.** Confirmed by `subscribe.go:86` — `eventType := reflect.TypeOf(zero)` returns nil for an interface zero value. Sticking to concrete struct types (`ebpf.EBPFContainerEnrolled`, `dockerevents.DockerEvent`) keeps the netlogger subscriptions safe; any future refactor that uses an interface-typed event would crash CP at boot (violating no-panic). Worth documenting on the consumer side, not just relying on overseer's own check.

### Task 3 (2026-05-21)

- **Provider lifetime is the caller's — Stop alone does not flush OTLP batches.** `netlogger.Service.Stop` only drains the kernel→sink pipeline (reader, processor, reverse-DNS, subscriber goroutines). The `*sdklog.LoggerProvider` it holds is by contract NOT owned by Stop (`Deps.OtelLoggerProvider` doc + netlogger/CLAUDE.md). CP main's drain hook must Shutdown the provider after Stop, otherwise the BatchProcessor goroutine survives until process exit AND any in-flight OTLP batch is never flushed. First subagent review flagged this. Fix: hoist `netloggerProvider` to run scope alongside `netloggerSvc`, append `provider.Shutdown(stopCtx)` to `drainCallbackBody` after `netloggerSvc.Stop`. Error aggregated into the drain errs. This is the load-bearing wiring detail that makes "Stop before FlushAll" actually deliver flushed telemetry.
- **`otel.SetErrorHandler` is process-global; wrap in sync.Once.** Calling it inside `NewOtelLoggerProvider` every invocation means the LAST caller in a multi-provider process silently steals SDK error routing from earlier subsystems. Today there's only one consumer (netlogger), but the CLAUDE.md explicitly invites future emitters (sysexec events, etc.). Fix: package-level `errorHandlerOnce sync.Once` in otelclient.go binds the first caller's `opts.Log` into the handler permanently. Subsequent providers share the SDK runtime but their boot-time SetErrorHandler call is a no-op. Documented in the comment so a future refactor doesn't reintroduce the clobber.
- **Circuit-breaker trip-transition must swallow the err.** Original code returned the inner error on the call that flipped `tripped=true`. That gives the SDK's BatchProcessor one extra "this batch failed, retry it" cycle before the breaker fully takes over — inconsistent with the documented post-trip drop-on-floor contract. Fix: return nil on the trip transition itself (the breaker log line is already written). Test updated: `TestCircuit_TripsAfterThreshold` now asserts the 3rd call returns nil (trip transition) while calls 1 and 2 propagate the inner err.
- **Unconfigured ≠ degraded; log level matters.** All five degraded reasons funneled through a single `log.Error()` call, but the `endpoint == ""` path (no `OTEL_EXPORTER_OTLP_ENDPOINT`) is the normal optional-monitoring-stack deployment shape. Crying Error on every default-config boot trains operators to filter `event=netlogger_unavailable` out of their triage stream, masking real failures later. Fix: track an `unconfigured` bool through the switch; route that path to `log.Warn()` and reserve `Error` for genuine failures (otelcerts nil, LoadTLSConfig, NewOtelLoggerProvider, netlogger.New, netlogger.Start, plaintext rejection).
- **Trust-lane rejection is at the boundary, not inside the SDK.** CP main rejects `http://` endpoints with `step=OTLP endpoint is plaintext` BEFORE constructing the provider. Pushing infra telemetry over plaintext would smuggle records onto the agent-lane receiver (per the trust-lane-separation feedback in MEMORY.md) — fail loud at the configuration boundary instead of hoping `otlploggrpc` rejects later.
- **Sink shape changed from `Deps.Sink Sink` to `Deps.OtelLoggerProvider *sdklog.LoggerProvider`.** Eliminated the public `NewNopSink` and `NewStdoutSink` constructors plus the `stdoutSink` / `stdoutRecord` types — Task 2 used them for break-glass acceptance, but with the OTel sink wired Task 3 makes them dead weight. `New()` internally builds `otelSink` when the provider is non-nil; `nopSink` is the test/degraded default. One fewer surface to keep in sync with the strict directive. Deleted `sink_test.go` (the only file that asserted the removed APIs); migrated `processor_test.go::TestPipeline_StdoutSink` to `TestPipeline_EndToEnd` using the in-file `recordingSink`. `netlogger_test.go` dropped the "nil sink" Deps validation subtest since Sink is no longer in Deps.
- **Preflight is a raw `tls.Dial`, not `grpc.DialContext`.** `grpc.DialContext` + `grpc.WithBlock` are deprecated in v1.81+ (the project's grpc pin). `tls.DialWithDialer` is cheaper for a one-shot probe AND surfaces the actual underlying failure (refused / timeout / cert mismatch) cleanly. The OTel SDK's own gRPC dial happens later inside `otlploggrpc.New`; the preflight just confirms the collector is reachable + speaks TLS against our cert/root.
- **otelSink emits BOTH `l4_proto` (string) AND `l4_proto_code` (int).** The strict directive says every field on Event lands as an attribute; the L4Proto raw uint8 codes (1=stream/2=dgram/3=raw) deserve human-friendly names for dashboard filtering, but also the raw code in case the SOCK_* constant set evolves and operators need to filter on a code we don't have a string for. Belt-and-braces; cheap.
- **Test-side OTel pipeline uses `SimpleProcessor` + `recordingExporter`.** BatchProcessor adds a flush interval (default 1s) that would make assertions racy. `sdklog.NewSimpleProcessor` flushes synchronously on every Emit, so the test sees records immediately. `recordingExporter.Export` clones each record (SDK retains slice ownership) so the test's assertion window is decoupled from any subsequent SDK mutation.
- **Two tests were self-serving — deleted.** `TestNewOtelSink_NilProviderReturnsNil` asserts a one-line `if provider == nil { return nil }` guard already enforced by every Service construction path that follows; `TestCircuit_DefaultThreshold` was a near-duplicate of `TestCircuit_TripsAfterThreshold` with the sole behavior delta being "constructor fills FailureThreshold=0 → 3". Test-hunter flagged both; pulled before commit.
- **Prom-metrics scrape wiring deferred to follow-up PR.** Spec Task 3 listed Prom-counter registration + a counting-exporter wrap + periodic gauge refresh from `events_drops` / `ratelimit_drops`. User confirmed in mid-task clarification: "what events are there for now, CP gets it and emits it" — descope metrics, leave the existing 6 counters as in-process diagnostics with a TODO at the top of `metrics.go`. The follow-up will land scrape exposure for the existing 6 + the deferred dimensions in one go, alongside a `Service.Metrics()` tripped-state surface so operators can see the circuit-breaker state from a /metrics endpoint instead of a log-line that has scrolled past.

### Task 4 (2026-05-21)

- **Plan undercounted scope: monitor-stack ingestion routing was missing.** Plan Task 4 said "docs only". Grep sweep found `service.name=ebpf-egress` had no route in `internal/monitor/templates/otel-config.yaml.tmpl` — `routing/trusted` only matched `clawker-cp` / `envoy` / `coredns`, so records would have landed in the `logs/trusted_unrouted` quarantine (debug-only, never indexed) on local UAT. User selected "wire it now" at the question. Wired in this task: new `resource/netlogger` processor (stamps `ingest_source=netlogger`), `opensearch/logs_netlogger` exporter targeting `clawker-ebpf-egress` index, `routing/trusted` table entry, `logs/netlogger` pipeline, new `index-templates/clawker-ebpf-egress.json` mirroring the otel_sink attribute set, ISM `clawker-retention.json` index_patterns + bootstrap pre-create loop both updated. Effect: out-of-box `clawker monitor up` UAT now lands records in OpenSearch without any operator wiring. Lesson: when a task is framed as "docs only", still grep-sweep — the plan author may have missed a load-bearing wiring detail that breaks UAT.
- **Doc surfaces grew further than plan listed.** The plan listed only a handful of CLAUDE.md updates. Actual stale-ref sweep needed: `docs/{firewall,monitoring,control-plane,architecture,threat-model,quickstart}.mdx`, `docs/docs.json` nav, `.claude/docs/{ARCHITECTURE,DESIGN,KEY-CONCEPTS,REPO-STRUCTURE,MONITORING-REFERENCE}.md`, `.claude/rules/monitoring.md`, `internal/monitor/CLAUDE.md`, `internal/controlplane/{firewall,overseer,otelcerts}/CLAUDE.md`. The "five indices" → "six indices" stale count drifted across four separate files and would have shown up immediately when any reviewer cross-checked them. Lesson: phrase that names a fixed-cardinality set (`five indices`, `four trusted pipelines`, `three subsystems`) is a stale-ref magnet — grep for the count words, not just the new noun.
- **`docs/observability.mdx` — keep it user-facing, not a CLAUDE.md transplant.** First draft duplicated implementation detail from `netlogger/CLAUDE.md` (BPF token-bucket burst/refill numbers, BatchProcessor SDK terminology, PERCPU_ARRAY type). Trimmed on review. The Mintlify page should answer "what records will I see and how do I query them"; the internal mechanics belong in CLAUDE.md.
- **`dst_host` is empty 100% of the time today, not "sometimes empty".** Drafted as "empty when cache miss" — wrong. Per `reverse_dns.go`, the userspace cache only stores hashes (the dnsbpf plugin doesn't write strings yet), so Lookup always returns "". Comment-analyzer flagged the imprecision. Worth tracking — when the follow-up plugin lands, the doc row + the "Current Limitations" callout both need updating in lockstep.
- **`otel-config.yaml.tmpl` template comments duplicate prose, drift independently.** Two in-template comments said "only cp/envoy/coredns hold infra-intermediate leaves today". The same sentiment was updated in `internal/monitor/CLAUDE.md` and `.claude/rules/monitoring.md` but the template comments drifted. They're easy to miss because they look like static config, not documentation. Grep for the literal text when updating routing-related claims.

### Task 5 (2026-05-22)

- **Cilium AND Tetragon both consume rate-limit tokens unconditionally on ringbuf-reserve failure.** Verified via deepwiki against `bpf/lib/ratelimit.h` (Cilium `ratelimit_check_and_take`) and `bpf/process/bpf_rate.h` + `bpf/lib/process.h` (Tetragon `cgroup_rate` + `event_output_metric`). Neither has anything resembling a refund helper. The Task-1 `ratelimit_refund` was industry-divergent and architecturally unsound: a non-atomic write under exactly the contention causing the failure, with the perverse incentive of nudging the bucket to retry harder when the correct response is to back off. Yanked.

- **`events_drops` is PERCPU and read-side stays in BPF subsystem land, NOT netlogger.** User reframed mid-task: netlogger is a security-telemetry pipeline; drop counters are operator/SRE diagnostics. Don't mix them. The BPF write path is correct (verified post-yank: `__sync_fetch_and_add` on `events_drops[0]` after `bpf_ringbuf_reserve == NULL`). Userspace `Manager.EventsDrops()` accessor exists. No userspace reader is wired today and that's INTENTIONAL — when the /metrics scrape follow-up lands, it'll surface drop counters from the firewall/eBPF subsystem, not from netlogger's OTel stream. Saved as `feedback_netlogger_security_only` in MEMORY.md.

- **clawker's FNV-1a 32-bit domain hash is divergent from Cilium AND Tetragon, but the fix is out of scope here.** Cilium uses userspace-allocated sequential u32 identities (`pkg/fqdn/namemanager` + IPCache) — no collision possible. Tetragon doesn't do per-domain enforcement in BPF at all. clawker's FNV approach has a theoretical collision floor that's harmless for today's deployment shape (single-digit-to-hundreds of firewall-rule domains) but a real architectural debt. Filed as `initiative_route_identity_allocator` Serena memory + a one-liner in `.serena/memories/bug-tracker.md`. The CP-side reverse map shipped here inherits the same collision floor as the existing `route_map` — no new risk introduced, just unfixed.

- **dst_host fix via control-plane reverse map (not via a new BPF map).** The set of domains dnsbpf will ever resolve is bounded and knowable at the control plane: every allow-rule with a domain destination + the internal hostnames CoreDNS serves out of band (`docker.internal` + `consts.MonitoringServiceHostnames`). `DomainHash` is deterministic. So netlogger can build `hash → domain` directly from firewall config — no BPF map round-trip needed. The data-plane alternative (extend dnsbpf to write a `domain_strings` BPF map) is architecturally purer but adds a pinned BPF map, dnsbpf changes, and ~32KB pinned with no per-record win. CP-side won on size / simplicity / "this PR" framing.

- **`firewall.Handler.AllResolvableDomains` must mirror `GenerateCorefile`'s passes exactly.** The two surfaces have to produce identical sets — if `GenerateCorefile` emits a zone, dnsbpf hashes it; netlogger's reverse map must contain the matching hash → domain entry, or `dst_host` will be empty for that zone's records. The new method shares the normalize / skip-IP-or-CIDR / skip-deny / dedup-against-reserved-internal passes. Tests pin the contract against the Corefile zone set.

- **`domain_hash` dropped from `otelSink.Emit`.** Strict directive ("emit ALL Event fields") was reframed once netlogger was clarified as security-telemetry-only. SOC analysts query on `dst_host`; a bare 32-bit hash without the string is unactionable noise. The hash stays on `Event` so `ReverseDNSMap.Lookup` can translate it to `dst_host`, but it doesn't ride the wire.

- **netlogger walks `dns_cache` on every refresh tick as a triage signal, not as the source of truth.** The control-plane DomainSource is the source. The walk detects hashes present in `dns_cache` that aren't in DomainSource — races (rule remove between dnsbpf write and netlogger refresh), dnsbpf stale entries, hash collisions against an unknown domain. Each tick logs `event=netlogger_reverse_dns_unattributed` with the unattributed count if non-zero. Triage signal in the structured CP log; no impact on per-record `dst_host` attribution.

### Task 5 — pre-UAT triage (2026-05-22)

**Status:** Tasks 1–4 are committed to `feat/ebpf-logging` (4 commits, pushed to origin). Two design corrections landed as UNCOMMITTED local edits before UAT runs — they are sitting on the worktree pending the user's UAT pass. No PR opened yet.

**Correction 1 — Yanked `ratelimit_refund` from `bpf/common.h`.** Task 1 had landed a CAS-bounded helper that returned a consumed token to the per-cgroup bucket when `bpf_ringbuf_reserve` failed (kernel-fault drop). User asked whether Cilium / Tetragon do this. Verified via deepwiki against `bpf/lib/ratelimit.h` (Cilium) and `bpf/process/bpf_rate.h` + `bpf/process/types/basic.h` + `bpf/lib/process.h` (Tetragon): NEITHER refunds. Both consume the token unconditionally and bump a drop counter; a failed reserve is the signal to back off, not retry harder. Refund creates a non-atomic write under exactly the contention causing the failure. Yanked the helper + the call site in `submit_event`. Two clean counters remain: `events_drops` (PERCPU, kernel-fault) and `ratelimit_drops` (HASH per cgroup, intentional throttle). Cleaner architecturally and matches industry convention.

**Correction 2 — `dst_host` populated via CP-side reverse map (no new BPF map).** Tasks 2-4 shipped with the documented limitation that `ReverseDNSMap.Lookup` always returned `""` because dnsbpf wrote only the FNV hash into `dns_cache`. User pushed back: hash-only attribution is unactionable for SOC analysts. Resolved by giving netlogger a `Deps.Domains DomainSource` closure wired in CP main to `firewall.Handler.AllResolvableDomains` (new public accessor — enumerates allow-rule domains + internal hosts with the same normalize/filter passes `GenerateCorefile` uses, so the two sources agree by construction). `ReverseDNSMap` rebuilds `hash → domain` every refresh tick from the source. Walk over `dns_cache` stays as a triage signal (emits `event=netlogger_reverse_dns_unattributed` when BPF holds hashes the source can't account for). `domain_hash` dropped from `otelSink.Emit` — SOC queries on `dst_host`, and a bare hash is noise without the string.

**Architecture observation captured for follow-up: clawker's FNV-1a 32-bit domain hash is divergent from both Cilium and Tetragon.** Cilium uses userspace-allocated sequential u32 identities (`pkg/fqdn/namemanager` + IPCache); Tetragon doesn't do per-domain enforcement in BPF at all. clawker is doing the Cilium pattern with a worse identity allocator (FNV has theoretical collisions; sequential allocation does not). Out of scope for this PR — filed as `initiative_route_identity_allocator` Serena memory AND a one-liner in `.serena/memories/bug-tracker.md`. The CP-side reverse map inherits the same collision floor as today's `route_map`; not new risk, just unfixed for now.

**Strict-directive reinterpretation:** User clarified mid-task that netlogger is a SECURITY-TELEMETRY pipeline, not an operator-diagnostic surface. Drop counters (`events_drops`, `ratelimit_drops`) are eBPF subsystem health — they belong on `/metrics` or break-glass CLI, NEVER on the netlogger OTel stream. Saved as `feedback_netlogger_security_only` in MEMORY.md so future agents don't propose adding kernel-drop watchers / log emit goroutines to netlogger. Strict "emit ALL Event fields" directive now applies to per-decision security records only — `domain_hash` was correctly dropped because it's a BPF-side identity handle that's unactionable for SOC at the OTel layer once `dst_host` resolves.

**Files modified on worktree (uncommitted at handoff):**

```
M  cmd/clawker-cp/main.go                                      (Domains wire-up)
M  docs/observability.mdx                                      (Domain Resolution section + attribute table + Current Limitations)
M  internal/controlplane/firewall/ebpf/CLAUDE.md               (ratelimit_state write-source updated)
M  internal/controlplane/firewall/ebpf/bpf/common.h            (ratelimit_refund deleted, submit_event simplified)
M  internal/controlplane/firewall/ebpf/clawker_*_bpfel.{go,o}  (regen)
M  internal/controlplane/firewall/ebpf/netlogger/CLAUDE.md     (ReverseDNSMap section + attribute table)
M  internal/controlplane/firewall/ebpf/netlogger/netlogger.go  (Deps.Domains)
M  internal/controlplane/firewall/ebpf/netlogger/otel_sink.go  (domain_hash removed from emit)
M  internal/controlplane/firewall/ebpf/netlogger/reverse_dns.go (control-plane reverse map)
M  internal/controlplane/firewall/ebpf/netlogger/*_test.go     (signatures, assertions, new tests)
M  internal/controlplane/firewall/handler.go                   (AllResolvableDomains)
M  internal/controlplane/firewall/handler_test.go              (two new tests pinning the accessor)
M  .serena/memories/bug-tracker.md                             (route-identity-allocator follow-up)
?? .serena/memories/initiative_route_identity_allocator.md     (new follow-up initiative)
```

`go build ./...`, `go vet ./...`, and `go test ./internal/controlplane/...` all green. `make ebpf` regenerated bindings cleanly.

**What the next agent must do:**

1. **Read this memory top-to-bottom**, including Tasks 1–4 sections — they describe what already shipped and committed.
2. **Anchor on git state:** `git log --oneline -15`, `git status --short`, `git diff --stat`. Tasks 1–4 are commits `e59730ad..093c6ce8`. Task 5 corrections are sitting unstaged with no commit yet.
3. **Wait for the user to run UAT.** Suggested probes:
   - Allowed domain: `clawker run -it --agent dev @` + `curl https://github.com` → OSD `clawker-ebpf-egress` index should show `verdict=allowed`, `dst_host=github.com`, NO `domain_hash` attribute.
   - Wildcard: `.example.com` allow rule + subdomain resolution → `dst_host=example.com` (normalized).
   - Direct-IP: `curl https://1.1.1.1` → `dst_host=""`, `verdict=denied`.
   - Internal host: `curl http://host.docker.internal:...` → `dst_host=docker.internal`.
   - Bypass: `clawker firewall bypass 30s --agent dev` + curl → `verdict=bypassed`, `dst_host` populated.
4. **Triage findings together with the user.** When UAT passes:
   - Squash Task 5 corrections into ONE commit. Recommended message:
     ```
     refactor(netlogger): match Cilium ratelimit convention + populate dst_host
     
     - yank ratelimit_refund (industry-divergent — Cilium bpf/lib/ratelimit.h
       and Tetragon bpf/process/bpf_rate.h both consume tokens unconditionally
       on bpf_ringbuf_reserve failure; failed reserve is back-off signal,
       not retry-harder). events_drops PERCPU + ratelimit_drops HASH remain
       as two independent drop dimensions.
     - populate dst_host via CP-side reverse map (no new BPF map). netlogger
       Deps.Domains closure over firewall.Handler.AllResolvableDomains;
       ReverseDNSMap rebuilds hash→domain from the firewall rule set on
       each refresh tick. Walk over dns_cache stays as the unattributed-
       hash triage signal.
     - drop domain_hash from otelSink emission. SOC queries on dst_host;
       bare hash is unactionable noise. Strict directive ("emit ALL Event
       fields") narrowed to security-relevant fields per security-only
       reframing.
     - file route-identity-allocator follow-up (initiative_route_identity_
       allocator) for the proper Cilium-pattern fix: replace FNV with
       userspace-allocated sequential identities in dns_cache + route_map.
     ```
   - Push: `git push origin feat/ebpf-logging`.
   - Open PR with the body template from "Task 5" section below (no behavior change to the template — just add a "Pre-UAT corrections" paragraph noting refund yank + dst_host).
5. **Mark Task 5 → `complete`** and hand off the PR URL.

**Do NOT begin any work in `initiative_route_identity_allocator` from this conversation.** That is a separate initiative requiring its own branch and migration story for pinned-map shape changes.

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
2. A BPF-side **token-bucket rate limiter** keyed by `cgroup_id` so a misbehaving container can't monopolize the ringbuf.
3. A BPF-side **kernel-fault drop counter** (`BPF_MAP_TYPE_PERCPU_ARRAY`, 1 entry) bumped when `bpf_ringbuf_reserve` returns NULL.
4. A new userspace subpackage `internal/controlplane/firewall/ebpf/netlogger/` that drains the ringbuf, enriches each event with `{container_id, agent, project, domain}` looked up by `cgroup_id`, and emits as OTLP log records.
5. A generic OTel-client constructor at `internal/controlplane/otelclient.go` so future subsystems (sysexec events, etc.) construct their own `*sdklog.LoggerProvider` against the existing infra OTel receiver without duplicating the SDK wiring.

### What this initiative explicitly does NOT ship

- **No sock_ops / TCP_CLOSE per-connection byte tracking.** This is by design and matches production practice — see "Why decision-time emit is the right scope" below. The event struct intentionally has NO `bytes_in` / `bytes_out` / `duration_ns` fields. Roundtrip data for ALLOWED connections comes from the L7 layer (Envoy access logs); for BYPASSED connections it is genuinely unrecoverable and operators correlate against other sources by 5-tuple.
- **No Envoy access-log OTLP rewiring.** Separate branch.
- **No CoreDNS `log` plugin → filelog receiver pivot.** Separate branch.
- **No OpenSearch backend migration.** Separate branch.
- **No new mTLS certs minted.** netlogger reuses CP's existing `otelcerts.Service.LoadTLSConfig` (per-handshake ephemeral leaf — same path the CP zerolog→OTLP bridge already uses). Same identity, same cert, same gRPC endpoint.
- **Distinct `service.name=ebpf-egress`.** Resource attribute differs from the CP zerolog stream (`service.name=clawker-cp`) so the OpenSearch exporter routes records to a separate data stream by default. This is the right shape because the two streams have materially different operational characteristics: CP zerolog is operator-facing daemon-health, netlogger is per-agent security telemetry; different consumers, different retention, different volume profiles. Identity-layer reuse (cert / gRPC / endpoint) and resource-attribute distinction are independent concerns.
- **Two `*sdklog.LoggerProvider` instances in one process** (one per service.name). Both constructed by the same generic `NewOtelLoggerProvider` helper. Same exporter wiring, same TLS material, different batch processor + retry tuning. Within the netlogger provider, the instrumentation scope name is `clawker.netlogger` and records carry `event.name=ebpf.egress` so future netlogger-emitted event types (e.g. sock-state) can be filtered within the stream.

### Why decision-time emit is the right scope

The BPF record captures the **decision**. **Strict directive: emit ALL possible fields on every record — every field in the BPF `struct egress_event`, plus every field added by userspace enrichment.** Not "all current fields" — ALL possible fields, including any that get added in the future. No discretion. No "this field isn't interesting for this verdict, skip it". When a field has no value for a given event, emit it empty (`""`) or zero (`0`) — never drop it from the record.

Operators decide which fields matter at dashboard / query time. The emitter's job is to make sure every field that exists on the event is present on every record, so consumers never have to guess whether a field was unset versus unsupported.

Per-connection bytes/duration are not in the BPF event by design — they live on the L7 proxy stream (Envoy access logs), emitted independently from that source. Sock_ops state tracking in BPF would (a) double the BPF surface area, (b) introduce a new map keyed by socket cookie with verifier complexity, (c) leave UDP / connectionless flows with no analogous signal, (d) overlap with the L7 proxy's existing access-log emission. We don't do it.

Each record stands on its own:

- DENIED records are intrinsically complete — no traffic flowed, there are no bytes/duration to record.
- BYPASSED records are the headline win — BPF observes traffic even when Envoy and CoreDNS enforcement is skipped. This closes the bypass-mode forensic blind-spot.
- ALLOWED records describe an enforcement decision, not a connection lifecycle. They say "this agent was permitted to reach X" — useful on its own.

Per-connection bytes/duration are a separate concern, sourced from the L7 proxy (Envoy access logs) and not from BPF. Adding sock_ops state tracking to chase byte counts would (a) double the BPF surface area, (b) introduce a new map keyed by socket cookie with verifier complexity, (c) leave UDP / connectionless flows with no analogous signal, (d) overlap with the L7 proxy's existing access-log emission. We don't do it.

**Operator workflow**: query the netlogger stream for the BPF decision record; if richer L7 detail (HTTP method, status, bytes) is needed for the same flow, pivot by 5-tuple to the Envoy access-log stream once that stream exists (separate branch). For BYPASSED connections, only the netlogger record exists — that's the documented limitation of bypass mode and is inherent to bypass semantics.

### Library API notes (read before starting any task)

These are behaviors of the libraries this initiative builds on. Not optional — wiring against these APIs has hard constraints.

**`github.com/cilium/ebpf/ringbuf`**
- `Reader.ReadInto(*Record)` is preferred over `Read()` on a hot path — `Read` allocates a fresh `Record` per call; `ReadInto` reuses the caller's slice.
- `Reader.Read` / `ReadInto` block until a record arrives. Graceful shutdown: call `Reader.Close()` from a separate goroutine — pending Read returns `ringbuf.ErrClosed`.
- `ringbuf.NewReader` rejects a `*ebpf.Map` whose `MaxEntries` is not a power-of-2 multiple of the page size.
- The library does not expose kernel-side drop tracking. `bpf_ringbuf_reserve` returning NULL on a full buffer is invisible to userspace unless the BPF program counts it (PERCPU_ARRAY pattern below).

**`github.com/cilium/ebpf/link`**
- `link.AttachCgroup(CgroupOptions{Path, Attach, Program})` is the attach point for all `cgroup/*` BPF program types — already used by every program in `internal/controlplane/firewall/ebpf/manager.go`.

**`go.opentelemetry.io/otel/sdk/log`**
- `BatchProcessor.OnEmit` is non-blocking — records go into an internal ring buffer; on overflow the **oldest record is dropped** and an internal atomic counter increments. We do NOT need to implement drop semantics ourselves.
- Default `MaxQueueSize=2048`, `ExportInterval=1s`, `ExportTimeout=30s`. Override via `WithMaxQueueSize` / `WithExportInterval` / `WithExportTimeout`.
- The internal drop counter is not exposed as a stable metric. To get a Prom counter, wrap the `sdklog.Exporter` and count `Export` calls (success vs failure).
- `*sdklog.LoggerProvider.Logger(scopeName)` caches per-scope. Safe to call repeatedly; same scope name returns the same instance.
- `LoggerProvider.Shutdown(ctx)` flushes in-flight batches then closes the exporter. Wrap with a deadline context — a hung Shutdown must not block the CP drain path.

**`go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc`**
- Has its own retry layer via `WithRetry(RetryConfig{...})`. Defaults: enabled, `InitialInterval=5s`, `MaxInterval=30s`, `MaxElapsedTime=1min`. The 1-minute default is **too long** for our use case — a dead collector pins the export goroutine and refills the BatchProcessor queue many times over. Override to 10s.
- `otel.SetErrorHandler` is process-global. Each `NewOtelLoggerProvider` call re-sets it; idempotent across providers in the same process. Route to the CP file logger with `event=otel_sdk_error` for operator visibility.

### Existing clawker primitives to reuse (do not reinvent)

| Primitive | Path | Why |
|-----------|------|-----|
| `ebpf.Manager` | `internal/controlplane/firewall/ebpf/manager.go` | Already owns BPF lifetime, pinned maps, `OpenPinned()`. We add accessor methods for the new pinned maps. |
| `overseer.Overseer` bus — for eBPF **enrollment** events + existing `DockerEvent` for removal | `internal/controlplane/overseer/`, `internal/controlplane/dockerevents/` | This initiative adds ONE new event type published by `firewall.Handler`: `EBPFContainerEnrolled{cgroup_id, container_id, occurred_at}`, emitted from `FirewallEnable` after the existing `container_map` write succeeds. Because `firewall.Handler.FirewallInit` already re-enrolls every running managed agent at CP startup (see `handler.go:160-167, 236-242`), this single emit point hydrates netlogger's cache both at startup (via the FirewallInit re-enrollment sweep) and at runtime (each new container enroll). For the **removal half**, netlogger subscribes to the existing `dockerevents.DockerEvent` already published on overseer — container die/destroy is the eviction signal. No `EBPFContainerRemoved` event is added; duplicating the die signal would be redundant. **netlogger touches overseer ONLY for these two event types — `EBPFContainerEnrolled` for enrollment and `DockerEvent` (die/destroy actions) for eviction.** The BPF ringbuf telemetry stream does NOT touch overseer; it goes ringbuf → OTLP via netlogger's own reader. Lifecycle (sparse, per-container, overseer) vs telemetry (dense, per-decision, ringbuf→OTLP) are completely separate. |
| `docker.Client` (existing CP-held instance) | `internal/docker/` | Used once per container at enrollment time to fetch labels: when netlogger receives an `EBPFContainerEnrolled` event, it does ONE `ContainerInspect(container_id)` to extract `dev.clawker.{agent,project}` + the container name, then caches the resolved attribution by `cgroup_id` for the lifetime of that enrollment. BPF events do an O(1) map lookup, never a Docker call. **No client-side response cache is added in this initiative** — the Docker daemon's own in-memory state makes per-enrollment inspects cheap; a userspace cache is unnecessary at this scale. |
| `otelcerts.Service` | `internal/controlplane/otelcerts/otelcerts.go::LoadTLSConfig` | Already in use by CP zerolog→OTLP bridge. `LoadTLSConfig(svc)` returns a `*tls.Config` with `GetClientCertificate` that re-mints per handshake. We call `LoadTLSConfig("netlogger")` — same issuer, same root CA, no new on-disk material. |
| `consts.MonitoringServiceOtelCollector` + `cfg.Settings().Monitoring.OtelInfraPort` | `internal/config/` | OTLP endpoint for trusted-lane infra push. Already routed cross-network. |
| `logger.Logger` | `internal/logger/logger.go` | **Only** for netlogger's own degraded-path structured logs (`event=netlogger_unavailable`, drop-counter periodic summaries). **NEVER** for the network event records themselves — those go direct OTLP via the new `*sdklog.LoggerProvider`. |
| CP no-panic discipline | root `CLAUDE.md` + `internal/controlplane/CLAUDE.md` | Hard rule: no `panic()`, no `log.Fatal()`, no `os.Exit()` from netlogger code path. Constructor returns `(nil, error)`; main degrades to `event=netlogger_unavailable`. Every long-lived goroutine wraps with `defer recover()` — see existing recover-wrapped goroutines in `cmd/clawker-cp/main.go` for the template. |

### Rules

- Read `CLAUDE.md`, `internal/controlplane/CLAUDE.md`, `internal/controlplane/firewall/CLAUDE.md`, and `internal/controlplane/firewall/ebpf/CLAUDE.md` before starting any task.
- Use Serena tools (`mcp__serena__*`) for code navigation — `find_symbol`, `get_symbols_overview`, `find_referencing_symbols`, `replace_symbol_body`. Do not Read whole files to discover symbols.
- Use `internal/config/mocks/configmocks` for config doubles in tests. Use `internal/controlplane/firewall/ebpf/mocks.EBPFManagerMock` for manager mocking.
- Every BPF map change requires running `make ebpf` to regenerate `clawker_*_bpfel.go` bindings. Generated files are gitignored.
- Per `.claude/rules/testing.md`: do NOT run `go test ./...` inside a clawker container (`$CLAWKER_AGENT` set) — the e2e suite tears down the host CP. Use targeted package tests + `make test`.
- Pre-commit hooks (installed via `scripts/install-hooks.sh`) run unit tests automatically. Don't double-run before commit.
- Tests use real implementations as far as possible (`internal/testenv.New(t)`, etc.). Mock only at external boundaries.

### Non-negotiable directive — applies to every task

**No deferrals. No scope reductions. No "we'll add it later" / "follow-up branch" / "v2" / "out of scope for this task" escape hatches. No agent-side decisions to skip, simplify, or postpone any requirement.** Every line item in a task's design, implementation steps, and acceptance criteria lands in this PR.

**When you hit tension, PAUSE WORK AND ASK THE USER.** This is the fastest path. Tension = a requirement that looks infeasible, conflicting requirements, an API that doesn't behave as documented here, a design choice the doc doesn't pin down, an unexpected verifier rejection, an architectural seam that resists clean implementation — anything where you find yourself reaching for a workaround, a simplification, or a "minimal version". Stop. Ask. Wait.

Asking 3-5 questions and waiting 10 minutes for an answer is **dramatically faster** than:
1. Shipping the wrong thing
2. User reviews the diff
3. User catches the silent descope
4. User forces a full rewrite in a fresh context window
5. The fresh-context agent re-derives the answer that one question would have surfaced

The user prefers a mid-task pause over a finished-but-wrong PR. They have explicitly built this loop expecting pauses. Use `AskUserQuestion` whenever you would otherwise make a unilateral call on something this document doesn't already decide.

Incomplete work — missing files, skipped requirements, omitted attribute fields, untested code paths, "I'll add X in a follow-up" framing in commit messages or code comments, design choices made silently — is grounds for the user forcing the entire task to be redone from scratch in a fresh context window. That's the slow path. Asking is the fast path.

The only acceptable scope reductions are the ones already enumerated in "What this initiative explicitly does NOT ship" at the top of this document. Anything else is in scope and must land — or must be raised to the user as a question before any code lands that reflects a different choice.

---

## Task 1: BPF — events_ringbuf + per-decision-point emission + drop counters + kernel rate limiter

> **No deferrals on this task.** Every item in "Creates/modifies", "Implementation steps", and "Acceptance Criteria" below must land in this PR. No "I'll add the rate limiter later", no "drop counters can wait", no skipping the IPv6 branches. If anything blocks you, surface to the user — do not silently descope. Incomplete work means redoing the task from scratch in a fresh context window.

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
//    Buffer must be power-of-2 page-size multiple (ringbuf.NewReader rejects otherwise).
//    Start at 256 KiB = 64 pages × 4 KiB.  Tunable but ratchet up only after observing drops.
//    Single ringbuf (not per-CPU) because we have one userspace reader and the records are
//    tiny (~32 bytes); a single ring keeps the userspace consumer simple.
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

// 3) Rate-limit state — token bucket per cgroup_id.
//    LRU_HASH so dead cgroups evict naturally.  Per-cgroup keying matches the granularity
//    of "noisy agent" we want to throttle.
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
//    (intentional vs ringbuf overflow) demands different operator response.  Key is
//    cgroup_id so userspace can attribute drops to a specific noisy agent.
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
// requiring callers to be explicit.  Be consistent — every emit site swaps once,
// the helper never swaps.
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
    __u8  _pad[3];        // explicit padding — zero-initialized via compound literal
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
    // Racy refill — intentionally non-atomic for hot-path performance.
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

**`lookup_domain_hash_for_ip(dst_ip)`** — uses the existing pinned `dns_cache` (key=IPv4, value={domain_hash, expire_ts}). Returns 0 if not present (direct-IP connect, or DNS cache miss). The existing `dns_cache` GC (already in `Manager.GarbageCollectDNS`) evicts stale entries — no extra work needed.

**Manager.go accessors**:

```go
func (m *Manager) EventsRingbuf() *ebpf.Map      // returns m.objs.EventsRingbuf
func (m *Manager) EventsDrops() *ebpf.Map         // returns m.objs.EventsDrops
func (m *Manager) RatelimitDrops() *ebpf.Map      // returns m.objs.RatelimitDrops
func (m *Manager) DNSCache() *ebpf.Map            // returns m.objs.DnsCache (already exists, just expose)
```

These are read-only access — netlogger never writes to these maps. Returning the raw `*ebpf.Map` is the same shape `LookupContainer` uses internally on the existing Manager.

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

> **No deferrals on this task.** Every file in "Creates", every step in "Implementation steps", every line in "Acceptance Criteria" must land in this PR. No skipping the LabelCache cgroup-id-reuse test, no skipping the reverse-DNS goroutine, no "I'll add the rate limit later", no race-detector skips. If anything blocks you, surface to the user — do not silently descope. Incomplete work means redoing the task from scratch in a fresh context window.

**Creates:**
- `internal/controlplane/firewall/ebpf/netlogger/netlogger.go` — top-level `Service` struct, `New(Deps)`, `Start(ctx)`, `Stop(ctx) error`
- `internal/controlplane/firewall/ebpf/netlogger/event.go` — `Event` struct (enriched), parser using bpf2go type + `binary.NativeEndian`
- `internal/controlplane/firewall/ebpf/netlogger/cache.go` — `LabelCache` cgroup_id → `{container_id, agent, project}`
- `internal/controlplane/firewall/ebpf/netlogger/reverse_dns.go` — periodic `dns_cache` scan, `domain_hash → domain` map under RWMutex
- `internal/controlplane/firewall/ebpf/netlogger/reader.go` — ringbuf drain goroutine
- `internal/controlplane/firewall/ebpf/netlogger/processor.go` — channel consumer goroutine, enriches + emits via `Sink`
- `internal/controlplane/firewall/ebpf/netlogger/sink.go` — `Sink` interface, `nopSink` (for tests), `stdoutSink` (for Task-2 acceptance — replaced by OTel sink in Task 3)
- `internal/controlplane/firewall/ebpf/netlogger/metrics.go` — Prom counters declared but not registered yet (Task 3 wires registration)
- `internal/controlplane/firewall/ebpf/netlogger/CLAUDE.md` — package reference doc
- Test files for each of the above

**Also modifies (lifecycle event emit):**
- `internal/controlplane/overseer/types.go` (or sibling) — new event type `EBPFContainerEnrolled{CgroupID uint64, ContainerID string, OccurredAt time.Time}`. Implements the `overseer.Event` interface alongside existing event types. The type lives in the overseer package because that's where the bus types live; the `firewall.Handler` package PRODUCES instances of it (overseer package owns the type, firewall package owns the emit point — symmetric with how `dockerevents.DockerEvent` is defined in `dockerevents` and produced by the feeder).
- `internal/controlplane/firewall/handler.go` — `FirewallEnable` publishes `EBPFContainerEnrolled` to the overseer bus AFTER the existing `container_map.Update` returns successfully and `Install` returns no error. Publish is fire-and-forget (overseer's pub interface should already be non-blocking; if it isn't, the call site does NOT block the RPC). Nil-bus tolerant — if `HandlerDeps.Bus` is nil (test wiring without overseer), skip the publish. Add a `Bus *overseer.Overseer` (or whatever overseer's exported producer-side type is named) field to `HandlerDeps`.
- `cmd/clawker-cp/main.go` — wire the overseer bus into `HandlerDeps.Bus`. This is the existing CP-process bus instance; no new construction.

**Depends on:** Task 1 (Manager accessors must exist).

### Background — the consumer pipeline shape

A two-goroutine kernel→userspace pipeline with a bounded channel between them. Architecture:

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
        │  queue chan []byte  │  buffered, size 8192 (tunable; sized for clawker's per-agent volume)
        └──────────┬──────────┘
                   │
                   ▼
        ┌─────────────────────┐
        │  processor goroutine│  drains queue, parses + enriches + emits via Sink
        │  processor.go::run  │  bumps QueueReceived / ParseErrors
        └─────────────────────┘
```

**Critical**: the kernel reader MUST NOT block on the channel. A blocked reader stalls the ringbuf which causes upstream `bpf_ringbuf_reserve` failures (counted as kernel-fault drops by Task 1's `events_drops`). On channel-full, drop the newest event via a `select` with a `default` arm and bump the queue-dropped counter.

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

Parser uses the bpf2go-generated `clawkerEgressEvent` type and `binary.NativeEndian`. The bpf2go-generated struct has `structs.HostLayout` so the field offsets match the C ABI exactly. Do NOT define a parallel Go struct.

**`LabelCache`** (in `cache.go`):

```go
// LabelCache resolves cgroup_id to container identity.  Backed by a slice +
// dual-index maps + invalid flag, guarded by a single mutex.  Invalidation
// is event-driven via the dockerevents bus.
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
// frees the slot for recycle.
func (c *LabelCache) EvictByContainerID(containerID string)
```

**Wiring to overseer — enrollment (new) + dockerevents (existing)** — `Service.Start` subscribes to the `overseer.Overseer` bus and filters for:

- `EBPFContainerEnrolled{CgroupID uint64, ContainerID string, OccurredAt time.Time}` — NEW type added by this initiative, emitted by `firewall.Handler.FirewallEnable`. See "Overseer event types" below for the definition site and "Modifications to firewall.Handler" for the emit site.
- `dockerevents.DockerEvent` (already published on overseer today) — netlogger filters for `Type=container, Action ∈ {die, destroy}` to drive cache eviction.

These two are the **only** overseer interactions netlogger has. The eBPF telemetry stream itself never touches overseer.

The handler:
- On `EBPFContainerEnrolled`: do ONE `docker.Client.ContainerInspect(container_id)` to fetch the container's labels and name; store `{cgroup_id → container_id, container_name, dev.clawker.agent, dev.clawker.project}` in the LabelCache. Global-scope agents have an empty project — pass through verbatim. Use raw Docker label values; do NOT synthesize from cgroup name or `AgentFullName`, because the downstream dashboard variable resolution (Prom-sourced) keys on the same raw label strings and any drift means panels go blank.
- On `dockerevents.DockerEvent` with `Type=container` and `Action ∈ {die, destroy}`: evict the entry by container_id. Use the soft-delete pattern (mark invalid + index-remove) so cgroup-id reuse by the kernel cannot return stale labels for a newly-enrolled container.
- `ActionDie` / `ActionStop` / `ActionDestroy`: call `EvictByContainerID(Actor.ID)`.

**Startup hydration is free** — no explicit backfill needed. `firewall.Handler.FirewallInit` already re-enrolls every running managed agent at CP startup (see `handler.go:160-167, 236-242`: "Init re-enrolls every running managed agent it can find. On a cold CP start that follows a previous CP's FlushAll, container_map is empty — without re-enrollment, long-lived agents that outlived the previous CP would egress unenforced"). Each re-enrollment calls `FirewallEnable`, which (after this initiative) emits `EBPFContainerEnrolled`. netlogger subscribes to those events at `Service.Start` and is hydrated naturally by the FirewallInit sweep. Single code path: every `container_map` entry that exists at any moment corresponds to exactly one `EBPFContainerEnrolled` event that netlogger has consumed or will consume.

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
        // Go's small-object allocator handles this well.
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
5. Drop the overseer subscription — `Overseer.Subscribe` returns an unsubscribe func; call it. The lifecycle-event handler goroutine exits when its parent context is cancelled.

### Implementation steps

1. Create `internal/controlplane/firewall/ebpf/netlogger/` directory. Add `CLAUDE.md` skeleton (sections to be filled as files land).
2. Implement `Event` + parser in `event.go`. Reference the bpf2go-generated `clawkerEgressEvent` type from the parent `ebpf` package directly. Test: feed synthetic byte slices (constructed via `binary.Write` against the same struct definition) and assert all fields decode correctly. Verify endianness with a fixed IPv4 example.
3. Implement `LabelCache` in `cache.go` using slice + dual-index + invalid pattern. Unit test concurrent `AddOrUpdate` + `Lookup` + `EvictByContainerID` under race detector (`go test -race`). Specifically test the cgroup-id reuse scenario: AddOrUpdate(cgID=42, contA), EvictByContainerID(contA), AddOrUpdate(cgID=42, contB), Lookup(cgID=42) returns contB labels.
4. Implement `ReverseDNSMap` in `reverse_dns.go`. Test with a fake `*ebpf.Map` (use a test-only `Iterable` interface so tests don't need a real BPF map) or via the existing `ebpf.Map` test seam in `manager_test.go`.
5. Implement reader + processor + Sink interface + nopSink + stdoutSink. Tests use the nopSink to drive the pipeline; stdoutSink test feeds a synthetic event, decodes the JSON output, asserts fields.
6. Top-level `Service` in `netlogger.go`:
   - `Deps` struct: `Mgr *ebpf.Manager`, `Bus *overseer.Overseer` (for `EBPFContainerEnrolled` + existing `dockerevents.DockerEvent` for die/destroy eviction), `Docker docker.Client` (for one inspect per enrollment), `Log *logger.Logger`, `Sink Sink`. Future: `OtelLoggerProvider *sdklog.LoggerProvider` (Task 3 adds).
   - `New(Deps) (*Service, error)`: validate required deps, construct LabelCache, ReverseDNSMap, reader, processor. No goroutines started.
   - `Start(ctx context.Context) error`: subscribe to bus (`EBPFContainerEnrolled` + `dockerevents.DockerEvent`), start reader + processor + reverse-DNS ticker. No explicit backfill — FirewallInit re-enrollment hydrates the cache via the same subscription path. Return nil; degraded paths inside the goroutines per CP no-panic.
   - `Stop(ctx context.Context) error`: drain per the order above.
7. Test the full pipeline end-to-end using a real `ebpf.Map` via the bpf2go test helpers (load the BPF spec in a test, write synthetic events to events_ringbuf via... actually no, ringbuf is kernel-write only from userspace tests. Use a different approach: bypass the ringbuf, call `processor.handle(rawBytes)` directly. Pipeline E2E lands in Task 4.)
8. Write `CLAUDE.md` for the package covering: architecture diagram (reader → channel → processor → sink), the five drop counter dimensions, the LabelCache design (slice + dual-index + invalid flag), the OTel-deferred-to-Task-3 note.

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

> **No deferrals on this task.** Every file in "Creates/modifies", every step in "Implementation steps", every check in "Acceptance Criteria" must land in this PR. No skipping the Prom counter wrap, no skipping the degraded path, no skipping the drain hook ordering, no leaving the generic `NewOtelLoggerProvider` half-built. Every Event-struct field must appear as an OTel attribute on every emitted record — no field omissions. If anything blocks you, surface to the user — do not silently descope. Incomplete work means redoing the task from scratch in a fresh context window.

**Creates/modifies:**
- `internal/controlplane/otelclient.go` — new file, generic `NewOtelLoggerProvider(opts) (*sdklog.LoggerProvider, error)`
- `internal/controlplane/otelclient_test.go` — unit tests for the generic constructor
- `internal/controlplane/firewall/ebpf/netlogger/otel_sink.go` — new file, `otelSink` implementing `Sink` using a `otellog.Logger` from a shared provider
- `internal/controlplane/firewall/ebpf/netlogger/circuit.go` — new file, `circuitExporter` decorating `sdklog.Exporter`: tracks consecutive `Export()` failures, permanently trips after N (default 3), emits `event=netlogger_collector_lost` once on trip, drops all subsequent records on the floor. No background reconnect.
- `internal/controlplane/firewall/ebpf/netlogger/otel_sink_test.go` — uses an in-process `sdklog.Exporter` test double to assert record shape
- `internal/controlplane/firewall/ebpf/netlogger/netlogger.go` — `Deps` gains `OtelLoggerProvider *sdklog.LoggerProvider`
- `cmd/clawker-cp/main.go` — boot wiring + degraded path + drain hook
- `internal/controlplane/firewall/ebpf/netlogger/metrics.go` — register Prom counters (now that we have a Sink that can error)
- `internal/controlplane/CLAUDE.md` — document `otelclient.go` and new netlogger wiring
- `internal/controlplane/firewall/ebpf/netlogger/CLAUDE.md` — add OTel sink section

**Depends on:** Tasks 1 and 2.

### Background — what the OTel SDK gives us for free

From the OTel SDK source:

- `BatchProcessor.OnEmit` is non-blocking. Records go into a ring buffer. **On overflow, the oldest record is dropped and an internal counter increments**. We do not need to implement drop-on-overflow ourselves.
- The internal drop counter is NOT exposed as a stable metric. The pattern is to **wrap the `sdklog.Exporter`** and count `Export()` calls (success vs error) into Prom counters of our own.
- `otlploggrpc.New(WithRetry(...))` defaults to `MaxElapsedTime=1min`. For a dead infra collector this means each batch sits in the exporter for ~1min before failing — refilling the queue dozens of times in that window. **We set `MaxElapsedTime=10s`** so the exporter fails fast.
- `otel.SetErrorHandler` is process-global. Wire it once in `otelclient.go` to route SDK-internal errors through `logger.Logger.Warn` with `event=otel_sdk_error`.

### Collector-unavailable posture (strict requirement)

**Monitoring stack is not always up.** Users may run clawker without `clawker monitor up`. netlogger must NOT spend the lifetime of the CP process retrying connections to a collector that never came up. Behavior:

1. **Startup preflight.** At `NewOtelLoggerProvider` construction time, do a one-shot gRPC dial with a **20-second deadline** against the configured OTLP endpoint, using the supplied `*tls.Config`. If the dial returns an error within that window, return `(nil, error)` from the constructor — the caller (CP main) sees the error, emits `event=netlogger_unavailable`, and netlogger runs with `nopSink`. Telemetry is dropped on the floor for the rest of the CP lifetime. No background reconnect, no periodic retry, no buffering.
2. **Runtime circuit breaker.** Even if startup succeeded, the collector may go down later. Wrap the `sdklog.Exporter` with a circuit-breaker decorator that tracks **consecutive `Export()` failures**. After **N consecutive failures** (start with `N=3`), the breaker permanently trips: subsequent `Export()` calls return `nil` immediately (so the BatchProcessor thinks export succeeded and the queue drains via the natural drop-oldest path), and a single structured log line fires (`event=netlogger_collector_lost`). Once tripped it stays tripped for the rest of the CP lifetime — we do NOT periodically probe to reconnect. The user can restart CP to retry.
3. **No background reconnect / health-check goroutine.** Telemetry availability is binary per-CP-lifetime: either the collector was up at boot and stayed up enough to keep the circuit closed, or netlogger is dropping. The cost of running a reconnect loop forever against a missing collector exceeds the value of the telemetry once it returns.

The circuit-breaker wrapper lives in the netlogger package (it's netlogger's policy, not a general OTel concern), composed onto the generic `*sdklog.Exporter` via `OtelClientOptions.ExporterWrap`. The generic `NewOtelLoggerProvider` in `internal/controlplane/otelclient.go` only owns the preflight dial — it has no opinion on circuit-breaking, because future callers may want different policies.

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

    // ServiceName is the OTel resource attribute service.name.  Distinct
    // emitters in the same binary SHOULD use distinct service.name values
    // when their streams have different operational characteristics
    // (retention, routing, consumer audience).  The CP zerolog bridge uses
    // "clawker-cp"; netlogger uses "ebpf-egress".  Identity-layer
    // reuse (TLS, cert, endpoint) is orthogonal — that's what the
    // TLSConfig + Endpoint fields handle.
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
    // Standard use cases: wrapping with a counting exporter for Prom metrics
    // (netlogger/metrics.go) and wrapping with a circuit-breaker exporter that
    // permanently trips after N consecutive failures (netlogger/circuit.go).
    // Wraps compose — pass a single func that applies multiple decorators in
    // the order the caller wants observability vs failure-tripping.
    ExporterWrap func(sdklog.Exporter) sdklog.Exporter

    // PreflightTimeout caps the startup gRPC dial used to verify the collector
    // is reachable before constructing the BatchProcessor.  If the dial fails
    // within this window, NewOtelLoggerProvider returns an error and the caller
    // degrades the subsystem (no background reconnect, no buffered retry).
    // Default 20s.  Set to zero to skip the preflight entirely (NOT recommended
    // for a monitoring-stack-optional deployment shape).
    PreflightTimeout time.Duration
}

func NewOtelLoggerProvider(opts OtelClientOptions) (*sdklog.LoggerProvider, error) {
    // Validate.  No silent defaults for required fields.
    if opts.Endpoint == ""      { return nil, fmt.Errorf("otelclient: Endpoint required") }
    if opts.TLSConfig == nil    { return nil, fmt.Errorf("otelclient: TLSConfig required") }
    if opts.ServiceName == ""   { return nil, fmt.Errorf("otelclient: ServiceName required") }
    if opts.Log == nil          { return nil, fmt.Errorf("otelclient: Log required") }

    // Preflight: one-shot dial with deadline.  If the collector isn't up at
    // CP boot we fail fast and the caller degrades the subsystem.  No
    // background reconnect loop — telemetry availability is binary per CP
    // lifetime.  See "Collector-unavailable posture" in the design notes.
    preflight := opts.PreflightTimeout
    if preflight == 0 { preflight = 20 * time.Second }
    {
        ctx, cancel := context.WithTimeout(context.Background(), preflight)
        defer cancel()
        creds := credentials.NewTLS(opts.TLSConfig)
        conn, err := grpc.DialContext(ctx, opts.Endpoint,
            grpc.WithTransportCredentials(creds),
            grpc.WithBlock(),
        )
        if err != nil {
            return nil, fmt.Errorf("otelclient: preflight dial %s: %w", opts.Endpoint, err)
        }
        _ = conn.Close()  // we just needed to confirm reachability
    }

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
        // scope name "clawker.netlogger" — discriminates future event types
        // WITHIN the netlogger stream (e.g. if we ever add a sock-state event
        // type, it would share the provider but use a different scope).  The
        // top-level stream separation from CP zerolog comes from this
        // provider's distinct service.name=ebpf-egress resource.
        logger: provider.Logger("clawker.netlogger"),
    }
}

func (s *otelSink) Emit(ctx context.Context, ev Event) {
    var rec otellog.Record
    rec.SetEventName("ebpf.egress")
    rec.SetTimestamp(ev.Timestamp)
    rec.SetObservedTimestamp(time.Now())
    rec.SetSeverity(otellog.SeverityInfo)
    rec.SetSeverityText("INFO")
    rec.SetBody(otellog.StringValue("ebpf egress flow"))

    // Strict directive: every field on the Event struct MUST be added here as an
    // attribute, every time.  If a field is later added to Event (or to the BPF
    // egress_event struct that Event mirrors), this Emit body MUST be updated in
    // the same change to carry it.  No "we'll add it when we need it" — operators
    // can only filter on fields that are present.  Empty/zero values are emitted
    // explicitly; never drop an attribute because its value is the zero value.
    //
    // The block below is a sample matching the field set as of Task 1 — extend it
    // every time the Event struct grows.
    rec.AddAttributes(
        otellog.String("source", "ebpf"),
        otellog.String("verdict", verdictString(ev.Verdict)),
        otellog.String("container_id", ev.ContainerID),
        otellog.String("agent", ev.Agent),
        otellog.String("project", ev.Project),
        otellog.Int64("cgroup_id", int64(ev.CgroupID)),
        otellog.String("dst_ip", ev.DstIP.String()),
        otellog.Int("dst_port", int(ev.DstPort)),
        otellog.String("l4_proto", l4ProtoString(ev.L4Proto)),
        otellog.Bool("ipv6", ev.IsIPv6),
        otellog.Bool("ipv4_mapped", ev.IsMapped),
        otellog.String("dst_host", ev.Domain),
        otellog.Int64("domain_hash", int64(ev.DomainHash)),
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
            // ExporterWrap composes: counting (Prom metrics, outermost) ∘
            // circuit-breaker (collector-lost trip, innermost) ∘ otlploggrpc.
            // Order matters: circuit returns nil on tripped state so the
            // counting wrapper records that as a "success" — desired, because
            // a tripped collector isn't an export error from netlogger's POV,
            // it's a deliberate drop.
            wrap := func(inner sdklog.Exporter) sdklog.Exporter {
                breaker := netlogger.NewCircuitExporter(inner, netlogger.CircuitOptions{
                    FailureThreshold: 3,
                    Log:              log,
                })
                return netlogger.NewCountingExporter(breaker, promCounters)
            }
            provider, err = controlplane.NewOtelLoggerProvider(controlplane.OtelClientOptions{
                Endpoint:            endpoint,
                TLSConfig:           tlsCfg,
                ServiceName:         "ebpf-egress",   // distinct from clawker-cp; lands in its own OS data stream
                MaxQueueSize:        2048,
                ExportInterval:      time.Second,
                ExportTimeout:       30 * time.Second,
                RetryMaxElapsedTime: 10 * time.Second,
                PreflightTimeout:    20 * time.Second,    // dial collector with 20s deadline at boot; fail fast if monitoring stack is down
                Log:                 log,
                ExporterWrap:        wrap,
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

## Task 4: docs

> **No deferrals on this task.** Every CLAUDE.md update, and every Mintlify doc page must land in this PR. No skipping the collector-down test, no "we'll write the docs in a follow-up", no shipping with a green allow-path test but skipping the bypass assertion. If anything blocks you, surface to the user — do not silently descope. Incomplete work means redoing the task from scratch in a fresh context window.

**Creates/modifies:**
- `docs/firewall.mdx` (or new `docs/observability.mdx`) — Mintlify doc covering netlogger
- All CLAUDE.md updates from previous tasks reviewed for consistency

**Depends on:** Tasks 1, 2, 3.

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
- LabelCache design (slice + dual-index + invalid flag) and its dockerevents-driven invalidation
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
# Mintlify docs build
go run ./cmd/gen-docs --doc-path docs --markdown --website
test -f docs/observability.mdx

# All CLAUDE.md files reference netlogger consistently
grep -l netlogger internal/controlplane/CLAUDE.md \
                  internal/controlplane/firewall/CLAUDE.md \
                  internal/controlplane/firewall/ebpf/CLAUDE.md \
                  internal/controlplane/firewall/ebpf/netlogger/CLAUDE.md
```

### What landed (Task 4 — 2026-05-21)

- `docs/observability.mdx` (new) — user-facing Mintlify page covering record shape (every attribute on the otel_sink), routing topology, reliability, trust lane, and known limitations (domain strings empty, Prom not scraped).
- `docs/docs.json` nav — observability slot under Guides between firewall and control-plane.
- Stale-ref fixes across user docs: `docs/{firewall,monitoring,control-plane,architecture,threat-model,quickstart}.mdx`. Pinned BPF map list updated to include the four new netlogger maps. Bypass-mode forensic-coverage callout added to firewall + threat-model. Monitor index list bumped to 6.
- `.claude/docs/{ARCHITECTURE,DESIGN,KEY-CONCEPTS,REPO-STRUCTURE,MONITORING-REFERENCE}.md` + `.claude/rules/monitoring.md` — package table, key concepts table, index/cross-pattern strings, routing topology all carry netlogger.
- `internal/monitor/CLAUDE.md` — bootstrap tree includes `clawker-ebpf-egress.json`, pipeline table has `logs/netlogger` row, trust-block conditional note + index-split rationale + per-sender callout all extended.
- `internal/controlplane/{firewall,overseer,otelcerts}/CLAUDE.md` — `FirewallEnable` documents the `EBPFContainerEnrolled` publish side-effect; overseer documents the new event; otelcerts notes `LoadTLSConfig("netlogger")` as a known in-process consumer.
- **Monitor-stack ingestion routing wiring** (scope discovered during sweep, user approved): `otel-config.yaml.tmpl` gains `resource/netlogger` processor, `opensearch/logs_netlogger` exporter (`logs_index: clawker-ebpf-egress`), `routing/trusted` table entry for `service.name=ebpf-egress`, and `logs/netlogger` pipeline. `index-templates/clawker-ebpf-egress.json` (new) mirrors otel_sink attribute set. ISM `clawker-retention.json` index_patterns + `bootstrap.sh.tmpl` pre-create loop both updated. Local-stack UAT no longer needs operator-side wiring.

### Acceptance Criteria (Task 4)

```bash
# Doc file exists + builds via gen-docs (CLI ref regenerates without warnings)
test -f docs/observability.mdx
go run ./cmd/gen-docs --doc-path docs --markdown --website

# Repo builds
go build ./...

# Monitor + CP unit tests pass
go test ./internal/monitor/... ./internal/controlplane/... ./internal/dnsbpf/...

# Index templates + ISM are valid JSON
jq . internal/monitor/templates/opensearch-bootstrap/index-templates/clawker-ebpf-egress.json
jq . internal/monitor/templates/opensearch-bootstrap/ism-policies/clawker-retention.json

# otel-config has all four wiring points for ebpf-egress
grep -c 'opensearch/logs_netlogger\|logs/netlogger\|resource/netlogger\|ebpf-egress' internal/monitor/templates/otel-config.yaml.tmpl
# Expect ≥7

# Cross-doc consistency (every surface that lists indices includes clawker-ebpf-egress)
grep -L clawker-ebpf-egress \
  docs/monitoring.mdx \
  docs/quickstart.mdx \
  .claude/docs/MONITORING-REFERENCE.md \
  .claude/docs/DESIGN.md \
  .claude/rules/monitoring.md \
  internal/monitor/CLAUDE.md
# Expect empty (every file matches)

# No "five indices" / "all 5 indices" stale strings remain
! grep -rn 'five indices\|all 5 indices' docs/ .claude/ internal/monitor/
```

### Wrap Up (Task 4)

1. Update Progress Tracker: Task 4 → `complete`, add Task 5.  **(done)**
2. Append key learnings — see "Task 4 (2026-05-21)" above.  **(done)**
3. Run completion gate: `code-reviewer`, `comment-analyzer` ran on the doc/template diff; findings folded back in (count fixes, dst_host always-empty, in-template comments). `test-hunter`, `code-simplifier`, `silent-failure-hunter`, `type-design-analyzer` skipped — diff has no code or tests.  **(done)**
4. Commit: `docs(netlogger): docs sweep + monitor-stack ebpf-egress routing`.  **(deferred to Task 5 per user instruction)**

---

## Task 5: UAT + commit + push + PR open

> **Background context is in this memory.** Do not re-derive design. Read this memory top-to-bottom (Tasks 1–4 sections, especially "What landed (Task 4)" and Key Learnings), then read the actual commit history + unstaged diff to anchor what is real. The user will direct you when to commit, when to push, and when to triage UAT findings.

**This task does not modify code or docs until the user gives explicit instruction.** Expect to be in observe-and-triage mode for the early portion.

### Starting state — what you inherit

- Branch `feat/ebpf-logging`, 4 commits ahead of `main`:
  - `feat(ebpf): events_ringbuf + per-decision-point egress event emission`
  - `feat(netlogger): ringbuf reader + label cache + reverse-DNS + processor`
  - `feat(netlogger): otel sink + cp wiring + generic otel client`
  - `chore: final plan` (and a couple of doc-sync chore commits)
- **Unstaged docs/template diff from Task 4 (NOT yet committed)** — `git status --short` will show ~19 modified files + 2 new files (`docs/observability.mdx`, `internal/monitor/templates/opensearch-bootstrap/index-templates/clawker-ebpf-egress.json`). Inspect with `git diff` before acting on anything; this is the load-bearing context for what Task 4 already did.
- Serena memory `initiative_ebpf-netlogger` (this file) — task tracker + design decisions + all key learnings.

### Steps (sequenced — do NOT jump ahead)

1. **Orient.** Read this memory top-to-bottom. Then `git log --oneline -15`, `git status --short`, `git diff` to see what Task 4 staged but didn't commit. Build a mental model of what shipped and what the user is about to UAT.

2. **Wait for user direction.** Do NOT commit, push, or run UAT autonomously. The user will likely:
   - Run UAT against a real OTel collector (typically `make clawker && clawker monitor down --volumes && clawker monitor up && clawker run -it --agent dev @`, then probe netlogger records in OpenSearch at `http://localhost:5601` Discover for index `clawker-ebpf-egress`).
   - Surface any bugs they hit — likely categories: records not landing in `clawker-ebpf-egress` (routing/index template), missing attributes (otel_sink omitted a field), wrong attribute type (index template mapping mismatch), netlogger degraded at CP boot (`event=netlogger_unavailable` in `~/.local/share/clawker/logs/clawker.log`), drain hangs (Stop ordering), kernel-side drops (`events_drops` / `ratelimit_drops` non-zero).

3. **Triage each reported bug.** For every finding, follow this loop:
   - Reproduce the symptom independently if you can. Match the user's observation against `cmd/clawker-cp/main.go` (boot + drain), `internal/controlplane/firewall/ebpf/netlogger/` (pipeline), `internal/monitor/templates/otel-config.yaml.tmpl` (routing), and the relevant index template.
   - Identify root cause. Prefer "fix the wiring/code" over "document the limitation".
   - Apply the smallest correct fix. Stay within the surface area Task 4 touched unless the bug is in Tasks 1–3 (in which case fix there and call it out).
   - Re-run targeted tests + offer the user a re-UAT.

4. **Commit when user approves.** Once UAT passes:
   - Single commit covering all Task 4 changes (docs + monitor wiring + index template + ISM + bootstrap + any UAT triage fixes). Conventional message:

     ```
     docs(netlogger): observability page + monitor-stack ebpf-egress routing

     - new docs/observability.mdx covering record shape, routing topology,
       reliability posture, trust lane, known limitations
     - monitor-stack OTel collector + OpenSearch bootstrap learn to route
       service.name=ebpf-egress into the new clawker-ebpf-egress index
       (new index template + ISM + bootstrap pre-create + routing/trusted
       table + opensearch/logs_netlogger exporter + resource/netlogger
       processor + logs/netlogger pipeline) so out-of-box `clawker monitor
       up` UAT lands records without operator-side wiring
     - stale-ref sweep across docs/, .claude/, and package CLAUDE.md files
       — six indices everywhere, netlogger callouts in firewall + threat
       model + control-plane + architecture, bypass forensic-coverage note
       added to the firewall + threat-model guides
     ```

   - Pre-commit hooks will run unit tests automatically. Don't manually run `make test` beforehand.

5. **Push when user approves.**
   - `git push origin feat/ebpf-logging` (origin should already be set; `gh pr list --head feat/ebpf-logging` to confirm whether a PR exists).

6. **Open / update PR when user approves.**
   - PR title: `feat(ebpf): network event emitter (netlogger)`.
   - PR body template:
     ```
     ## Summary
     - Per-decision-point eBPF egress event emitter (netlogger) drains a new
       BPF ringbuf populated at every cgroup connect/sendmsg/sock_create
       hook, enriches by cgroup_id via overseer enrollment events, and
       emits OTLP log records on the trusted infra lane with
       service.name=ebpf-egress.
     - Closes the bypass-mode forensic blind spot — verdict=bypassed
       records carry the same attribution + 4-tuple + domain as
       verdict=allowed / verdict=denied.
     - Local monitoring stack auto-routes the new stream into the
       clawker-ebpf-egress OpenSearch index with retention + mappings
       preconfigured by the bootstrap one-shot.

     ## Changes
     - BPF: events_ringbuf + per-decision-point emit + drop counters +
       per-cgroup token-bucket rate limiter; reworked enter_enforced into
       a tri-state enum so callers can emit BYPASSED records.
     - netlogger package: ringbuf reader → bounded queue → processor
       pipeline; LabelCache (slice + dual-index + invalid flag) hydrated
       by overseer EBPFContainerEnrolled + evicted by dockerevents
       die/destroy; ReverseDNSMap (hash-only today, string follow-up
       tracked).
     - controlplane.NewOtelLoggerProvider (new generic constructor) +
       netlogger OTel sink + circuit breaker (3 consecutive failures,
       permanent until CP restart); preflight TLS dial with 20s deadline;
       degraded paths emit event=netlogger_unavailable.
     - CP main boot wiring + drain ordering (netlogger.Stop before
       ebpfMgr.FlushAll so the ringbuf drains and the BatchProcessor
       flushes before BPF maps go away).
     - Docs sweep + monitor-stack ingestion routing wiring.

     ## What's NOT in this PR (tracked separately)
     - Reverse-DNS string population (dnsbpf-side map for hash → string)
     - Native OTLP Envoy access logs correlated by 5-tuple
     - CoreDNS log plugin → filelog receiver pivot
     - OpenSearch backend migration

     ## Test plan
     - [ ] go build ./... clean
     - [ ] go test ./internal/controlplane/... ./internal/monitor/... pass
     - [ ] make ebpf regenerates clean
     - [ ] make clawker && clawker monitor down --volumes && clawker
           monitor up && clawker run -it --agent dev @ ; from a separate
           shell, probe netlogger records in OSD at
           http://localhost:5601 (Discover, index clawker-ebpf-egress) —
           verify verdict=allowed records on whitelisted domains,
           verdict=denied on blocked, verdict=bypassed under
           `clawker firewall bypass 30s --agent dev`
     - [ ] CP drain via container stop completes within 5s without
           losing in-flight ringbuf records
     ```
   - Body via HEREDOC so formatting is preserved (see root `CLAUDE.md` PR-creation template).

7. **Hand off to user with the PR URL.** No further work; mark Task 5 → `complete`.

### Acceptance Criteria (Task 5)

- All four commits are on `feat/ebpf-logging` pushed to origin.
- PR is open against `main` with the full body template above.
- User confirms UAT records visible in `clawker-ebpf-egress` with the expected attribute set across all three verdicts.
- No regressions in `go test ./...` (unit only — do NOT run `go test ./...` inside the container; e2e suite tears down host CP).

---

## Appendix — Out-of-scope items captured for follow-up branches

1. **Domain string population.** Today's reverse-DNS only has hashes. Add a `domain_strings` map (key=domain_hash, value=fixed-size char array or string-table reference) populated by CoreDNS dnsbpf plugin so netlogger's `ReverseDNSMap` can return real strings instead of empty.
2. **Envoy native OTLP access logs.** Reuses the same generic `NewOtelLoggerProvider` (now in CP). Envoy emits directly to the infra collector — no CP-side reader. This is the byte-count source for ALLOWED L7 flows; pairs with netlogger by 5-tuple at query time.
3. **CoreDNS log plugin → filelog receiver.** Replaces the current coredns `otel` plugin's gRPC push with a stdout-tailed filelog receiver.
4. **OpenSearch backend migration.** The netlogger emits to whatever OTel collector is configured — backend choice is orthogonal.

These form a coherent next initiative if the broader SIEM pivot is taken on later.

**Not on this list (intentionally):** sock_ops / TCP_CLOSE roundtrip byte tracking. See "Why decision-time emit is the right scope" in the Context section — bytes/duration belong to the L7 proxy stream, not the BPF decision stream. Doing it in BPF doubles the surface area and overlaps the Envoy access-log emission.

