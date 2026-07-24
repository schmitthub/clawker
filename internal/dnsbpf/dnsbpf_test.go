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

	clawkerebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
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
	IP       uint32
	Identity clawkerebpf.RouteIdentity
	TTL      uint32
}

func (r *recordingMap) Update(ip uint32, identity clawkerebpf.RouteIdentity, ttl uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, mapEntry{ip, identity, ttl})
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
		Next:     cannedHandler{msg: resp},
		Zone:     "github.com.",
		Identity: 261,
		Map:      rec, // non-nil — exercises writeARecords
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
	assert.Equal(t, clawkerebpf.RouteIdentity(261), entries[0].Identity)
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
		Next:     cannedHandler{msg: resp},
		Zone:     "github.com.",
		Identity: 261,
		Map:      rec,
	}

	req := new(dns.Msg)
	req.SetQuestion("github.com.", dns.TypeA)

	w := dnstest.NewRecorder(&ctest.ResponseWriter{})
	_, err := h.ServeDNS(context.Background(), w, req)
	require.NoError(t, err)

	// Both A records written, AAAA ignored.
	entries := rec.getEntries()
	require.Len(t, entries, 2)

	assert.Equal(t, clawkerebpf.RouteIdentity(261), entries[0].Identity)
	assert.Equal(t, clawkerebpf.RouteIdentity(261), entries[1].Identity)
	assert.NotEqual(t, entries[0].IP, entries[1].IP) // different IPs
}

func TestServeDNS_WildcardZoneUsesZoneIdentity(t *testing.T) {
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
		Next:     cannedHandler{msg: resp},
		Zone:     ".example.com.", // wildcard zone
		Identity: 300,
		Map:      rec,
	}

	req := new(dns.Msg)
	req.SetQuestion("sub.example.com.", dns.TypeA)

	w := dnstest.NewRecorder(&ctest.ResponseWriter{})
	_, err := h.ServeDNS(context.Background(), w, req)
	require.NoError(t, err)

	entries := rec.getEntries()
	require.Len(t, entries, 1)
	// The identity is the ZONE's identity — subdomain answers inherit it,
	// which is what makes wildcard zones route correctly.
	assert.Equal(t, clawkerebpf.RouteIdentity(300), entries[0].Identity)
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
		Next:     cannedHandler{msg: resp},
		Zone:     "example.com.",
		Identity: 301,
		Map:      rec,
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

func TestServeDNS_MinTTLClampsShortTTLs(t *testing.T) {
	resp := new(dns.Msg)
	resp.SetReply(new(dns.Msg))
	// CDN-style 5s TTL: must be clamped, not written through — the
	// userspace GC could otherwise sweep the entry between the answer
	// and the app connect().
	var hdr dns.RR_Header
	hdr.Name = "cdn.example.com."
	hdr.Rrtype = dns.TypeA
	hdr.Class = dns.ClassINET
	hdr.Ttl = 5
	resp.Answer = []dns.RR{&dns.A{Hdr: hdr, A: net.ParseIP("93.184.216.35")}}

	rec := new(recordingMap)
	h := Handler{
		Next:     cannedHandler{msg: resp},
		Zone:     "cdn.example.com.",
		Identity: 302,
		Map:      rec,
	}

	req := new(dns.Msg)
	req.SetQuestion("cdn.example.com.", dns.TypeA)

	w := dnstest.NewRecorder(new(ctest.ResponseWriter))
	_, err := h.ServeDNS(context.Background(), w, req)
	require.NoError(t, err)

	entries := rec.getEntries()
	require.Len(t, entries, 1)
	assert.Equal(t, uint32(60), entries[0].TTL)
}
