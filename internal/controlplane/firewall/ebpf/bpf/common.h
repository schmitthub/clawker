// SPDX-License-Identifier: GPL-2.0
//
// GPL-2.0 is required here (see clawker.c licensing note) because the BPF
// helpers invoked from files that #include this header are kernel-gated to
// GPL-licensed programs. The rest of the clawker repository is MIT-licensed.
//
// common.h — Shared types, maps, and routing helpers for clawker eBPF
// programs.
//
// Header strategy: clawker's BPF programs only touch stable kernel UAPI
// (struct bpf_sock_addr, struct bpf_sock, BPF_MAP_TYPE_*, LIBBPF_PIN_BY_NAME)
// and use no CO-RE relocations (no BPF_CORE_READ, no preserve-access-index).
// So we pull the needed types from the pinned Linux UAPI header set
// (<linux/bpf.h>, <linux/types.h>) instead of a committed vmlinux.h dump.
// The UAPI pin is anchored via the linux-libc-dev package version in the
// pinned builder image — see internal/ebpf/REPRODUCIBILITY.md.
//
// All BPF maps are shared across programs via pinning to /sys/fs/bpf/clawker/.
// The Go userspace code (internal/ebpf/) mirrors these struct layouts exactly.

#ifndef __CLAWKER_COMMON_H
#define __CLAWKER_COMMON_H

#include <stdbool.h>
#include <linux/types.h>
#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

// Socket option constants — stable kernel ABI, not pulled in by <linux/bpf.h>.
#ifndef SOL_SOCKET
#define SOL_SOCKET 1
#endif
#ifndef SO_MARK
#define SO_MARK 36
#endif

// Socket type constants — normally from <linux/net.h>/<sys/socket.h> but
// those headers are userspace-oriented and don't play well with -target bpf.
// These values are the stable kernel ABI (enum sock_type in linux/net.h).
#ifndef SOCK_STREAM
#define SOCK_STREAM 1
#endif
#ifndef SOCK_DGRAM
#define SOCK_DGRAM 2
#endif
#ifndef SOCK_RAW
#define SOCK_RAW 3
#endif

// ---------------------------------------------------------------------------
// Routing constants
// ---------------------------------------------------------------------------

// DNS port — referenced by connect/sendmsg/recvmsg DNS redirect paths.
#define DNS_PORT 53

// Docker's embedded DNS resolver, reachable from every container at this
// fixed address. sendmsg/recvmsg rewrite the source/dest to this so the
// application socket accepts the DNS response as if it came from Docker.
// Value is host byte order — callers bpf_htonl() before assigning to ctx.
#define DOCKER_EMBEDDED_DNS 0x7f00000b // 127.0.0.11

// IPv4-mapped IPv6 prefix — the third 32-bit word of ::ffff:x.x.x.x is
// 0x0000ffff in host byte order. Used by is_ipv4_mapped() to detect
// dual-stack sockets that carry IPv4 traffic over an AF_INET6 socket.
#define IPV4_MAPPED_PREFIX 0x0000ffff

// Socket mark used by Envoy/CoreDNS upstream connections for loop prevention.
// The connect4/connect6 programs skip redirect for marked traffic so Envoy's
// own outbound requests don't get bounced back to itself.
// Envoy sets this via upstream_bind_config.socket_options SO_MARK.
#define CLAWKER_MARK 0xC1A4 // "CLA4" — clawker IPv4 mark

// ---------------------------------------------------------------------------
// Map value structs
// ---------------------------------------------------------------------------

// Per-container network configuration. Populated by eBPF Manager on Enable().
struct container_config {
	__u32 envoy_ip;        // Envoy static IP (network byte order)
	__u32 coredns_ip;      // CoreDNS static IP (network byte order)
	__u32 gateway_ip;      // clawker-net gateway IP (network byte order)
	__u32 net_addr;        // clawker-net network address (network byte order)
	__u32 net_mask;        // clawker-net subnet mask (network byte order)
	__u32 host_proxy_ip;   // Host proxy IP (network byte order)
	__u16 host_proxy_port; // Host proxy port (host byte order)
	__u16 egress_port;     // Envoy egress listener port (host byte order)
};

// DNS cache entry: resolved IP → domain identity.
// Written by the CoreDNS dnsbpf plugin on every resolution; read by userspace
// garbage collection (internal/ebpf Manager.GarbageCollectDNS). The BPF fast
// path (clawker.c) only uses domain_hash for routing and does NOT check
// expire_ts — expiration is enforced exclusively by userspace GC.
struct dns_entry {
	__u32 domain_hash; // FNV-1a hash of normalized domain
	__u32 expire_ts;   // Wall-clock expiration: time.Now().Unix() + TTL seconds
};

