# Route Identity Allocator (architectural follow-up to ebpf-netlogger)

**Status:** IMPLEMENTED 2026-07-23 on branch feat/ebpf-dns-hash (uncommitted). Sticky persisted IdentityAllocator + full rename + seeded-precedence source flag + GC zombie sparing + bpftest prog-run harness (`make test-bpf` + CI bpf job) + identity e2e (churn/CP-restart pinned-IP curls) + docs sweep all done; acceptance grep (FNV|DomainHash|domain_hash) = zero in product code + docs. Unit suite green (6163). PENDING host actions: `make ebpf` (pinned-toolchain binding regen — local regen used container clang 14), `make clawker` (stale embeds), restart host CP, run e2e + `make test-bpf`. QUIC-past-TTL + upgrade-boot recovery intentionally NOT e2e'd (covered by unit dnsEntryEvictable + prog-run expired-entry test + Load() schema-mismatch flush via the dns_entry 8→12B value change); flagged for user triage. Filed 2026-05-21 from netlogger UAT triage.

## Problem

`internal/controlplane/firewall/ebpf/types.go::DomainHash` returns FNV-1a 32-bit of the lowercased domain. That hash is the key both BPF and userspace use to talk about "which domain" — `dns_cache[ip] = {domain_hash, expire_ts}`, `route_map[{domain_hash, port}] = envoy_port`, `dnsbpf` writes it, `firewall.Handler.FirewallSyncRoutes` writes it, netlogger emits it on every record.

The collision space is 32 bits. For tens of firewall-rule domains the probability is astronomically low. But it's a non-zero floor that production-grade eBPF stacks have engineered around for a reason.

## What battle-tested stacks do

**Cilium** does NOT hash domains in BPF. The pattern (verified via DeepWiki against `pkg/fqdn/dnsproxy/proxy.go`, `pkg/fqdn/namemanager/manager.go`, `pkg/policy/selectorcache.go`):

1. Userspace L7 DNS proxy intercepts queries.
2. NameManager maintains FQDN → IP mappings.
3. IPCache assigns sequential `u32` **security identities** (allocated, not derived).
4. BPF map keyed on IP → identity; policy lookup on identity → action.
5. Quote from selectorcache.go: *"there is nothing intrinsic about an IP that says that it corresponds to a given FQDNSelector; this relationship is contained only via DNS responses, which are handled externally."*

**Tetragon** does not do per-domain enforcement in BPF at all. DNS is purely userspace (`pkg/sensors/tracing/generickprobe.go::dnsLookup`).

clawker's design = Cilium's pattern with a worse identity allocator. The fix is to replace the FNV derivation with a userspace-allocated `u32`.

## Scope

| File | Change |
|------|--------|
| `internal/controlplane/firewall/ebpf/types.go` | Delete `DomainHash` entirely. Replace with `IdentityAllocator` (sequential u32 minted at firewall rule registration). Owned by `firewall.Handler`. |
| `internal/controlplane/firewall/handler.go` | Allocate identity per rule on `FirewallAddRules` / startup; expose `IdentityFor(domain) (u32, bool)`. `FirewallSyncRoutes` writes `route_map[{identity, port}]`. |
| `internal/dnsbpf/dnsbpf.go` | dnsbpf no longer computes a hash. On A-record arrival, look up the identity from CP-injected `IdentityResolver` (dnsbpf-side reverse: domain → identity). Write `dns_cache[ip] = {identity, expire_ts}`. dnsbpf gets a new boot dependency on CP-mounted identity table OR an admin-side push via the same `Stack.Reload` cycle. |
| `internal/controlplane/firewall/ebpf/netlogger` | ReverseDNSMap source flips from "hash → string via firewall rules" to "identity → string via firewall.Handler.IdentityFor". One source of truth. |
| `bpf/common.h` | `dns_entry.domain_hash` → `dns_entry.identity` (rename only; same u32 shape). `route_key.domain_hash` → `route_key.identity`. `lookup_domain_hash_for_ip` → `lookup_identity_for_ip`. |
| Pinned-map migration | Existing `route_map` / `dns_cache` entries become stale on the boot following the change. Clean: `ebpf.Manager.Load` already removes mismatched-schema maps. Less clean: a value-shape change with same key shape would silently load old data. Use `LIBBPF_PIN_BY_NAME` + version suffix (`route_map_v2`) OR a one-shot flush on Manager startup for the first boot after the rename. |

## Side effects

