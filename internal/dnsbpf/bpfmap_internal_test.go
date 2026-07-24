package dnsbpf

import (
	"errors"
	"testing"

	"github.com/cilium/ebpf"

	clawkerebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
)

// fakeDNSCacheMap is an in-memory dnsCacheMap for exercising BPFMap.Update's
// source-precedence logic without a kernel map. Production only ever passes
// uint32 keys and dnsEntry values; a mismatched type is a test-wiring bug,
// surfaced as an error so the write path visibly fails.
type fakeDNSCacheMap struct {
	entries map[uint32]dnsEntry
	// lookupErr, if non-nil, is returned from Lookup regardless of whether
	// the key exists — simulates kernel-level failures (EFAULT, bad FD)
	// that are NOT ErrKeyNotExist.
	lookupErr error
}

func newFakeDNSCacheMap() *fakeDNSCacheMap {
	return &fakeDNSCacheMap{entries: make(map[uint32]dnsEntry), lookupErr: nil}
}

var errFakeMapBadType = errors.New("fakeDNSCacheMap: unexpected key/value type")

func (f *fakeDNSCacheMap) Lookup(key, valueOut any) error {
	k, ok := key.(uint32)
	if !ok {
		return errFakeMapBadType
	}
	out, ok := valueOut.(*dnsEntry)
	if !ok {
		return errFakeMapBadType
	}
	if f.lookupErr != nil {
		return f.lookupErr
	}
	entry, exists := f.entries[k]
	if !exists {
		return ebpf.ErrKeyNotExist
	}
	*out = entry
	return nil
}

func (f *fakeDNSCacheMap) Update(key, value any, _ ebpf.MapUpdateFlags) error {
	k, ok := key.(uint32)
	if !ok {
		return errFakeMapBadType
	}
	v, ok := value.(dnsEntry)
	if !ok {
		return errFakeMapBadType
	}
	f.entries[k] = v
	return nil
}

func (f *fakeDNSCacheMap) Close() error { return nil }

func TestBPFMapUpdate_WritesDNSSourceOnEmptyKey(t *testing.T) {
	fake := newFakeDNSCacheMap()
	b := &BPFMap{m: fake}

	b.Update(0x01020304, 300, 60)

	got, ok := fake.entries[0x01020304]
	if !ok {
		t.Fatalf("Update on empty key wrote nothing")
	}
	if got.Identity != 300 {
		t.Fatalf("Identity = %d; want 300", got.Identity)
	}
	if got.Source != clawkerebpf.DNSSourceDNS {
		t.Fatalf("Source = %d; want DNSSourceDNS (%d)", got.Source, clawkerebpf.DNSSourceDNS)
	}
}

func TestBPFMapUpdate_OverwritesDNSSourceEntry(t *testing.T) {
	fake := newFakeDNSCacheMap()
	fake.entries[0x01020304] = dnsEntry{Identity: 300, ExpireTS: 1, Source: clawkerebpf.DNSSourceDNS, Pad: [3]uint8{}}
	b := &BPFMap{m: fake}

	b.Update(0x01020304, 301, 60)

	if got := fake.entries[0x01020304].Identity; got != 301 {
		t.Fatalf("Identity after overwrite = %d; want 301 — DNS-source entries must be last-writer-wins", got)
	}
}

func TestBPFMapUpdate_RefusesToOverwriteSeedEntry(t *testing.T) {
	// A DNSSourceSeed entry is owned by CP SyncRoutes (IP-literal rule) and
	// outranks DNS-derived writes — the cilium ipcache source-precedence
	// analog. A resolution landing on the same IP must leave it untouched.
	fake := newFakeDNSCacheMap()
	seed := dnsEntry{Identity: 400, ExpireTS: 4102444800, Source: clawkerebpf.DNSSourceSeed, Pad: [3]uint8{}}
	fake.entries[0x01020304] = seed
	b := &BPFMap{m: fake}

	b.Update(0x01020304, 300, 60)

	if got := fake.entries[0x01020304]; got != seed {
		t.Fatalf("seed entry mutated by DNS-derived write: got %+v; want %+v", got, seed)
	}
}

func TestBPFMapUpdate_SkipsWriteOnLookupError(t *testing.T) {
	// A Lookup failure other than ErrKeyNotExist means the entry's source is
	// unknown — the key could hold a seed. Writing anyway would replace it
	// with a short-TTL DNS entry the GC then evicts, failing the IP rule
	// closed until the next CP reconcile. Update must skip the write and let
	// the next DNS answer retry.
	fake := newFakeDNSCacheMap()
	seed := dnsEntry{Identity: 400, ExpireTS: 4102444800, Source: clawkerebpf.DNSSourceSeed, Pad: [3]uint8{}}
	fake.entries[0x01020304] = seed
	fake.lookupErr = errors.New("bpf lookup: bad file descriptor")
	b := &BPFMap{m: fake}

	b.Update(0x01020304, 300, 60)

	if got := fake.entries[0x01020304]; got != seed {
		t.Fatalf("entry mutated despite lookup failure: got %+v; want %+v", got, seed)
	}
}