// Global per-domain TCP route key (shared across all enforced containers).
// Presence in container_map determines enforcement; route_map is global.
struct route_key {
	__u32 domain_hash; // Matches dns_entry.domain_hash
	__u16 dst_port;    // Original destination port (host byte order)
	__u16 _pad;
};

// TCP route value: which Envoy listener to route to.
struct route_val {
	__u16 envoy_port; // Target Envoy TCP listener port (host byte order)
	__u16 _pad;
};

// ---------------------------------------------------------------------------
// Pinned BPF maps — shared across all programs via /sys/fs/bpf/clawker/
// ---------------------------------------------------------------------------

// container_map: cgroup_id → container_config
// Presence in this map means "this container is managed by clawker firewall."
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 256);
	__type(key, __u64); // cgroup_id
	__type(value, struct container_config);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} container_map SEC(".maps");

// bypass_map: cgroup_id → u8 flag (1 = bypassed)
// Set during temporary bypass, deleted on re-enable.
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 256);
	__type(key, __u64); // cgroup_id
	__type(value, __u8);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} bypass_map SEC(".maps");

// dns_cache: resolved IP → domain identity + TTL
// Written by CoreDNS plugin, read by connect4 for per-domain routing.
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 16384);
	__type(key, __u32); // IP address (network byte order)
	__type(value, struct dns_entry);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} dns_cache SEC(".maps");

// route_map: {domain_hash, dst_port} → envoy_port
// Global TCP routing table shared by all enforced containers.
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 8192);
	__type(key, struct route_key);
	__type(value, struct route_val);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} route_map SEC(".maps");

// ---------------------------------------------------------------------------
// Metrics maps — counters read by eBPF Manager for Prometheus export
// ---------------------------------------------------------------------------

struct metric_key {
	__u64 cgroup_id;
	__u32 domain_hash;
	__u16 dst_port;
	__u8  action; // 0=allow, 1=deny, 2=bypass
	__u8  _pad;
};

struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_HASH);
	__uint(max_entries, 16384);
	__type(key, struct metric_key);
	__type(value, __u64); // counter
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} metrics_map SEC(".maps");

enum action {
	ACTION_ALLOW  = 0,
	ACTION_DENY   = 1,
	ACTION_BYPASS = 2,
};

// ---------------------------------------------------------------------------
// Egress event channel — per-decision-point ringbuf for userspace netlogger
// ---------------------------------------------------------------------------

// Egress verdict — written into struct egress_event.verdict.
enum egress_verdict {
	EGRESS_VERDICT_ALLOWED  = 0,
	EGRESS_VERDICT_DENIED   = 1,
	EGRESS_VERDICT_BYPASSED = 2,
};

// Egress flags — written into struct egress_event.flags as a bitmask.
// Bits 0-2: address-shape discriminator (mutually exclusive use, but
//   IPV4_MAPPED can co-occur with the others as an annotation).
// Bits 3-4: emit_site enum — which BPF program submitted the event.
//   Lets userspace derive event.name (connect / sendmsg / sock_create)
//   without growing the struct. 2 bits = 4 values, current 3 used.
enum egress_flags {
	EGRESS_FLAG_IPV6        = 1 << 0, // native IPv6 (not IPv4-mapped); dst_ip carries 16-byte v6 addr
	EGRESS_FLAG_IPV4_MAPPED = 1 << 1, // ::ffff:x.x.x.x; dst_ip carries the v4 in first 4 bytes
	EGRESS_FLAG_NO_DST      = 1 << 2, // sock_create — no destination exists; dst_ip is zero

	// emit_site enum (bits 3-4). Helpers OR these into flags.
	EGRESS_EMIT_CONNECT     = 0 << 3, // clawker_connect4 / clawker_connect6
	EGRESS_EMIT_SENDMSG     = 1 << 3, // clawker_sendmsg4 / clawker_sendmsg6
	EGRESS_EMIT_SOCK_CREATE = 2 << 3, // clawker_sock_create
	EGRESS_EMIT_MASK        = 3 << 3,
};

