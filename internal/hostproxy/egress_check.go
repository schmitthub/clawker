package hostproxy

import (
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// egressRule is a local copy of config.EgressRule's YAML-relevant fields.
// Avoids importing internal/config, which would violate the package DAG.
type egressRule struct {
	Dst         string     `yaml:"dst"`
	Proto       string     `yaml:"proto,omitempty"`
	Port        int        `yaml:"port,omitempty"`
	Action      string     `yaml:"action,omitempty"`
	PathRules   []pathRule `yaml:"path_rules,omitempty"`
	PathDefault string     `yaml:"path_default,omitempty"`
}

// pathRule is a local copy of config.PathRule's YAML-relevant fields.
type pathRule struct {
	Path   string `yaml:"path"`
	Action string `yaml:"action"`
}

// egressRulesFile matches the YAML structure of the egress rules file managed
// by the firewall package. Only YAML tags are needed for read-only parsing.
type egressRulesFile struct {
	Rules []egressRule `yaml:"rules"`
}

// CheckURLAgainstEgressRules checks whether targetURL is permitted by the egress
// rules in rulesFilePath. Returns nil if allowed, an error describing the block
// reason otherwise. The rules file is read on every call (no caching) with a
// The firewall daemon writes atomically (temp+fsync+rename), so no locking needed.
func CheckURLAgainstEgressRules(targetURL, rulesFilePath string) error {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	proto, defaultPort, err := schemeToProto(parsed.Scheme)
	if err != nil {
		return err
	}

	// Reject URLs with userinfo or opaque forms — no legitimate browser URL uses these.
	if parsed.User != nil {
		return fmt.Errorf("URL with userinfo is not allowed")
	}
	if parsed.Opaque != "" {
		return fmt.Errorf("opaque URL is not allowed")
	}

	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}

	port := defaultPort
	if parsed.Port() != "" {
		port, err = strconv.Atoi(parsed.Port())
		if err != nil {
			return fmt.Errorf("invalid port %q: %w", parsed.Port(), err)
		}
	}

	rules, err := readEgressRules(rulesFilePath)
	if err != nil {
		// If we can't read rules, block by default (fail closed).
		return fmt.Errorf("cannot read egress rules: %w", err)
	}

	path := parsed.Path
	if path == "" {
		path = "/"
	}

	return matchRules(rules, host, proto, port, path)
}

// schemeToProto maps a URL scheme to the egress rule proto and its default port.
func schemeToProto(scheme string) (proto string, defaultPort int, err error) {
	switch strings.ToLower(scheme) {
	case "https":
		return "tls", 443, nil
	case "http":
		return "http", 80, nil
	default:
		return "", 0, fmt.Errorf("unsupported URL scheme %q", scheme)
	}
}

// readEgressRules reads and parses the egress rules file. The firewall daemon
// writes this file atomically (temp+fsync+rename), so concurrent reads always
// see a complete snapshot — no locking needed.
func readEgressRules(path string) ([]egressRule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var f egressRulesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing egress rules: %w", err)
	}

	return f.Rules, nil
}

// matchRules checks if the given host/proto/port/path combination is allowed by
// the rule set. Exact domain matches always take priority over wildcard matches
// regardless of rule ordering — this prevents a wildcard allow from shadowing
// an exact deny (or vice versa). Returns nil if allowed, an error if blocked or
// no matching rule.
func matchRules(rules []egressRule, host, proto string, port int, path string) error {
	var wildcardMatch *egressRule

	// Pass 1: find the best match. Exact domain wins; wildcard is fallback.
	for i := range rules {
		r := normalizeEgressRule(rules[i])

		if !strings.EqualFold(r.Proto, proto) || r.Port != port {
			continue
		}

		matchType := dstMatchType(r.Dst, host)
		if matchType == matchNone {
			continue
		}

		if matchType == matchExact {
			return evaluateRule(r, host, path)
		}

		// Wildcard match — remember first one as fallback.
		if wildcardMatch == nil {
			normalized := r
			wildcardMatch = &normalized
		}
	}

	if wildcardMatch != nil {
		return evaluateRule(*wildcardMatch, host, path)
	}

	return fmt.Errorf("domain %q is not in the egress allow list", host)
}

