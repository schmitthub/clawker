package firewall

import (
	"fmt"
	"net"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

// Upstream DNS servers: Cloudflare malware-blocking resolvers.
var upstreamDNS = []string{"1.1.1.2", "1.0.0.2"}

// corefileLogFormat is the custom log format for DNS query logging.
// Uses logfmt-compatible key=value pairs for easy parsing by the OTEL
// collector / OpenSearch pipeline. CoreDNS placeholders: {name}=queried
// domain, {type}=query type (A/AAAA), {rcode}=response code
// (NOERROR/NXDOMAIN), {duration}=resolution time.
const corefileLogFormat = `source=coredns client_ip={remote} domain={name} qtype={type} rcode={rcode} duration={duration}`

// GenerateCorefile produces a CoreDNS Corefile from the given egress rules.
// healthPort is the port the CoreDNS health plugin listens on (inside the container).
//
// Only "allow" rules with domain destinations (not IPs/CIDRs) get forward zones.
// Each allowed domain gets its own zone forwarding to Cloudflare malware-blocking DNS.
// The catch-all "." zone returns NXDOMAIN for everything else.
func GenerateCorefile(rules []config.EgressRule, healthPort int) ([]byte, error) {
	var b strings.Builder

	// Docker internal names: forward to Docker's own embedded DNS (127.0.0.11).
	// CoreDNS runs on clawker-net, so its 127.0.0.11 can resolve container names
	// and host.docker.internal for all containers on the same network.
	// These zones ensure Docker networking works when resolv.conf points to CoreDNS.
	// They are reserved — egress rules matching these names are skipped from the
	// per-domain zones to avoid duplicate zone definitions that crash CoreDNS.
	//
	// Monitoring hostnames live in [consts.MonitoringServiceHostnames] so the
	// compose template and this list cannot drift — rename one and the other
	// follows by construction.
	internalHosts := append(
		[]string{"docker.internal"}, // host.docker.internal, gateway.docker.internal
		consts.MonitoringServiceHostnames...,
	)

	// Reserved zones — internal hosts get their own zones forwarding to Docker DNS.
	// Egress rules matching these names are skipped to avoid duplicate CoreDNS zones.
	reserved := make(map[string]bool, len(internalHosts))
	for _, host := range internalHosts {
		reserved[host] = true
	}

	// Collect unique allowed domains (skip IPs, CIDRs, deny rules, reserved names).
	emitted := make(map[string]bool)
	var domains []string
	for _, r := range rules {
		if !isAllowDomain(r) {
			continue
		}
		domain := normalizeDomain(r.Dst)
		if reserved[domain] || emitted[domain] {
			continue
		}
		emitted[domain] = true
		domains = append(domains, domain)
	}

	// Per-domain forward zones.
	// AAAA queries return NODATA (NOERROR with empty answer) because the eBPF
	// connect6 hook blocks all IPv6 to prevent dual-stack firewall bypass. Returning
	// AAAA records that can't connect misleads clients (e.g., npm/node don't fall
	// back on EPERM). NODATA tells clients to prefer IPv4.
	for _, domain := range domains {
		fmt.Fprintf(&b, "%s {\n", domain)
		fmt.Fprintf(&b, "    log . \"%s\"\n", corefileLogFormat)
		b.WriteString("    template IN AAAA . {\n")
		b.WriteString("        rcode NOERROR\n")
		b.WriteString("    }\n")
		b.WriteString("    dnsbpf\n")
		fmt.Fprintf(&b, "    forward . %s\n", strings.Join(upstreamDNS, " "))
		fmt.Fprintf(&b, "}\n\n")
	}

	// Internal host forward zones (Docker DNS).
	// dnsbpf is included so host.docker.internal resolution populates dns_cache.
	// AAAA NODATA applied for the same IPv6-block reason as public zones.
	for _, host := range internalHosts {
		fmt.Fprintf(&b, "%s {\n", host)
		fmt.Fprintf(&b, "    log . \"%s\"\n", corefileLogFormat)
		b.WriteString("    template IN AAAA . {\n")
		b.WriteString("        rcode NOERROR\n")
		b.WriteString("    }\n")
		b.WriteString("    dnsbpf\n")
		b.WriteString("    forward . 127.0.0.11\n")
		b.WriteString("}\n\n")
	}

	// Catch-all zone: NXDOMAIN for everything not explicitly allowed.
	b.WriteString(". {\n")
	fmt.Fprintf(&b, "    log . \"%s\"\n", corefileLogFormat)
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

// normalizeDomain strips any leading dot (wildcard indicator) and trailing dot
// (FQDN indicator) from a domain name.
func normalizeDomain(d string) string {
	d = strings.TrimPrefix(d, ".")
	return strings.TrimSuffix(d, ".")
}

// isWildcardDomain returns true if the domain uses the leading-dot convention
// (e.g., ".datadoghq.com") to indicate that all subdomains should be matched.
func isWildcardDomain(d string) bool {
	return strings.HasPrefix(d, ".")
}
