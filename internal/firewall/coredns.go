package firewall

import (
	"fmt"
	"net"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
)

// Upstream DNS servers: Cloudflare malware-blocking resolvers.
var upstreamDNS = []string{"1.1.1.2", "1.0.0.2"}

// GenerateCorefile produces a CoreDNS Corefile from the given egress rules.
// healthPort is the port the CoreDNS health plugin listens on (inside the container).
//
// Only "allow" rules with domain destinations (not IPs/CIDRs) get forward zones.
// Each allowed domain gets its own zone forwarding to Cloudflare malware-blocking DNS.
// The catch-all "." zone returns NXDOMAIN for everything else.
func GenerateCorefile(rules []config.EgressRule, healthPort int) ([]byte, error) {
	var b strings.Builder

	// Collect unique allowed domains (skip IPs, CIDRs, deny rules).
	seen := make(map[string]bool)
	var domains []string
	for _, r := range rules {
		if !isAllowDomain(r) {
			continue
		}
		domain := normalizeDomain(r.Dst)
		if seen[domain] {
			continue
		}
		seen[domain] = true
		domains = append(domains, domain)
	}

	// Per-domain forward zones.
	for _, domain := range domains {
		fmt.Fprintf(&b, "%s {\n", domain)
		fmt.Fprintf(&b, "    forward . %s\n", strings.Join(upstreamDNS, " "))
		fmt.Fprintf(&b, "}\n\n")
	}

	// Docker internal names: forward to Docker's own embedded DNS (127.0.0.11).
	// CoreDNS runs on clawker-net, so its 127.0.0.11 can resolve container names
	// and host.docker.internal for all containers on the same network.
	// These zones ensure Docker networking works when resolv.conf points to CoreDNS.
	internalHosts := []string{
		"docker.internal", // host.docker.internal, gateway.docker.internal
		"otel-collector",  // monitoring: OpenTelemetry collector
		"jaeger",          // monitoring: Jaeger tracing
		"prometheus",      // monitoring: Prometheus metrics
		"loki",            // monitoring: Loki log aggregation
		"grafana",         // monitoring: Grafana dashboards
	}
	for _, host := range internalHosts {
		fmt.Fprintf(&b, "%s {\n", host)
		b.WriteString("    forward . 127.0.0.11\n")
		b.WriteString("}\n\n")
	}

	// Catch-all zone: NXDOMAIN for everything not explicitly allowed.
	b.WriteString(". {\n")
	b.WriteString("    template IN ANY . {\n")
	b.WriteString("        rcode NXDOMAIN\n")
	b.WriteString("    }\n")
	fmt.Fprintf(&b, "    health :%d\n", healthPort)
	b.WriteString("    reload 2s\n")
	b.WriteString("}\n")

	return []byte(b.String()), nil
}

// isAllowDomain returns true if the rule is an allow rule targeting a domain
// (not an IP address or CIDR range).
func isAllowDomain(r config.EgressRule) bool {
	action := strings.ToLower(r.Action)
	if action != "allow" && action != "" {
		return false
	}
	return !isIPOrCIDR(r.Dst)
}

// isIPOrCIDR returns true if s is an IP address or CIDR notation.
func isIPOrCIDR(s string) bool {
	if net.ParseIP(s) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(s)
	return err == nil
}

// normalizeDomain strips any trailing dot from a domain name.
func normalizeDomain(d string) string {
	return strings.TrimSuffix(d, ".")
}
