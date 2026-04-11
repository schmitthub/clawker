// Package dnsbpf is a CoreDNS plugin that intercepts DNS responses and
// populates the clawker eBPF dns_cache map with resolved IP → domain hash
// mappings. This enables the BPF connect4 program to route per-domain TCP
// traffic (e.g., SSH to github.com vs gitlab.com on port 22) to the correct
// Envoy listener.
//
// The plugin uses nonwriter to capture responses from the next plugin in
// the chain (typically "forward"), extracts A records, and writes each
// resolved IP to the pinned BPF map at /sys/fs/bpf/clawker/dns_cache.
//
// Corefile usage:
//
//	github.com {
//	    dnsbpf
//	    forward . 1.1.1.2 1.0.0.2
//	}
package dnsbpf

import (
	"context"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/nonwriter"
	"github.com/miekg/dns"

	clawkerebpf "github.com/schmitthub/clawker/internal/ebpf"
)

// MapWriter is the interface for writing to the BPF dns_cache map.
// Implemented by BPFMap for production; fakes can be injected for tests.
type MapWriter interface {
	Update(ip, domainHash, ttlSeconds uint32)
}

// Handler implements plugin.Handler by intercepting DNS responses and
// writing A record results to the BPF dns_cache map.
type Handler struct {
	Next plugin.Handler
	Zone string    // Corefile zone this instance serves (e.g., "github.com.")
	Map  MapWriter // Shared BPF map writer (nil = skip writes)
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

// writeARecords extracts A records from the response and writes them to
// the BPF dns_cache map. The domain hash is computed from the Corefile zone
// name (not the query name) so wildcard zones work correctly.
func (h Handler) writeARecords(msg *dns.Msg) {
	// Use the zone name for the domain hash. For wildcard zones like
	// ".example.com", this ensures all subdomains map to the same hash
	// that the route_map uses.
	domain := zoneToDomain(h.Zone)
	domainHash := clawkerebpf.DomainHash(domain)

	for _, rr := range msg.Answer {
		a, ok := rr.(*dns.A)
		if !ok {
			continue
		}
		ip := clawkerebpf.IPToUint32(a.A)
		if ip == 0 {
			continue
		}
		ttl := rr.Header().Ttl
		if ttl == 0 {
			ttl = 60 // minimum 60s for short-TTL records
		}
		h.Map.Update(ip, domainHash, ttl)
	}
}

// zoneToDomain converts a CoreDNS zone name (FQDN with trailing dot) to
// the domain string used by the firewall manager for hashing.
// "github.com." → "github.com"
// ".example.com." → ".example.com" (wildcard)
func zoneToDomain(zone string) string {
	return strings.TrimSuffix(zone, ".")
}
