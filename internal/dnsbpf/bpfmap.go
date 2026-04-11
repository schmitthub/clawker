package dnsbpf

import (
	"fmt"
	"time"

	"github.com/cilium/ebpf"

	clawkerebpf "github.com/schmitthub/clawker/internal/ebpf"
)

// DefaultPinPath is where the eBPF manager pins the dns_cache map.
const DefaultPinPath = "/sys/fs/bpf/clawker/dns_cache"

// dnsEntry mirrors struct dns_entry in bpf/common.h.
// expire_ts uses wall-clock seconds (time.Now().Unix() + TTL), matching
// the garbage collector in internal/ebpf/manager.go GarbageCollectDNS().
type dnsEntry struct {
	DomainHash uint32
	ExpireTs   uint32
}

// BPFMap wraps a pinned BPF hash map for dns_cache writes.
type BPFMap struct {
	m *ebpf.Map
}

// OpenBPFMap opens the pinned dns_cache map.
func OpenBPFMap(pinPath string) (*BPFMap, error) {
	m, err := ebpf.LoadPinnedMap(pinPath, nil)
	if err != nil {
		return nil, fmt.Errorf("opening pinned dns_cache map at %s: %w", pinPath, err)
	}
	return &BPFMap{m: m}, nil
}

// Update writes an IP → domain_hash entry to the dns_cache map.
// ip must be in network byte order (from IPToUint32).
func (b *BPFMap) Update(ip, domainHash, ttlSeconds uint32) {
	entry := dnsEntry{
		DomainHash: domainHash,
		ExpireTs:   uint32(time.Now().Unix()) + ttlSeconds,
	}
	if err := b.m.Update(ip, entry, ebpf.UpdateAny); err != nil {
		log.Warningf("updating dns_cache for ip=%s hash=%d: %v",
			clawkerebpf.Uint32ToIP(ip), domainHash, err)
	}
}

// Close closes the BPF map file descriptor.
func (b *BPFMap) Close() error {
	if b.m != nil {
		return b.m.Close()
	}
	return nil
}
