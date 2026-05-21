package netlogger

import (
	"context"
	"sync"
	"time"

	"github.com/cilium/ebpf"

	clawkerebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
)

// ReverseDNSMap is the userspace mirror of the pinned dns_cache,
// keyed by domain_hash. It exists so the OTel sink can attach a
// `dst_host` attribute alongside the `domain_hash` field when the
// operator wants human-readable domain attribution on egress
// records.
//
// dns_cache stores {IPv4 → {domain_hash, expire_ts}} — the original
// domain string is not on the BPF side, so Lookup returns "" for
// every hash. The observed-hash set is tracked so a future
// (hash → string) population path can light Lookup up without
// touching the public API; until then, operators filter on the
// numeric `domain_hash` attribute on each emitted record.
type ReverseDNSMap struct {
	mu     sync.RWMutex
	byHash map[uint32]string

	// walk is the iteration seam. Production wires it from the
	// pinned dns_cache map via NewReverseDNSMap; tests inject a
	// stub so they don't need a real *ebpf.Map (which would require
	// CAP_BPF, unavailable inside the clawker dev container per
	// Task-1 learnings).
	walk func(visit func(hash uint32)) error

	log *logger.Logger
}

// NewReverseDNSMap constructs a ReverseDNSMap backed by a pinned BPF
// dns_cache map. Pass nil for dnsCache when running in a test that
// supplies its own walk function via NewReverseDNSMapWithWalk.
func NewReverseDNSMap(dnsCache *ebpf.Map, log *logger.Logger) *ReverseDNSMap {
	if log == nil {
		log = logger.Nop()
	}
	return &ReverseDNSMap{
		byHash: make(map[uint32]string),
		walk:   walkDNSCache(dnsCache),
		log:    log,
	}
}

// NewReverseDNSMapWithWalk constructs a ReverseDNSMap with an
// injectable walk function — used by unit tests that don't have a
// real BPF map handle.
func NewReverseDNSMapWithWalk(walk func(visit func(hash uint32)) error, log *logger.Logger) *ReverseDNSMap {
	if log == nil {
		log = logger.Nop()
	}
	return &ReverseDNSMap{
		byHash: make(map[uint32]string),
		walk:   walk,
		log:    log,
	}
}

// Lookup returns the domain string bound to hash, or "" when:
//   - hash == 0 (direct-IP connect, no DNS resolution at all)
//   - the hash has not been observed in dns_cache yet
//   - the dns_cache entry exists but no domain string is available
//     (see type doc) — every hit returns "" until a follow-up
//     population path lights this up.
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
// row must not kill the netlogger pipeline (CP no-panic discipline).
func (m *ReverseDNSMap) Run(ctx context.Context, interval time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			m.log.Error().
				Interface("panic", r).
				Str("event", "netlogger_reverse_dns_panic").
				Msg("reverse-DNS refresh loop panicked — Lookup will return cached values only")
		}
	}()
	if interval <= 0 {
		interval = 5 * time.Second
	}
	m.refresh()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refresh()
		}
	}
}

// refresh walks the dns_cache map exactly once, replacing byHash
// with the set of observed hashes. Iteration errors are logged at
// debug — the next tick retries.
func (m *ReverseDNSMap) refresh() {
	if m.walk == nil {
		return
	}
	next := make(map[uint32]string)
	err := m.walk(func(hash uint32) {
		if hash == 0 {
			return
		}
		// String stays "" until a separate (hash → domain)
		// population path lands. The presence of the key here
		// is itself useful: it lets Lookup differentiate "we
		// observed this hash" from "never seen". See type doc.
		next[hash] = ""
	})
	if err != nil {
		m.log.Debug().Err(err).Str("event", "netlogger_reverse_dns_refresh_error").Msg("dns_cache iterate failed")
		return
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
