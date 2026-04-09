package dnsbpf

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	ctest "github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clawkerebpf "github.com/schmitthub/clawker/internal/ebpf"
)

// cannedHandler returns a plugin.Handler that writes msg as the response.
type cannedHandler struct {
	msg *dns.Msg
}

func (c cannedHandler) ServeDNS(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	resp := c.msg.Copy()
	resp.SetReply(r)
	resp.Answer = c.msg.Answer
	w.WriteMsg(resp)
	return dns.RcodeSuccess, nil
}

func (c cannedHandler) Name() string { return "test" }

var _ plugin.Handler = cannedHandler{}

// recordingMap implements MapWriter and records all Update calls.
type recordingMap struct {
	mu      sync.Mutex
	entries []mapEntry
}

type mapEntry struct {
	IP, DomainHash, TTL uint32
}

func (r *recordingMap) Update(ip, domainHash, ttl uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, mapEntry{ip, domainHash, ttl})
}

func (r *recordingMap) getEntries() []mapEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]mapEntry, len(r.entries))
	copy(cp, r.entries)
	return cp
}

var _ MapWriter = (*recordingMap)(nil)

func TestServeDNS_ForwardsResponseAndWritesBPFMap(t *testing.T) {
	resp := new(dns.Msg)
	resp.SetReply(&dns.Msg{})
	resp.Answer = []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{Name: "github.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.ParseIP("140.82.121.4"),
		},
	}

	rec := &recordingMap{}
	h := Handler{
		Next: cannedHandler{msg: resp},
		Zone: "github.com.",
		Map:  rec, // non-nil — exercises writeARecords
	}

	req := new(dns.Msg)
	req.SetQuestion("github.com.", dns.TypeA)

	w := dnstest.NewRecorder(&ctest.ResponseWriter{})
	rcode, err := h.ServeDNS(context.Background(), w, req)

	// Response forwarded correctly.
	require.NoError(t, err)
	assert.Equal(t, dns.RcodeSuccess, rcode)
	require.NotNil(t, w.Msg)
	require.Len(t, w.Msg.Answer, 1)
	a, ok := w.Msg.Answer[0].(*dns.A)
	require.True(t, ok)
	assert.Equal(t, "140.82.121.4", a.A.String())

	// BPF map written with correct values.
	entries := rec.getEntries()
	require.Len(t, entries, 1)
	assert.Equal(t, clawkerebpf.IPToUint32(net.ParseIP("140.82.121.4")), entries[0].IP)
	assert.Equal(t, clawkerebpf.DomainHash("github.com"), entries[0].DomainHash)
	assert.Equal(t, uint32(300), entries[0].TTL)
}

func TestServeDNS_MultipleARecords(t *testing.T) {
	resp := new(dns.Msg)
	resp.SetReply(&dns.Msg{})
	resp.Answer = []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{Name: "github.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.ParseIP("140.82.121.3"),
		},
		&dns.A{
			Hdr: dns.RR_Header{Name: "github.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.ParseIP("140.82.121.4"),
		},
		// AAAA records should be ignored.
		&dns.AAAA{
			Hdr:  dns.RR_Header{Name: "github.com.", Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60},
			AAAA: net.ParseIP("::1"),
		},
	}

	rec := &recordingMap{}
	h := Handler{
		Next: cannedHandler{msg: resp},
		Zone: "github.com.",
		Map:  rec,
	}

	req := new(dns.Msg)
	req.SetQuestion("github.com.", dns.TypeA)

	w := dnstest.NewRecorder(&ctest.ResponseWriter{})
	_, err := h.ServeDNS(context.Background(), w, req)
	require.NoError(t, err)

	// Both A records written, AAAA ignored.
	entries := rec.getEntries()
	require.Len(t, entries, 2)

	expectedHash := clawkerebpf.DomainHash("github.com")
	assert.Equal(t, expectedHash, entries[0].DomainHash)
	assert.Equal(t, expectedHash, entries[1].DomainHash)
	assert.NotEqual(t, entries[0].IP, entries[1].IP) // different IPs
}

func TestServeDNS_WildcardZoneUsesZoneHash(t *testing.T) {
	resp := new(dns.Msg)
	resp.SetReply(&dns.Msg{})
	resp.Answer = []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{Name: "sub.example.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 120},
			A:   net.ParseIP("93.184.216.34"),
		},
	}

	rec := &recordingMap{}
	h := Handler{
		Next: cannedHandler{msg: resp},
		Zone: ".example.com.", // wildcard zone
		Map:  rec,
	}

	req := new(dns.Msg)
	req.SetQuestion("sub.example.com.", dns.TypeA)

	w := dnstest.NewRecorder(&ctest.ResponseWriter{})
	_, err := h.ServeDNS(context.Background(), w, req)
	require.NoError(t, err)

	entries := rec.getEntries()
	require.Len(t, entries, 1)
	// Domain hash uses the ZONE name (.example.com), not the query name (sub.example.com).
	assert.Equal(t, clawkerebpf.DomainHash(".example.com"), entries[0].DomainHash)
}

func TestServeDNS_MinTTL(t *testing.T) {
	resp := new(dns.Msg)
	resp.SetReply(&dns.Msg{})
	resp.Answer = []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{Name: "example.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0},
			A:   net.ParseIP("93.184.216.34"),
		},
	}

	rec := &recordingMap{}
	h := Handler{
		Next: cannedHandler{msg: resp},
		Zone: "example.com.",
		Map:  rec,
	}

	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	w := dnstest.NewRecorder(&ctest.ResponseWriter{})
	_, err := h.ServeDNS(context.Background(), w, req)
	require.NoError(t, err)

	entries := rec.getEntries()
	require.Len(t, entries, 1)
	assert.Equal(t, uint32(60), entries[0].TTL) // 0 TTL clamped to 60s minimum
}

func TestZoneToDomain(t *testing.T) {
	tests := []struct {
		zone   string
		domain string
	}{
		{"github.com.", "github.com"},
		{".example.com.", ".example.com"},
		{".", ""},
		{"api.github.com.", "api.github.com"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.domain, zoneToDomain(tt.zone), "zone=%s", tt.zone)
	}
}