- `dst_host` follow-up: the userspace already holds `identity → domain` so the netlogger reverse map is a direct read, not a hash inversion. The CP-side reverse-from-rules workaround shipped on `feat/ebpf-logging` can be ripped out.
- The `DomainSource` Deps field on netlogger goes away (replaced by `firewall.Handler.IdentityFor`).
- `route_map` lookups in BPF stay O(1); no perf delta.

## Migration considerations — DECIDED 2026-07-23 (branch feat/ebpf-dns-hash)

**Option (a) deterministic allocation is REJECTED — it is a latent bug, worse than the FNV collision it replaces.** `dns_cache` is pinned and populated asynchronously by CoreDNS; it is NOT rewritten on reconcile. Deterministic sort-and-number renumbers identities on any rule add/remove → pinned dns_cache entries map old IPs to reassigned identities → cross-domain misroute on EVERY sort-shifting rule edit (vs 1e-6 collision odds). Cilium's stickiness machinery (refcounted allocator, round-robin next-free, withheld set on restart — pkg/identity/cache/local.go) exists precisely for this.

**Adopted design (verified against cilium source 2026-07-23, sparse files in scratchpad + api.github.com contents API):**
- Sticky persisted allocator: identity table on disk in FirewallDataSubdir (CP-owned, via internal/storage), NOT in user-editable egress-rules.yaml. Allocate on rule add, release on last-rule-removed (refcount = rules sharing dst), round-robin next-free over [256, 2^32), 0 = "no identity", 1-255 reserved for future well-known. Round-robin wrap = no premature reuse.
- dnsbpf identity delivery: Corefile `dnsbpf <identity>` directive arg per zone (coredns_config.go writes it; zone+identity atomic through Stack.Reload).
- minTTL clamp in dnsbpf Update (cilium NewDNSCache(minTTL) analog; today only TTL=0→60s).
- Seeded-precedence: SyncRoutes-seeded (IP-literal rule) dns_cache entries must not be overwritten by DNS-derived dnsbpf writes (cilium ipcache source-precedence analog, 2 levels). Likely a source flag in dns_entry (value-size change → free clean migration via Load() schema-mismatch removal).
- UDP zombie analog: GarbageCollectDNS spares expired entries whose IP has live flows in udp_flow_map (clawker's conntrack analog; BPF never checks expire_ts — userspace GC is sole expiry enforcement, so GC is the single insertion point). Protects long QUIC streams.
- Scope bits (top-8) N/A: single node, single allocator (documented reason, not work avoidance). perHostLimit deferred.

**Test strategy (cilium-pattern, approved 2026-07-23):**
1. Go unit: allocator invariants — churn/stickiness (THE renumbering regression test), 5000-domain uniqueness, persistence round-trip, refcount, no-premature-reuse, reserved band.
2. BPF prog-run suite (NEW INFRA, cilium bpf/tests pattern): test progs #include production common.h, seeded real maps, ebpf prog.Run() (BPF_PROG_TEST_RUN) asserting verdict/ringbuf/map side effects. make test-bpf + CI privileged job. UNKNOWN to probe: sock_addr prog_run kernel support; fallback = runnable-type test progs calling production decision helpers directly (identical coverage).
3. Privileged Go tests (build tag): GC liveness sparing, SyncRoutes, UpdateDNSCache against real unpinned kernel maps.
4. E2E: FNV-collision pair (must fail on main first), rule-churn live, CP restart identity stability, QUIC mid-flow survival, upgrade-boot stale-FNV recovery.
UAT demoted to smoke only.

## Acceptance bar

- `grep -rn "FNV\|DomainHash\|domain_hash" internal/ docs/ .claude/` returns zero hits in product code.
- `route_map` and `dns_cache` keys/values use `identity` naming end-to-end.
- BPF + userspace + dnsbpf all read identities from the same allocator surface.
- Collision-test (5000 random domains, allocate identities, assert uniqueness) passes.
- E2E test reproduces a deliberate FNV-collision pair (we can construct one in 2^16 tries) and asserts the post-allocator behavior keeps both rules independent — this would fail today on `feat/ebpf-logging`.

## Not in scope

- Replacing CoreDNS-as-DNS-proxy with a Cilium-style L7 DNS proxy. We keep CoreDNS; just the identity allocation moves out of FNV-land.
- Wildcard / regex policy matching à la Cilium's `matchPattern`. Out of scope; the allocator just needs to handle exact + leading-dot wildcards the same way `normalizeDomain` does today.