// egress_event ABI — 48 bytes total. All padding is explicit so the
// compound-literal zero-init in submit_event_* helpers leaves no
// uninitialized bytes on the ringbuf wire.
//
// Address representation follows the Cilium / Tetragon convention: a
// single flat 16-byte slot carries either the IPv4 destination in the
// first 4 bytes (rest zero) or the full IPv6 destination in all 16
// bytes. EGRESS_FLAG_IPV6 / EGRESS_FLAG_IPV4_MAPPED / EGRESS_FLAG_NO_DST
// in `flags` discriminate the three cases.
//
// Endianness convention (referenced from netlogger Go parser):
//   ts_ns, cgroup_id, domain_hash, dst_port, verdict, flags, l4_proto —
//     host byte order.
//   dst_ip — network byte order (matches ctx->user_ip4 / ctx->user_ip6
//     and the ContainerConfig IP fields in this codebase).
// Callers MUST bpf_ntohs() ctx->user_port before passing dst_port; the
// submit_event_* helpers never swap. Pick-one-side keeps every emit
// site explicit and prevents double-swap bugs.
struct egress_event {
	__u64 ts_ns;       // bpf_ktime_get_ns()
	__u64 cgroup_id;   // trust anchor — userspace cache key
	__u8  dst_ip[16];  // network byte order; v4 in [0..3] + zeros, v6 in [0..15], zero when NO_DST
	__u32 domain_hash; // 0 if no DNS resolution (direct-IP / no cache hit / v6 / no_dst)
	__u16 dst_port;    // host byte order (caller swapped); 0 when NO_DST
	__u8  verdict;     // enum egress_verdict
	__u8  flags;       // enum egress_flags bitmask
	__u8  l4_proto;    // SOCK_STREAM / SOCK_DGRAM / SOCK_RAW
	__u8  _pad[7];     // explicit padding — zero-initialized; aligns total to 48
};

_Static_assert(sizeof(struct egress_event) == 48, "egress_event must be 48 bytes");

// enter_state — return value of enter_enforced. Distinguishes
// ENTER_BYPASSED from ENTER_NOT_MANAGED so the bypass path is surfaced
// to callers, which emit a submit_event_v4/v6/nodst(BYPASSED) record
// before allowing.
enum enter_state {
	ENTER_NOT_MANAGED = 0, // not in container_map, fast-return
	ENTER_BYPASSED    = 1, // managed but bypassed; caller emits + allows
	ENTER_ENFORCED    = 2, // managed, proceed with routing decision
};

// events_ringbuf carries a fixed-size struct egress_event for every
// allow/deny/bypass decision in the cgroup programs. Userspace
// (internal/controlplane/firewall/ebpf/netlogger) drains it and emits
// each record as an OTLP log to the infra collector.
//
// 256 KiB total (64 × 4 KiB pages) — must be a power-of-2 multiple of the
// page size or cilium/ebpf's ringbuf.NewReader rejects it. Sized for one
// userspace reader handling records of 48 bytes; dial up only after
// observing kernel-fault drops in events_drops.
//
// __type(value, struct egress_event) is the BTF anchor for bpf2go's
// -type extraction. Ringbuf maps have no key/value shape the kernel
// verifier inspects, but the BTF entry it produces is what bpf2go reads
// to emit the Go-side clawkerEgressEvent struct. Without this line,
// `-type egress_event` fails with "collect C types: not found".
// events_ringbuf is intentionally NOT pinned. The ringbuf is a transient
// queue between BPF producers and the in-process netlogger consumer; it
// holds no load-bearing enforcement state. Pinning would survive across
// CP restarts and carry stale records produced by a previous CP's ABI
// (BPF_MAP_TYPE_RINGBUF reports KeySize=ValueSize=0, so the schema-
// change detector in manager.Load() cannot see ABI drift). Each CP boot
// creates a fresh ringbuf; in-flight records are discarded on shutdown,
// which is the desired property because old producers are detached on
// the same transition.
struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 256 * 1024);
	__type(value, struct egress_event);
} events_ringbuf SEC(".maps");

// events_drops: kernel-fault drop counter. Bumped when bpf_ringbuf_reserve
// returns NULL (buffer full). PERCPU_ARRAY single-slot (key always 0)
// to avoid contention on the hot path; this counter has no per-cgroup
// or per-cause dimension. Userspace reads index 0 and sums across CPUs.
// Not pinned for the same reason as events_ringbuf — it's a per-CP-lifetime
// counter, not enforcement state.
struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
	__type(key, __u32);
	__type(value, __u64);
	__uint(max_entries, 1);
} events_drops SEC(".maps");

// ratelimit_state: token bucket per cgroup_id. LRU_HASH so dead cgroups
// evict naturally without a userspace sweep. Bucket inaccuracy under
// racing CPUs is cheaper than the cmpxchg cost; non-atomic refill is
// intentional.
struct ratelimit_state_val {
	__u64 last_topup_ns;
	__u64 tokens;
};
struct {
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__type(key, __u64);
	__type(value, struct ratelimit_state_val);
	__uint(max_entries, 1024);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} ratelimit_state SEC(".maps");

