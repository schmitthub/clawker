package netlogger

import (
	"context"
	"sync"
	"time"

	"github.com/cilium/ebpf"

	clawkerebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
)

// IdentitySource returns the live route-identity table (identity → dst) the
// otelSink reads when emitting `dst_host` on a security record.
//
// Production wiring: a closure over the firewall IdentityAllocator's
// Snapshot — the same table that keys route_map and feeds the Corefile's
// dnsbpf directives, so attribution is a direct read of the allocation,
// not an inversion. Tests pass a static map via a literal closure.
//
// nil IdentitySource is supported (degraded mode — every Lookup returns ""),
// matching the boot-time shape before the wiring lands.
type IdentitySource func() map[uint32]string

// ReverseDNSMap holds the userspace identity→dst table the otelSink reads
// when stamping `dst_host` on each emitted security record.
//
// Source of truth is the firewall IdentityAllocator: dnsbpf writes each
// zone's CP-allocated identity into dns_cache (delivered via the Corefile
// directive argument), so the identity netlogger observes on a security
// record is a key in the allocator's table — by construction, both sides
// read one allocation.
//
// The pinned dns_cache map is still walked on every refresh tick for the
// observed-identity set. Identities present in dns_cache but absent from
// the source (race after rule remove, dnsbpf stale entry) leave
// dst_host="" — operators reading the security record see no domain
// attribution for that record, the same outcome as a direct-IP connect.
type ReverseDNSMap struct {
	mu   sync.RWMutex
	byID map[uint32]string

	// identities is the live allocation snapshot source. Each refresh
	// tick reads it once.
	identities IdentitySource

	// walk is the iteration seam over the pinned dns_cache map.
	// Production wires it via NewReverseDNSMap; tests inject a
	// stub so they don't need a real *ebpf.Map (which would
	// require CAP_BPF, unavailable inside the clawker dev
	// container). The walk is not load-bearing for Lookup —
	// IdentitySource is — but the dns_cache identity set is logged on
	// every refresh tick for triage when an emitted security
	// record carries an unattributed identity.
	walk func(visit func(identity uint32)) error

	log *logger.Logger
}

// NewReverseDNSMap constructs a ReverseDNSMap backed by a pinned BPF
// dns_cache map. Pass nil for dnsCache when running in a test that
// supplies its own walk function via NewReverseDNSMapWithWalk. Pass
// nil for identities to run in degraded mode (Lookup always returns "").
func NewReverseDNSMap(dnsCache *ebpf.Map, identities IdentitySource, log *logger.Logger) *ReverseDNSMap {
	if log == nil {
		log = logger.Nop()
	}
	return &ReverseDNSMap{
		byID:       make(map[uint32]string),
		identities: identities,
		walk:       walkDNSCache(dnsCache),
		log:        log,
	}
}

// NewReverseDNSMapWithWalk constructs a ReverseDNSMap with an
// injectable walk function — used by unit tests that don't have a
// real BPF map handle.
func NewReverseDNSMapWithWalk(
	walk func(visit func(identity uint32)) error,
	identities IdentitySource,
	log *logger.Logger,
) *ReverseDNSMap {
	if log == nil {
		log = logger.Nop()
	}
	return &ReverseDNSMap{
		byID:       make(map[uint32]string),
		identities: identities,
		walk:       walk,
		log:        log,
	}
}

// Lookup returns the dst string bound to identity, or "" when:
//   - identity == 0 (direct-IP connect, no DNS resolution at all)
//   - IdentitySource is nil (degraded mode)
//   - the identity is absent from the source (race after rule remove,
//     dnsbpf stale entry)
func (m *ReverseDNSMap) Lookup(identity uint32) string {
	if identity == 0 {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byID[identity]
}

// Run drives a periodic refresh of the byID map until ctx is
// cancelled. The first refresh fires immediately so the map is
// populated before the first egress event arrives.
//
// Recovers from any panic in the refresh path: a malformed dns_cache
// row or an IdentitySource panic must not kill the netlogger pipeline
// (CP no-panic discipline).
func (m *ReverseDNSMap) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	m.refreshRecovered()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refreshRecovered()
		}
	}
}

// refreshRecovered runs one refresh pass, recovering from a panic so a single
// bad sweep is logged and skipped rather than tearing down the loop. A
// top-level recover would catch the first panic and then return permanently,
// freezing reverse-DNS attribution at the last good map for the rest of the CP
// lifetime; recovering per-tick keeps the loop alive (mirrors the per-sweep
// recover discipline of the dns_cache GC goroutine). Enforcement is unaffected
// either way — this map only attributes egress records.
func (m *ReverseDNSMap) refreshRecovered() {
	defer func() {
		if r := recover(); r != nil {
			m.log.Error().
				Interface("panic", r).
				Str("event", "netlogger_reverse_dns_panic").
				Msg("reverse-DNS refresh panicked — skipping this pass, Lookup serves cached values until the next tick")
		}
	}()
	m.refresh()
}

// refresh snapshots the IdentitySource and walks dns_cache to surface any
// observed identities the source doesn't account for.
func (m *ReverseDNSMap) refresh() {
	next := make(map[uint32]string)
	if m.identities != nil {
		for id, dst := range m.identities() {
			if id == 0 || dst == "" {
				continue
			}
			next[id] = dst
		}
	}

	if m.walk != nil {
		var unattributed int
		err := m.walk(func(identity uint32) {
			if identity == 0 {
				return
			}
			if _, ok := next[identity]; !ok {
				unattributed++
			}
		})
		switch {
		case err != nil:
			m.log.Warn().
				Err(err).
				Str("event", "netlogger_reverse_dns_refresh_error").
				Msg("dns_cache iterate failed — emitted records will carry empty dst_host until next successful refresh")
		case unattributed > 0:
			// Identities present in dns_cache but missing from the
			// allocator table — race or stale entry. Records
			// emitted with these identities carry dst_host="".
			m.log.Warn().
				Int("unattributed", unattributed).
				Int("attributed", len(next)).
				Str("event", "netlogger_reverse_dns_unattributed").
				Msg("dns_cache holds identities absent from the route-identity table")
		}
	}

	m.mu.Lock()
	m.byID = next
	m.mu.Unlock()
}

// walkDNSCache adapts *ebpf.Map iteration to the walk function shape.
// Returns a no-op walk when the map handle is nil (e.g. tests that
// construct a ReverseDNSMap without a real BPF map).
func walkDNSCache(dnsCache *ebpf.Map) func(func(identity uint32)) error {
	if dnsCache == nil {
		return func(func(identity uint32)) error { return nil }
	}
	return func(visit func(identity uint32)) error {
		var key uint32
		var val clawkerebpf.DNSEntry
		iter := dnsCache.Iterate()
		for iter.Next(&key, &val) {
			visit(val.Identity)
		}
		return iter.Err()
	}
}
