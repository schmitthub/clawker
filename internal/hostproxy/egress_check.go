package hostproxy

import (
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// egressRule is a local copy of config.EgressRule's YAML-relevant fields.
// Avoids importing internal/config, which would violate the package DAG.
type egressRule struct {
	Dst   string `yaml:"dst"`
	Proto string `yaml:"proto,omitempty"`
	// Port is the dynamic port spec mirrored from config.EgressRule: a single
	// port ("443") or an inclusive range ("9000-9100"). It MUST be a string —
	// the firewall writes numeric-looking values quoted (port: "443") and range
	// specs, both of which fail to unmarshal into an int and would poison the
	// whole rules file (fail-closed → every /open/url blocked).
	Port        string     `yaml:"port,omitempty"`
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

	return matchRules(rules, host, proto, port, canonicalizePath(parsed.Path))
}

// canonicalizePath collapses a URL path to the form the origin server will
// actually resolve, so the path the rules match equals the path the host
// browser fetches. Without this, an agent prefixes an allowed path and
// "../"s out to a denied one — defeating path_default:deny entirely (e.g.
// /schmitthub/clawker/../../victim against a per-repo allowlist).
//
// Two normalizations happen between the string we validate and the bytes the
// origin serves, and we must replicate both or the matcher and the fetch
// disagree:
//
//   - Backslashes. For http/https the WHATWG URL parser the host browser uses
//     folds '\' to '/', so /v1/..\secret reaches the server as /v1/../secret.
//     path.Clean is POSIX and treats '\' as an ordinary character, so we fold
//     first or a backslash-disguised "../" sails straight through.
//   - Dot-segments and duplicate slashes. path.Clean resolves "." / ".."
//     (RFC 3986 §5.2.4) and merges "//". net/url has already percent-decoded
//     the path by the time we see it (%2e->'.', %2f->'/'), so the encoded
//     traversal variants collapse here too. Decoding-then-cleaning is
//     intentionally stricter than a spec-compliant server (which keeps %2f
//     literal) — the correct fail-closed direction for an allowlist.
//
// path.Clean strips a trailing slash, so restore it when the input had one:
// a directory-prefix rule like "/schmitthub/" must still match a request to
// the bare directory.
func canonicalizePath(p string) string {
	if p == "" {
		return "/"
	}
	p = strings.ReplaceAll(p, "\\", "/")
	cleaned := path.Clean(p)
	if strings.HasSuffix(p, "/") && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned
}

// schemeToProto maps a URL scheme to the egress rule proto and its default port.
func schemeToProto(scheme string) (proto string, defaultPort int, err error) {
	switch strings.ToLower(scheme) {
	case "https":
		return "https", 443, nil
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

		if !strings.EqualFold(r.Proto, proto) || !portSpecMatches(r.Port, port) {
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

	// No path rule matched; resolve via the same allowlist/denylist inference
	// the firewall side uses (firewall.EffectivePathDefault) so a denylist rule
	// like `firewall add foo.com --path /admin --action deny` produces matching
	// catch-all semantics across Envoy and the host proxy.
	if action == "" {
		action = effectivePathDefault(rules, pathDefault)
	}

	if !strings.EqualFold(action, "allow") {
		return fmt.Errorf("path %q on %q is denied by egress path rules", urlPath, host)
	}
	return nil
}

// effectivePathDefault mirrors firewall.EffectivePathDefault: explicit
// path_default wins; otherwise infer "deny" when any PathRule is allow
// (allowlist mode) and "allow" when every PathRule is deny (denylist mode).
// Must stay in lockstep with the firewall implementation — the same
// egress-rules.yaml must enforce the same catch-all on both paths.
func effectivePathDefault(rules []pathRule, pathDefault string) string {
	if pathDefault != "" {
		return pathDefault
	}
	for _, pr := range rules {
		if strings.EqualFold(pr.Action, "allow") {
			return "deny"
		}
	}
	return "allow"
}

// normalizeEgressRule applies the same defaults as firewall.NormalizeRule:
// legacy proto:"tls" → "https", empty proto → "https", empty action → "allow",
// https with port 0 → 443, http with port 0 → 80, ssh with port 0 → 22.
func normalizeEgressRule(r egressRule) egressRule {
	if strings.EqualFold(r.Proto, "tls") {
		r.Proto = "https"
	}
	if r.Proto == "" {
		r.Proto = "https"
	}
	if r.Action == "" {
		r.Action = "allow"
	}
	if r.Port == "" {
		switch strings.ToLower(r.Proto) {
		case "https":
			r.Port = "443"
		case "http":
			r.Port = "80"
		case "ssh":
			r.Port = "22"
		}
	}
	return r
}

// portSpecMatches reports whether the request port p satisfies the rule's
// dynamic port spec: a single port ("443") or an inclusive range ("9000-9100").
// A range only ever attaches to opaque protos (tcp/ssh/udp); since /open/url
// handles http/https only, a range never matches a browser URL in practice —
// but the membership check is correct regardless.
//
// It MUST parse identically to config.ParsePortSpec, which is the boundary the
// firewall validates every spec through before writing egress-rules.yaml. The
// package DAG forbids importing internal/config, so the logic is duplicated —
// keep it in lockstep. The divergence matters in the DENY direction: a deny
// rule the firewall accepted but this function fails to parse would silently
// not match and fall through to a wildcard allow, opening an exfil hole. An
// empty/malformed/out-of-range spec matches nothing.
func portSpecMatches(spec string, p int) bool {
	lo, hi, ok := parsePortSpec(spec)
	return ok && p >= lo && p <= hi
}

// parsePortSpec mirrors config.ParsePortSpec: it trims surrounding whitespace,
// accepts a single port ("443") or an inclusive range ("9000-9100"), bounds-
// checks every number to 1-65535, and rejects reversed ranges (lo>hi). ok is
// false for an empty, malformed, or out-of-range spec.
func parsePortSpec(spec string) (lo, hi int, ok bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 0, 0, false
	}
	if left, right, isRange := strings.Cut(spec, "-"); isRange {
		l, okLo := parsePortNumber(left)
		h, okHi := parsePortNumber(right)
		if !okLo || !okHi || l > h {
			return 0, 0, false
		}
		return l, h, true
	}
	n, okN := parsePortNumber(spec)
	if !okN {
		return 0, 0, false
	}
	return n, n, true
}

// parsePortNumber parses a single whitespace-trimmed, bounds-checked port.
func parsePortNumber(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 0xffff {
		return 0, false
	}
	return n, true
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