// ratelimit_drops: intentional drop counter keyed by cgroup_id.
// Distinct from events_drops because the per-cgroup dimension is what
// makes "noisy agent" attributable — userspace can iterate this map to
// name the offending agent. events_drops is a single global counter
// for "ringbuf full" without sub-attribution. Operator response differs
// per class, so the two counters never share a storage location.
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__type(key, __u64);
	__type(value, __u64);
	__uint(max_entries, 256);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} ratelimit_drops SEC(".maps");

// ---------------------------------------------------------------------------
// Shared leaf helpers
// ---------------------------------------------------------------------------

// Increment a per-CPU counter in the metrics map.
static __always_inline void metric_inc(__u64 cgroup_id, __u32 domain_hash,
				       __u16 dst_port, __u8 action)
{
	struct metric_key key = {
		.cgroup_id = cgroup_id,
		.domain_hash = domain_hash,
		.dst_port = dst_port,
		.action = action,
	};
	__u64 *val = bpf_map_lookup_elem(&metrics_map, &key);
	if (val) {
		// metrics_map is BPF_MAP_TYPE_PERCPU_HASH — each CPU has its
		// own value slot, so __sync_fetch_and_add is not strictly
		// required. Kept for defensive consistency with generic map
		// patterns; cost is negligible.
		__sync_fetch_and_add(val, 1);
	} else {
		__u64 one = 1;
		bpf_map_update_elem(&metrics_map, &key, &one, BPF_NOEXIST);
	}
}

// is_loopback: IPv4 address is in 127.0.0.0/8.
static __always_inline bool is_loopback(__u32 ip)
{
	return (ip & bpf_htonl(0xFF000000)) == bpf_htonl(0x7F000000);
}

// is_in_subnet: IPv4 address is inside (net_addr, net_mask). All three
// arguments are network byte order to match ctx->user_ip4.
static __always_inline bool is_in_subnet(__u32 ip, __u32 net_addr, __u32 net_mask)
{
	return (ip & net_mask) == net_addr;
}

// is_ipv6_loopback: ::1
static __always_inline bool is_ipv6_loopback(const struct bpf_sock_addr *ctx)
{
	return ctx->user_ip6[0] == 0 && ctx->user_ip6[1] == 0 &&
	       ctx->user_ip6[2] == 0 && ctx->user_ip6[3] == bpf_htonl(1);
}

// is_ipv4_mapped: ::ffff:x.x.x.x (dual-stack IPv4 carried over AF_INET6).
static __always_inline bool is_ipv4_mapped(const struct bpf_sock_addr *ctx)
{
	return ctx->user_ip6[0] == 0 && ctx->user_ip6[1] == 0 &&
	       ctx->user_ip6[2] == bpf_htonl(IPV4_MAPPED_PREFIX);
}

// ---------------------------------------------------------------------------
// Egress event emission — rate-limited ringbuf submit
// ---------------------------------------------------------------------------

// Token-bucket tunables — see ratelimit_check_and_take. Per-cgroup keying
// matches the granularity of "noisy agent" we want to throttle without
// starving siblings. Compile-time constants; no userspace config knob.
#define RATELIMIT_BURST       64ULL
#define RATELIMIT_REFILL_NS   100000000ULL // 100 ms
#define RATELIMIT_TOKENS_PER  64ULL

// ratelimit_drop_bump increments the ratelimit_drops counter for the
// given cgroup_id, inserting a fresh entry on first drop. Shared by
// the rate-limit path in ratelimit_check_and_take so the contention
// branch and the empty-bucket branch use one code path.
static __always_inline void
ratelimit_drop_bump(__u64 cgroup_id)
{
	__u64 *drops = bpf_map_lookup_elem(&ratelimit_drops, &cgroup_id);
	if (drops) {
		__sync_fetch_and_add(drops, 1);
		return;
	}
	__u64 one = 1;
	bpf_map_update_elem(&ratelimit_drops, &cgroup_id, &one, BPF_ANY);
}

