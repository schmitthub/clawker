// Command coredns-clawker is a custom CoreDNS build embedding two in-tree
// plugins: dnsbpf and otel.
//
// dnsbpf intercepts DNS responses and writes IP -> {domain_hash, TTL}
// entries to the eBPF dns_cache map, enabling real-time BPF-based routing
// decisions without stale seed data.
//
// otel exports one structured dns.query log record per DNS query over
// OTLP/gRPC + mTLS to the CP-only collector receiver, feeding the
// monitoring stack with per-query observability.
//
// The init() below prepends both directives to dnsserver.Directives so
// otel sees every final response and dnsbpf still wraps all resolver
// plugins.
//
// Built by: make coredns-binary
// Embedded in clawker via go:embed (internal/controlplane/firewall/embed_coredns.go)
// and built into a Docker image on-demand by the CP firewall Stack.
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

	_ "github.com/schmitthub/clawker/cmd/coredns-clawker/plugins/otel"
	_ "github.com/schmitthub/clawker/internal/dnsbpf"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/coremain"
)

func init() {
	// Insert otel and dnsbpf at the front of the directive chain so the
	// OTEL exporter sees every final DNS response and dnsbpf still wraps all
	// resolver plugins (forward, hosts, etc.) to observe A answers before
	// they leave the process.
	dnsserver.Directives = append([]string{"otel", "dnsbpf"}, dnsserver.Directives...)
}

func main() {
	coremain.Run()
}
