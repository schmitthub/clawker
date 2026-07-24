// Package dnsbpf is a CoreDNS plugin that intercepts DNS responses and
// populates the clawker eBPF dns_cache map with resolved IP → route identity
// mappings. This enables the BPF connect4 program to route per-domain TCP
// traffic (e.g., SSH to github.com vs gitlab.com on port 22) to the correct
// Envoy listener.
//
// The plugin uses nonwriter to capture responses from the next plugin in
// the chain (typically "forward"), extracts A records, and writes each
// resolved IP to the pinned BPF map at /sys/fs/bpf/clawker/dns_cache.
//
// The route identity is allocated CP-side (firewall.IdentityAllocator) and
// delivered as the directive's argument by the Corefile generator — the
// plugin derives nothing itself, so the dns_cache and route_map keyspaces
// can never drift.
//
// Corefile usage:
//
//	github.com {
//	    dnsbpf 261
//	    forward . 1.1.1.2 1.0.0.2
//	}
package dnsbpf

import (
	"context"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/nonwriter"
	"github.com/miekg/dns"

	clawkerebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
)

// minTTLSeconds is the floor for dns_cache entry TTLs. Very short upstream
// TTLs (CDNs commonly serve 1-30s) would otherwise thrash the cache: the
// userspace GC could sweep an entry between the DNS answer and the app's
// connect(), denying a just-resolved destination. Cilium applies the same
// clamp via tofqdns-min-ttl.
const minTTLSeconds = 60

// MapWriter is the interface for writing to the BPF dns_cache map.
// Implemented by BPFMap for production; fakes can be injected for tests.
type MapWriter interface {
	Update(ip uint32, identity clawkerebpf.RouteIdentity, ttlSeconds uint32)
}

// Handler implements plugin.Handler by intercepting DNS responses and
// writing A record results to the BPF dns_cache map.
type Handler struct {
	Next plugin.Handler
	Zone string // Corefile zone this instance serves (e.g., "github.com.") — diagnostic context only, not read at runtime
	// Identity is the CP-allocated route identity for this zone, parsed
	// from the dnsbpf directive argument.
	Identity clawkerebpf.RouteIdentity
	Map      MapWriter // Shared BPF map writer (nil = skip writes)
}

// Name returns the plugin name as registered with CoreDNS.
func (h Handler) Name() string { return pluginName }

// ServeDNS intercepts the DNS response from the next plugin and writes
// A record IPs to the BPF dns_cache map.
func (h Handler) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	// Capture the response from the next plugin.
	nw := nonwriter.New(w)
	rcode, err := plugin.NextOrFailure(h.Name(), h.Next, ctx, nw, r)
	if err != nil {
		return rcode, err
	}
	if nw.Msg == nil {
		return rcode, err
	}

	// Write A records to BPF map.
	if h.Map != nil {
		h.writeARecords(nw.Msg)
	}

	// Forward the original response to the client.
	if err := w.WriteMsg(nw.Msg); err != nil {
		return dns.RcodeServerFailure, err
	}
	return rcode, nil
}

// writeARecords extracts A records from the response and writes them to the
// BPF dns_cache map under the zone's CP-allocated identity. Keying by the
// zone identity (not the query name) is what makes wildcard zones work: all
// subdomains map to the identity the route_map uses.
func (h Handler) writeARecords(msg *dns.Msg) {
	for _, rr := range msg.Answer {
		a, ok := rr.(*dns.A)
		if !ok {
			continue
		}
		ip := clawkerebpf.IPToUint32(a.A)
		if ip == 0 {
			continue
		}
		ttl := max(rr.Header().Ttl, minTTLSeconds)
		h.Map.Update(ip, h.Identity, ttl)
	}
}