// ratelimit_check_and_take returns true if the caller may emit a record,
// false if the cgroup is over its budget. The decrement uses an explicit
// CAS retry loop (__sync_val_compare_and_swap) that gates on tokens > 0
// so racing CPUs can never underflow the u64 counter. Without the CAS,
// a fetch-and-sub on tokens==0 would wrap to ~MAX_U64 and the cgroup
// would emit unbounded events until LRU eviction.
//
// Refill arithmetic stays non-atomic: refill drift under racing CPUs
// is cheaper than the cmpxchg cost and self-heals on the next tick.
// The CAS loop bound (RATELIMIT_CAS_RETRIES) caps verifier-loop budget
// — a sustained contention storm beyond that gets a drop instead of a
// silent emission, which is the safer side to err toward.
#define RATELIMIT_CAS_RETRIES 4
static __always_inline bool
ratelimit_check_and_take(__u64 cgroup_id)
{
	__u64 now = bpf_ktime_get_ns();
	struct ratelimit_state_val *st =
		bpf_map_lookup_elem(&ratelimit_state, &cgroup_id);
	if (!st) {
		struct ratelimit_state_val fresh = {
			.last_topup_ns = now,
			.tokens        = RATELIMIT_BURST - 1,
		};
		bpf_map_update_elem(&ratelimit_state, &cgroup_id, &fresh,
				    BPF_NOEXIST);
		return true;
	}

	if (now - st->last_topup_ns >= RATELIMIT_REFILL_NS) {
		__u64 add = RATELIMIT_TOKENS_PER;
		if (st->tokens + add > RATELIMIT_BURST)
			add = RATELIMIT_BURST - st->tokens;
		st->tokens += add;
		st->last_topup_ns = now;
	}

#pragma unroll
	for (int i = 0; i < RATELIMIT_CAS_RETRIES; i++) {
		__u64 cur = st->tokens;
		// Out-of-bounds (wrap from a hypothetical earlier non-CAS
		// caller, or stale prior-CP state) — clamp to 0 best-effort
		// via CAS and treat this attempt as a drop. The clamp is
		// soft: if it loses the CAS race, the next caller retries.
		if (cur == 0 || cur > RATELIMIT_BURST) {
			if (cur > RATELIMIT_BURST) {
				__sync_val_compare_and_swap(&st->tokens, cur, 0);
			}
			ratelimit_drop_bump(cgroup_id);
			return false;
		}
		if (__sync_val_compare_and_swap(&st->tokens, cur, cur - 1) == cur) {
			return true;
		}
		// CAS lost; another CPU mutated tokens — retry.
	}
	// Sustained contention exhausted the retry budget. Count as a
	// rate-limit drop so operators see the signal.
	ratelimit_drop_bump(cgroup_id);
	return false;
}

// lookup_domain_hash_for_ip resolves the FNV-1a domain hash for an IPv4
// destination by reading the pinned dns_cache (populated by CoreDNS). A
// miss (direct-IP connect, or DNS resolution outside our managed CoreDNS)
// returns 0; userspace treats 0 as "no domain attribution".
static __always_inline __u32
lookup_domain_hash_for_ip(__u32 dst_ip)
{
	if (dst_ip == 0)
		return 0;
	struct dns_entry *dns = bpf_map_lookup_elem(&dns_cache, &dst_ip);
	if (!dns)
		return 0;
	return dns->domain_hash;
}

// submit_event_v4 / submit_event_v6 / submit_event_nodst — typed
// wrappers around the ringbuf-reserve + ratelimit + compound-literal
// init sequence. Three helpers instead of one because each path has a
// different address representation and a different domain-hash source,
// and the BPF verifier is happiest when every field is initialized
// from a constant or a known-bounded copy.
//
// Two BPF-side drop dimensions, each kept distinct so operators can
// diagnose root cause from the userspace metric surface:
//   - Rate-limit drop: ratelimit_drops[cgroup_id] bumped. The token
//     was already consumed by ratelimit_check_and_take. "Noisy agent
//     over its budget" — adjust RATELIMIT_BURST or investigate the
//     agent.
//   - Kernel-fault drop (reserve returned NULL): events_drops[0]
//     bumped. The token consumed by ratelimit_check_and_take is NOT
//     refunded — matches Cilium (bpf/lib/ratelimit.h) and Tetragon
//     (bpf/process/bpf_rate.h) convention: a failed reserve indicates
//     the consumer can't keep up, so token-budget should back off,
//     not retry harder.
// Additional userspace-side drops (BatchProcessor overflow) are out of
// scope here; the netlogger pipeline counts those independently.

