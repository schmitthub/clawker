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
// Uses logfmt-compatible key=value pairs for `docker logs clawker-coredns`
// triage only — the OpenSearch pipeline ingests DNS events from the
// in-tree `otel` plugin (OTLP/gRPC + mTLS), not from this stdout sink.
// CoreDNS placeholders: {name}=queried domain, {type}=query type
// (A/AAAA), {rcode}=response code (NOERROR/NXDOMAIN), {duration}=
// resolution time.
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

	// First pass: record which allowed domains carry a wildcard (.X) rule and
	// which domains are explicitly denied. An exact-only allow (X, no wildcard)
	// must NXDOMAIN its subdomains — the same exact-host contract Envoy's SNI
	// matching enforces — while a wildcard allow forwards the whole subtree.
	// Deny domains become dedicated NXDOMAIN zones that win over any broader
	// allow via CoreDNS longest-zone matching (e.g. deny sub.X under .X).
	wildcard := make(map[string]bool)
	denySeen := make(map[string]bool)
	var denyDomains []string
	for _, r := range rules {
		if isIPOrCIDR(r.Dst) {
			continue
		}
		domain := normalizeDomain(r.Dst)
		if reserved[domain] {
			continue
		}
		switch {
		case isDenyRule(r):
			if !denySeen[domain] {
				denySeen[domain] = true
				denyDomains = append(denyDomains, domain)
			}
		case isAllowDomain(r) && isWildcardDomain(r.Dst):
			wildcard[domain] = true
		}
	}

	// Collect unique allowed domains (skip IPs, CIDRs, deny rules, reserved
	// names, and any name that is also explicitly denied — the deny zone wins).
	emitted := make(map[string]bool)
	var allowDomains []string
	for _, r := range rules {
		if !isAllowDomain(r) {
			continue
		}
		domain := normalizeDomain(r.Dst)
		if reserved[domain] || emitted[domain] || denySeen[domain] {
			continue
		}
		emitted[domain] = true
		allowDomains = append(allowDomains, domain)
	}

	// Per-domain forward zones. Exact-only domains additionally NXDOMAIN every
	// subdomain (see writeAllowZone); wildcard domains forward the whole subtree.
	for _, domain := range allowDomains {
		writeAllowZone(&b, domain, upstreamDNS, !wildcard[domain])
	}

	// Deny zones: NXDOMAIN the domain and its entire subtree, beating any
	// broader allow zone via CoreDNS longest-zone matching.
	for _, domain := range denyDomains {
		writeDenyZone(&b, domain)
	}

	// Internal host forward zones (Docker DNS). Never exact-scoped — e.g.
	// host.docker.internal is a subdomain of docker.internal and must resolve.
	for _, host := range internalHosts {
		writeAllowZone(&b, host, []string{"127.0.0.11"}, false)
	}

	// Catch-all zone: NXDOMAIN for everything not explicitly allowed.
	b.WriteString(". {\n")
	b.WriteString("    otel\n")
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

// isDenyRule reports whether r explicitly denies a domain destination.
// IP/CIDR deny rules are handled by Envoy/eBPF, not CoreDNS.
func isDenyRule(r config.EgressRule) bool {
	return strings.EqualFold(r.Action, "deny") && !isIPOrCIDR(r.Dst)
}

// subdomainRegex builds a CoreDNS template `match` regex that matches any
// subdomain of domain — but not the apex — against the trailing-dot FQDN query
// name. Dots become "[.]" character classes (the only regex metacharacter a
// domain name can contain), so no escaping is needed and the apex never matches
// because it lacks the leading separator dot. E.g. "api.github.com" ->
// "[.]api[.]github[.]com[.]$" matches "x.api.github.com." but not
// "api.github.com.".
func subdomainRegex(domain string) string {
	return "[.]" + strings.ReplaceAll(domain, ".", "[.]") + "[.]$"
}

// writeAllowZone writes a forward zone for domain.
//
// When exactOnly is true, the zone NXDOMAINs every subdomain of domain and
// forwards only the apex — the exact-host contract Envoy's SNI matching already
// enforces. The subdomain template carries `fallthrough` so the apex (which the
// subdomainRegex does not match) passes through to forward; without it CoreDNS
// returns SERVFAIL on a zone-match-but-no-regex-match. The IN ANY subdomain
// template is ordered before the AAAA template so subdomains are NXDOMAINed for
// every qtype.
//
// When exactOnly is false the whole subtree is forwarded (wildcard rules and
// internal hosts such as docker.internal, whose host.docker.internal subdomain
// must resolve).
//
// AAAA queries return NODATA (NOERROR with no answer) because the eBPF connect6
// hook blocks all IPv6 to prevent dual-stack firewall bypass. Returning AAAA
// records that can't connect misleads clients (e.g. npm/node don't fall back on
// EPERM); NODATA tells clients to prefer IPv4.
func writeAllowZone(b *strings.Builder, domain string, upstreams []string, exactOnly bool) {
	fmt.Fprintf(b, "%s {\n", domain)
	b.WriteString("    otel\n")
	fmt.Fprintf(b, "    log . \"%s\"\n", corefileLogFormat)
	if exactOnly {
		b.WriteString("    template IN ANY . {\n")
		fmt.Fprintf(b, "        match \"%s\"\n", subdomainRegex(domain))
		b.WriteString("        rcode NXDOMAIN\n")
		b.WriteString("        fallthrough\n")
		b.WriteString("    }\n")
	}
	b.WriteString("    template IN AAAA . {\n")
	b.WriteString("        rcode NOERROR\n")
	b.WriteString("    }\n")
	b.WriteString("    dnsbpf\n")
	fmt.Fprintf(b, "    forward . %s\n", strings.Join(upstreams, " "))
	b.WriteString("}\n\n")
}

// writeDenyZone writes a zone that NXDOMAINs domain and its entire subtree.
// CoreDNS longest-zone matching makes this win over any broader allow zone
// (e.g. deny sub.example.com under an allowed .example.com wildcard). otel/log
// stay so denied lookups remain observable; there is no forward and no dnsbpf
// because a denied name has no upstream answer to cache.
//
// Deny is always subtree-scoped: normalizeDomain strips any leading dot, so an
// exact deny (sub.X) and a wildcard deny (.sub.X) collapse to the same sub.X
// zone — the denied name's apex is blocked along with everything beneath it.
func writeDenyZone(b *strings.Builder, domain string) {
	fmt.Fprintf(b, "%s {\n", domain)
	b.WriteString("    otel\n")
	fmt.Fprintf(b, "    log . \"%s\"\n", corefileLogFormat)
	b.WriteString("    template IN ANY . {\n")
	b.WriteString("        rcode NXDOMAIN\n")
	b.WriteString("    }\n")
	b.WriteString("}\n\n")
}