// evaluateRule checks a matched rule's action and path rules.
func evaluateRule(r egressRule, host, path string) error {
	if !strings.EqualFold(r.Action, "allow") {
		return fmt.Errorf("domain %q is denied by egress rules", host)
	}
	if len(r.PathRules) > 0 {
		return checkPathRules(r.PathRules, r.PathDefault, host, path)
	}
	return nil
}

// checkPathRules evaluates path-level rules using longest-prefix matching.
func checkPathRules(rules []pathRule, pathDefault, host, urlPath string) error {
	bestLen := -1
	action := ""

	for _, pr := range rules {
		if pr.Path == "" {
			continue
		}
		if strings.HasPrefix(urlPath, pr.Path) && len(pr.Path) > bestLen {
			bestLen = len(pr.Path)
			action = pr.Action
		}
	}

	// No path rule matched; use path_default (fail-closed: deny if unset).
	if action == "" {
		action = pathDefault
		if action == "" {
			action = "deny"
		}
	}

	if !strings.EqualFold(action, "allow") {
		return fmt.Errorf("path %q on %q is denied by egress path rules", urlPath, host)
	}
	return nil
}

// normalizeEgressRule applies the same defaults as firewall.normalizeRule:
// empty proto → "tls", empty action → "allow", TLS with port 0 → 443.
func normalizeEgressRule(r egressRule) egressRule {
	if r.Proto == "" {
		r.Proto = "tls"
	}
	if r.Action == "" {
		r.Action = "allow"
	}
	if r.Port == 0 && strings.EqualFold(r.Proto, "tls") {
		r.Port = 443
	}
	return r
}

// matchKind classifies how a rule destination matched a host.
type matchKind int

const (
	matchNone     matchKind = iota // no match
	matchWildcard                  // wildcard suffix match (.example.com → sub.example.com)
	matchExact                     // exact domain, IP, or CIDR match
)

// dstMatchType returns how host matches the rule destination dst.
func dstMatchType(dst, host string) matchKind {
	// Try CIDR match first (dst contains "/").
	if strings.Contains(dst, "/") {
		prefix, err := netip.ParsePrefix(dst)
		if err == nil {
			hostIP, err := netip.ParseAddr(host)
			if err == nil && prefix.Contains(hostIP) {
				return matchExact
			}
		}
		return matchNone
	}

	// Try IP exact match (dst parses as an IP address).
	if dstIP, err := netip.ParseAddr(dst); err == nil {
		hostIP, err := netip.ParseAddr(host)
		if err == nil && dstIP == hostIP {
			return matchExact
		}
		return matchNone
	}

	return domainMatchType(dst, host)
}

// domainMatchType classifies how host matches a domain rule destination.
// Wildcard rules start with "." (e.g., ".claude.ai") and match subdomains.
// A wildcard also matches the bare apex ONLY as a fallback — exact rules
// for the apex always take priority (enforced by matchRules' two-pass scan).
func domainMatchType(dst, host string) matchKind {
	dst = strings.ToLower(strings.TrimSuffix(dst, "."))
	host = strings.ToLower(strings.TrimSuffix(host, "."))

	if !strings.HasPrefix(dst, ".") {
		if dst == host {
			return matchExact
		}
		return matchNone
	}

	// Wildcard: ".claude.ai" matches "sub.claude.ai" (wildcard)
	// and "claude.ai" itself (wildcard fallback — exact rules win if present).
	bare := dst[1:] // strip leading dot
	if strings.HasSuffix(host, dst) {
		return matchWildcard
	}
	if host == bare {
		return matchWildcard
	}
	return matchNone
}