// submit_event_v4 — connect4 / sendmsg4 / connect6+sendmsg6 IPv4-mapped
// paths. dst_ip4 is in network byte order (matches ctx->user_ip4 and
// ctx->user_ip6[3]). The 16-byte ev->dst_ip is fully zero-initialized
// by the compound literal, then the 4 v4 bytes are written into
// ev->dst_ip[0..3]. Domain-hash lookup is v4-keyed.
static __always_inline void
submit_event_v4(__u64 cgroup_id, __u32 dst_ip4, __u16 dst_port_host,
		__u8 l4_proto, __u8 verdict, __u8 flags)
{
	if (!ratelimit_check_and_take(cgroup_id))
		return;

	struct egress_event *ev =
		bpf_ringbuf_reserve(&events_ringbuf, sizeof(*ev), 0);
	if (!ev) {
		__u32 zero = 0;
		__u64 *cnt = bpf_map_lookup_elem(&events_drops, &zero);
		if (cnt)
			__sync_fetch_and_add(cnt, 1);
		return;
	}

	*ev = (struct egress_event){
		.ts_ns       = bpf_ktime_get_ns(),
		.cgroup_id   = cgroup_id,
		.dst_ip      = {0},
		.domain_hash = lookup_domain_hash_for_ip(dst_ip4),
		.dst_port    = dst_port_host,
		.verdict     = verdict,
		.flags       = flags,
		.l4_proto    = l4_proto,
		._pad        = {0, 0, 0, 0, 0, 0, 0},
	};
	__builtin_memcpy(ev->dst_ip, &dst_ip4, sizeof(dst_ip4));
	bpf_ringbuf_submit(ev, 0);
}

// submit_event_v6 — connect6 / sendmsg6 native IPv6 paths.
// dst_ip6 points at ctx->user_ip6 (4 × __be32, network byte order). The
// full 16 bytes copy into ev->dst_ip. Domain-hash lookup is v4-only, so
// v6 always emits hash=0; EGRESS_FLAG_IPV6 is OR'd into flags by the
// helper so call sites only carry verdict-specific flag bits.
static __always_inline void
submit_event_v6(__u64 cgroup_id, const __u32 dst_ip6[4], __u16 dst_port_host,
		__u8 l4_proto, __u8 verdict, __u8 flags)
{
	if (!ratelimit_check_and_take(cgroup_id))
		return;

	struct egress_event *ev =
		bpf_ringbuf_reserve(&events_ringbuf, sizeof(*ev), 0);
	if (!ev) {
		__u32 zero = 0;
		__u64 *cnt = bpf_map_lookup_elem(&events_drops, &zero);
		if (cnt)
			__sync_fetch_and_add(cnt, 1);
		return;
	}

	*ev = (struct egress_event){
		.ts_ns       = bpf_ktime_get_ns(),
		.cgroup_id   = cgroup_id,
		.dst_ip      = {0},
		.domain_hash = 0,
		.dst_port    = dst_port_host,
		.verdict     = verdict,
		.flags       = (__u8)(flags | EGRESS_FLAG_IPV6),
		.l4_proto    = l4_proto,
		._pad        = {0, 0, 0, 0, 0, 0, 0},
	};
	__builtin_memcpy(ev->dst_ip, dst_ip6, 16);
	bpf_ringbuf_submit(ev, 0);
}

// submit_event_nodst — sock_create paths. The syscall is socket
// creation, not connection/send — there is no destination, so dst_ip /
// dst_port / domain_hash are all zero and EGRESS_FLAG_NO_DST is the
// observable signal. Userspace renders Event.DstIP as invalid; the
// OTLP sink omits the dst_ip attribute so operators can partition via
// _exists_:attributes.dst_ip.
static __always_inline void
submit_event_nodst(__u64 cgroup_id, __u8 l4_proto, __u8 verdict)
{
	if (!ratelimit_check_and_take(cgroup_id))
		return;

	struct egress_event *ev =
		bpf_ringbuf_reserve(&events_ringbuf, sizeof(*ev), 0);
	if (!ev) {
		__u32 zero = 0;
		__u64 *cnt = bpf_map_lookup_elem(&events_drops, &zero);
		if (cnt)
			__sync_fetch_and_add(cnt, 1);
		return;
	}

	*ev = (struct egress_event){
		.ts_ns       = bpf_ktime_get_ns(),
		.cgroup_id   = cgroup_id,
		.dst_ip      = {0},
		.domain_hash = 0,
		.dst_port    = 0,
		.verdict     = verdict,
		.flags       = EGRESS_FLAG_NO_DST | EGRESS_EMIT_SOCK_CREATE,
		.l4_proto    = l4_proto,
		._pad        = {0, 0, 0, 0, 0, 0, 0},
	};
	bpf_ringbuf_submit(ev, 0);
}

// ---------------------------------------------------------------------------
// Program preamble helper
// ---------------------------------------------------------------------------

