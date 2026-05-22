# Route Identity Allocator (architectural follow-up to ebpf-netlogger)

**Status:** Proposed. Not started. Filed 2026-05-21 from netlogger UAT triage.

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

## Migration considerations

- The identity allocator must persist across CP restarts so `route_map` keys stay stable. Either: (a) deterministic allocation (sort rules by `RuleKey`, assign 0..N) — boot recomputes the same identities for the same rule set; or (b) write the allocation to `egress-rules.yaml` alongside the rule (sticky identity that survives rule reorder).
- (a) is simpler but a removed-then-re-added rule could shift identities under it — fine for `FirewallReload` since SyncRoutes rewrites the whole map anyway.
- (b) is sticky but adds on-disk state.
- Recommend (a) initially. (b) becomes necessary if/when identity stability across rule churn matters for audit (it might — netlogger record `identity=42` last week vs this week should mean the same domain).

## Acceptance bar

- `grep -rn "FNV\|DomainHash\|domain_hash" internal/ docs/ .claude/` returns zero hits in product code.
- `route_map` and `dns_cache` keys/values use `identity` naming end-to-end.
- BPF + userspace + dnsbpf all read identities from the same allocator surface.
- Collision-test (5000 random domains, allocate identities, assert uniqueness) passes.
- E2E test reproduces a deliberate FNV-collision pair (we can construct one in 2^16 tries) and asserts the post-allocator behavior keeps both rules independent — this would fail today on `feat/ebpf-logging`.

## Not in scope

- Replacing CoreDNS-as-DNS-proxy with a Cilium-style L7 DNS proxy. We keep CoreDNS; just the identity allocation moves out of FNV-land.
- Wildcard / regex policy matching à la Cilium's `matchPattern`. Out of scope; the allocator just needs to handle exact + leading-dot wildcards the same way `normalizeDomain` does today.
