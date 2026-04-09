// Command coredns-clawker is a custom CoreDNS build embedding the dnsbpf plugin.
//
// The dnsbpf plugin intercepts DNS responses and writes IP -> {domain_hash, TTL}
// entries to the eBPF dns_cache map, enabling real-time BPF-based routing
// decisions without stale seed data.
//
// Built by: make coredns-binary
// Embedded in clawker via go:embed (internal/firewall/coredns_embed.go)
// and built into a Docker image on-demand by the firewall manager.
package main

import (
	// Standard CoreDNS plugins used by our Corefile. coremain does NOT
	// transitively import these — zplugin.go's blank imports must be
	// replicated here for each plugin we use. We import only the plugins
	// our Corefile needs (not the full core/plugin set which drags in
	// Azure, AWS, K8s, etc.).
	_ "github.com/coredns/coredns/plugin/forward"
	_ "github.com/coredns/coredns/plugin/health"
	_ "github.com/coredns/coredns/plugin/log"
	_ "github.com/coredns/coredns/plugin/reload"
	_ "github.com/coredns/coredns/plugin/template"

	_ "github.com/schmitthub/clawker/internal/dnsbpf"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/coremain"
)

func init() {
	// Insert dnsbpf at the front of the directive chain so it wraps all
	// resolver plugins (forward, hosts, etc.) and sees every DNS response.
	dnsserver.Directives = append([]string{"dnsbpf"}, dnsserver.Directives...)
}

func main() {
	coremain.Run()
}