// enter_enforced is the shared fast-path preamble for every clawker BPF
// program. It handles root-uid pass-through, the active-bypass detection
// (when check_bypass is true), and the container_map lookup that gates
// enforcement.
//
// Returns enum enter_state:
//   ENTER_NOT_MANAGED — uid==0 or container not in container_map; caller
//                       must `return 1` immediately.
//   ENTER_BYPASSED    — managed but bypass flag set (check_bypass=true
//                       only); caller emits submit_event_v4/v6/nodst
//                       (BYPASSED) then `return 1`.
//   ENTER_ENFORCED    — proceed with normal routing decision; *cfg and
//                       *cgroup_id are populated.
//
// Callers that do not care about bypass (recvmsg4/recvmsg6) pass
// check_bypass = false. With check_bypass=false the function never
// returns ENTER_BYPASSED. Source-rewrite for DNS responses is keyed by
// CoreDNS IP, so a recvmsg from a bypassed container that never went
// through CoreDNS won't match the rewrite predicate — honoring bypass
// here is unnecessary.
//
// enter_enforced calls metric_inc(ACTION_BYPASS) on the confirmed
// bypass path so the existing metrics_map dump (consumed by the
// break-glass ebpf-manager CLI) keeps working. The
// submit_event_v4/v6/nodst(BYPASSED) record emitted by the caller is
// the finer-grained signal for the netlogger pipeline.
static __always_inline enum enter_state
enter_enforced(struct container_config **cfg, __u64 *cgroup_id, bool check_bypass)
{
	__u32 uid = bpf_get_current_uid_gid() & 0xFFFFFFFF;
	if (uid == 0)
		return ENTER_NOT_MANAGED;

	__u64 cid = bpf_get_current_cgroup_id();

	if (check_bypass) {
		__u8 *bypassed = bpf_map_lookup_elem(&bypass_map, &cid);
		if (bypassed && *bypassed == 1) {
			struct container_config *bc =
				bpf_map_lookup_elem(&container_map, &cid);
			if (!bc)
				return ENTER_NOT_MANAGED;
			// container_map confirmed; only now is this a real
			// bypass event worth counting in metrics_map.
			metric_inc(cid, 0, 0, ACTION_BYPASS);
			*cfg = bc;
			*cgroup_id = cid;
			return ENTER_BYPASSED;
		}
	}

	struct container_config *c = bpf_map_lookup_elem(&container_map, &cid);
	if (!c)
		return ENTER_NOT_MANAGED;

	*cfg = c;
	*cgroup_id = cid;
	return ENTER_ENFORCED;
}

// ---------------------------------------------------------------------------
// Routing decision helpers — shared by connect4/connect6 and sendmsg4/sendmsg6
// ---------------------------------------------------------------------------

// route_verdict tells the caller how to react to a route_result:
//   V_PASSTHROUGH — leave ctx alone; caller returns 1 (allow unchanged)
//   V_REWRITE     — caller assigns new_ip/new_port_nbo to the ctx field
//                   appropriate for its program (user_ip4 or user_ip6[3])
//                   and returns 1 (allow with redirect)
//   V_DENY        — caller returns 0 (drop)
enum route_verdict {
	V_PASSTHROUGH = 0,
	V_REWRITE     = 1,
	V_DENY        = 2,
};

// route_result carries the routing decision plus the redirect target.
// Helpers own all byte-order conversions and metric emission so callers
// only translate the verdict into the correct return value.
struct route_result {
	__u32 new_ip;       // network byte order, ready to assign
	__u16 new_port_nbo; // network byte order, ready to assign
	__u8  verdict;      // enum route_verdict
	__u8  _pad;
};

