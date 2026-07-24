package dnsbpf

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/cilium/ebpf"

	clawkerebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
)

// DefaultPinPath is where the eBPF manager pins the dns_cache map.
const DefaultPinPath = clawkerebpf.PinPath + "/" + clawkerebpf.DNSCacheMapName

// dnsEntry mirrors struct dns_entry in bpf/common.h.
// expire_ts uses wall-clock seconds (time.Now().Unix() + TTL), matching
// the garbage collector in controlplane/firewall/ebpf/manager.go GarbageCollectDNS().
type dnsEntry struct {
	Identity clawkerebpf.RouteIdentity // named uint32 — encodes identically into the kernel map
	ExpireTS uint32
	Source   uint8
	Pad      [3]uint8
}

// expiryTS returns the wall-clock expiry for a TTL, saturating at MaxUint32
// so the int64→uint32 narrowing can never wrap (expire_ts is a u32 on the
// BPF side).
func expiryTS(ttlSeconds uint32) uint32 {
	exp := time.Now().Unix() + int64(ttlSeconds)
	if exp <= 0 || exp > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(exp)
}

// dnsCacheMap is the subset of *ebpf.Map that BPFMap uses. It exists purely
// as a test seam so the source-precedence logic in Update can be exercised
// without a live kernel map (which would require CAP_BPF, unavailable in the
// dev container). The production *ebpf.Map satisfies it structurally.
type dnsCacheMap interface {
	Lookup(key, valueOut any) error
	Update(key, value any, flags ebpf.MapUpdateFlags) error
	Close() error
}

// BPFMap wraps a pinned BPF hash map for dns_cache writes.
type BPFMap struct {
	m dnsCacheMap
}

// OpenBPFMap opens the pinned dns_cache map.
func OpenBPFMap(pinPath string) (*BPFMap, error) {
	m, err := ebpf.LoadPinnedMap(pinPath, nil)
	if err != nil {
		return nil, fmt.Errorf("opening pinned dns_cache map at %s: %w", pinPath, err)
	}
	return &BPFMap{m: m}, nil
}

// Update writes an IP → identity entry to the dns_cache map.
// ip must be in network byte order (from IPToUint32).
//
// Source precedence (cilium ipcache analog): an existing DNSSourceSeed entry
// — written by CP SyncRoutes for an IP-literal rule — outranks a DNS-derived
// write, so it is left untouched. A lookup that fails for any reason other
// than ErrKeyNotExist also skips the write: the entry's source is unknown and
// overwriting could clobber a seed with a short-TTL DNS entry the GC would
// then evict. The lookup-then-update pair is not atomic, but the only
// competing seed writer is the CP's rare reconcile, which also wins any
// interleave: a seed written after our lookup is simply restored by the next
// reconcile's re-seed.
func (b *BPFMap) Update(ip uint32, identity clawkerebpf.RouteIdentity, ttlSeconds uint32) {
	var cur dnsEntry
	err := b.m.Lookup(ip, &cur)
	switch {
	case err == nil && cur.Source == clawkerebpf.DNSSourceSeed:
		return
	case err != nil && !errors.Is(err, ebpf.ErrKeyNotExist):
		// Fail toward preserving a possible seed; the next DNS answer retries.
		log.Warningf("looking up dns_cache for ip=%s: %v; skipping write",
			clawkerebpf.Uint32ToIP(ip), err)
		return
	}
	entry := dnsEntry{
		Identity: identity,
		ExpireTS: expiryTS(ttlSeconds),
		Source:   clawkerebpf.DNSSourceDNS,
	}
	if err := b.m.Update(ip, entry, ebpf.UpdateAny); err != nil {
		log.Warningf("updating dns_cache for ip=%s identity=%d: %v",
			clawkerebpf.Uint32ToIP(ip), identity, err)
	}
}

// Close closes the BPF map file descriptor.
func (b *BPFMap) Close() error {
	if b.m != nil {
		return b.m.Close()
	}
	return nil
}
