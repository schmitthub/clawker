package netlogger

import (
	"context"
	"sync"
	"time"

	"github.com/cilium/ebpf"

	clawkerebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
)

// DomainSource returns every domain dnsbpf will resolve under the
// active firewall configuration. ReverseDNSMap hashes each entry on
// every refresh tick to rebuild the hash→domain table the otelSink
// reads when emitting `dst_host` on a security record.
//
// Production wiring: a closure over `firewall.Handler.ReverseDNSDomains`
// (the CoreDNS zone set unioned with the IP-literal seeds SyncRoutes writes
// into dns_cache). Tests pass a static slice via a literal closure.
//
// nil DomainSource is supported (degraded mode — every Lookup returns ""),
// matching the boot-time shape before the wiring lands.
type DomainSource func() []string

// ReverseDNSMap holds the userspace hash→domain table the otelSink
// reads when stamping `dst_host` on each emitted security record.
//
// Source of truth is the firewall rule set + the internal hostnames
// CoreDNS serves out of band. dnsbpf computes the same FNV-1a hash on
// every A-record write into dns_cache, so the hash netlogger observes
// on a security record matches DomainHash(d) for some d in the firewall
// configuration — by construction: GenerateCorefile and AllResolvableDomains
// share the same normalize/filter passes for the CoreDNS-served zone subset,
// and the IP-literal seeds SyncRoutes writes (which CoreDNS never serves) are
// unioned on top via SeedDomainsFromRules. ReverseDNSDomains is that union.
//
// The pinned dns_cache map is still walked on every refresh tick for
// the observed-hash set. Hashes present in dns_cache but absent from
// DomainSource (race after rule remove, dnsbpf stale entry, hash
// collision against an unknown domain) leave dst_host="" — operators
// reading the security record see no domain attribution for that
// record, which is the same outcome as a direct-IP connect.
//
// Collision floor: FNV-1a is 32-bit. Two firewall-rule domains
// colliding is astronomically unlikely in any realistic config; an
// adversarial second-preimage against a known rule domain is not
// realistic either. The route_map shape inherits the same floor and
// is tracked as part of the route-identity allocator work.
type ReverseDNSMap struct {
	mu     sync.RWMutex
	byHash map[uint32]string

	// domains is the live source of "every domain dnsbpf might
	// resolve". Each refresh tick reads it once and hashes each
	// entry to populate byHash.
	domains DomainSource

	// walk is the iteration seam over the pinned dns_cache map.
	// Production wires it via NewReverseDNSMap; tests inject a
	// stub so they don't need a real *ebpf.Map (which would
	// require CAP_BPF, unavailable inside the clawker dev
	// container). The walk is not load-bearing for Lookup —
	// DomainSource is — but the dns_cache hash set is logged on
	// every refresh tick for triage when an emitted security
	// record carries an unattributed hash.
	walk func(visit func(hash uint32)) error

	log *logger.Logger
}

// NewReverseDNSMap constructs a ReverseDNSMap backed by a pinned BPF
// dns_cache map. Pass nil for dnsCache when running in a test that
// supplies its own walk function via NewReverseDNSMapWithWalk. Pass
// nil for domains to run in degraded mode (Lookup always returns "").
func NewReverseDNSMap(dnsCache *ebpf.Map, domains DomainSource, log *logger.Logger) *ReverseDNSMap {
	if log == nil {
		log = logger.Nop()
	}
	return &ReverseDNSMap{
		byHash:  make(map[uint32]string),
		domains: domains,
		walk:    walkDNSCache(dnsCache),
		log:     log,
	}
}

// NewReverseDNSMapWithWalk constructs a ReverseDNSMap with an
// injectable walk function — used by unit tests that don't have a
// real BPF map handle.
func NewReverseDNSMapWithWalk(walk func(visit func(hash uint32)) error, domains DomainSource, log *logger.Logger) *ReverseDNSMap {
	if log == nil {
		log = logger.Nop()
	}
	return &ReverseDNSMap{
		byHash:  make(map[uint32]string),
		domains: domains,
		walk:    walk,
		log:     log,
	}
}

// Lookup returns the domain string bound to hash, or "" when:
//   - hash == 0 (direct-IP connect, no DNS resolution at all)
//   - DomainSource is nil (degraded mode)
//   - the hash is absent from DomainSource (race after rule remove,
//     dnsbpf stale entry, or domain not under firewall management)
func (m *ReverseDNSMap) Lookup(hash uint32) string {
	if hash == 0 {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byHash[hash]
}

// Run drives a periodic refresh of the byHash map until ctx is
// cancelled. The first refresh fires immediately so the map is
// populated before the first egress event arrives.
//
// Recovers from any panic in the refresh path: a malformed dns_cache
// row or a DomainSource panic must not kill the netlogger pipeline
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

// refresh rebuilds byHash from DomainSource and walks dns_cache to
// surface any observed hashes the source doesn't account for.
func (m *ReverseDNSMap) refresh() {
	next := make(map[uint32]string)
	if m.domains != nil {
		for _, d := range m.domains() {
			if d == "" {
				continue
			}
			next[clawkerebpf.DomainHash(d)] = d
		}
	}

	if m.walk != nil {
		var unattributed int
		err := m.walk(func(hash uint32) {
			if hash == 0 {
				return
			}
			if _, ok := next[hash]; !ok {
				unattributed++
			}
		})
		switch {
		case err != nil:
			m.log.Warn().Err(err).Str("event", "netlogger_reverse_dns_refresh_error").Msg("dns_cache iterate failed — emitted records will carry empty dst_host until next successful refresh")
		case unattributed > 0:
			// Hashes present in dns_cache but missing from
			// DomainSource — race or stale entry. Records
			// emitted with these hashes carry dst_host="".
			m.log.Warn().
				Int("unattributed", unattributed).
				Int("attributed", len(next)).
				Str("event", "netlogger_reverse_dns_unattributed").
				Msg("dns_cache holds hashes absent from firewall rule set")
		}
	}

	m.mu.Lock()
	m.byHash = next
	m.mu.Unlock()
}

// walkDNSCache adapts *ebpf.Map iteration to the walk function shape.
// Returns a no-op walk when the map handle is nil (e.g. tests that
// construct a ReverseDNSMap without a real BPF map).
func walkDNSCache(dnsCache *ebpf.Map) func(func(hash uint32)) error {
	if dnsCache == nil {
		return func(func(hash uint32)) error { return nil }
	}
	return func(visit func(hash uint32)) error {
		var key uint32
		var val clawkerebpf.DNSEntry
		iter := dnsCache.Iterate()
		for iter.Next(&key, &val) {
			visit(val.DomainHash)
		}
		return iter.Err()
	}
}