// decide_connect computes the IPv4 routing decision for a connect() from a
// managed container. It is the shared body for clawker_connect4 (direct IPv4)
// and the IPv4-mapped branch of clawker_connect6 — the logic is identical,
// only the ctx field that receives the rewritten IP differs.
//
// Inputs:
//   ctx       — connect context (same type for both callers)
//   cfg       — container_config populated by enter_enforced
//   cgroup_id — cgroup id from enter_enforced, used for metric emission
//   dst_ip    — destination IPv4, network byte order
//   dst_port  — destination port, host byte order
//
// The caller must have already confirmed ctx->type is SOCK_STREAM or
// SOCK_DGRAM. All metric_inc sites live inside this helper so connect4
// and connect6's IPv4-mapped branch stay in lockstep on observability.
static __always_inline struct route_result
decide_connect(struct bpf_sock_addr *ctx, struct container_config *cfg,
	       __u64 cgroup_id, __u32 dst_ip, __u16 dst_port)
{
	struct route_result r = { .verdict = V_PASSTHROUGH };

	// Loop prevention: skip redirect for Envoy/CoreDNS upstream traffic.
	// Envoy sets SO_MARK = CLAWKER_MARK on its upstream sockets
	// (ref: iximiuz.com eBPF transparent proxy pattern).
	__u32 mark = 0;
	bpf_getsockopt(ctx, SOL_SOCKET, SO_MARK, &mark, sizeof(mark));
	if (mark == CLAWKER_MARK)
		return r;

	// DNS redirect — before loopback because Docker embedded DNS
	// (127.0.0.11) is loopback. All DNS must go through CoreDNS.
	if (dst_port == DNS_PORT) {
		r.verdict      = V_REWRITE;
		r.new_ip       = cfg->coredns_ip;
		r.new_port_nbo = bpf_htons(DNS_PORT);
		metric_inc(cgroup_id, 0, DNS_PORT, ACTION_ALLOW);
		return r;
	}

	if (is_loopback(dst_ip))
		return r;

	if (is_in_subnet(dst_ip, cfg->net_addr, cfg->net_mask))
		return r;

	if (cfg->host_proxy_ip != 0 &&
	    dst_ip == cfg->host_proxy_ip && dst_port == cfg->host_proxy_port)
		return r;

	// Gateway lockdown: redirect traffic aimed directly at the clawker-net
	// gateway through Envoy's egress listener so SNI inspection runs.
	// Metric is ACTION_DENY because from the user's perspective the
	// direct-to-gateway path is blocked, even though we return 1 and let
	// Envoy emit the actual refusal upstream.
	if (dst_ip == cfg->gateway_ip) {
		if (cfg->host_proxy_port != 0 && dst_port == cfg->host_proxy_port)
			return r;
		r.verdict      = V_REWRITE;
		r.new_ip       = cfg->envoy_ip;
		r.new_port_nbo = bpf_htons(cfg->egress_port);
		metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
		return r;
	}

	// Non-DNS UDP: deny outright. UDP has no TLS path for SNI inspection
	// and the DNS case is already handled above.
	if (ctx->type == SOCK_DGRAM) {
		metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
		r.verdict = V_DENY;
		return r;
	}

	// TCP per-domain routing via DNS cache. If the resolved IP has a
	// cached domain AND the domain has a route rule for this dst_port,
	// send it to the domain-specific Envoy listener instead of the
	// catch-all. Preserve domain_hash so the catch-all metric below can
	// still attribute traffic to the resolved domain when the route
	// lookup misses.
	__u32 domain_hash = 0;
	struct dns_entry *dns = bpf_map_lookup_elem(&dns_cache, &dst_ip);
	if (dns) {
		domain_hash = dns->domain_hash;
		struct route_key rk = {
			.domain_hash = dns->domain_hash,
			.dst_port = dst_port,
		};
		struct route_val *rv = bpf_map_lookup_elem(&route_map, &rk);
		if (rv) {
			r.verdict      = V_REWRITE;
			r.new_ip       = cfg->envoy_ip;
			r.new_port_nbo = bpf_htons(rv->envoy_port);
			metric_inc(cgroup_id, dns->domain_hash, dst_port, ACTION_ALLOW);
			return r;
		}
	}

	// Catch-all: Envoy egress listener (TLS/SNI inspection).
	r.verdict      = V_REWRITE;
	r.new_ip       = cfg->envoy_ip;
	r.new_port_nbo = bpf_htons(cfg->egress_port);
	metric_inc(cgroup_id, domain_hash, dst_port, ACTION_ALLOW);
	return r;
}

// decide_sendmsg is the UDP-only counterpart to decide_connect for sendmsg4
// and the IPv4-mapped branch of sendmsg6. The logic is a strict subset of
// decide_connect: DNS redirect, loopback/subnet pass-through, everything
// else denied. There is no per-domain routing (UDP has no TLS path).
static __always_inline struct route_result
decide_sendmsg(struct container_config *cfg, __u64 cgroup_id,
	       __u32 dst_ip, __u16 dst_port)
{
	struct route_result r = { .verdict = V_PASSTHROUGH };

	// DNS redirect before loopback (Docker embedded DNS is 127.0.0.11).
	if (dst_port == DNS_PORT) {
		r.verdict      = V_REWRITE;
		r.new_ip       = cfg->coredns_ip;
		r.new_port_nbo = bpf_htons(DNS_PORT);
		return r;
	}

	if (is_loopback(dst_ip))
		return r;

	if (is_in_subnet(dst_ip, cfg->net_addr, cfg->net_mask))
		return r;

	metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
	r.verdict = V_DENY;
	return r;
}

// should_rewrite_dns_source: recvmsg helper that tells the caller whether
// the incoming UDP response looks like a DNS reply coming back from CoreDNS.
// Callers that return true rewrite the source to Docker embedded DNS so the
// application's socket accepts it as if it came from 127.0.0.11:53.
//
// src_port is host byte order.
static __always_inline bool
should_rewrite_dns_source(struct container_config *cfg, __u32 src_ip, __u16 src_port)
{
	return src_ip == cfg->coredns_ip && src_port == DNS_PORT;
}

#endif // __CLAWKER_COMMON_H
