package firewall

import (
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/storage"
)

// EgressRulesFile is the top-level document type for storage.Store[T].
// It persists the active set of project-level egress rules to disk.
type EgressRulesFile struct {
	Rules []config.EgressRule `yaml:"rules" label:"Rules" desc:"Active egress firewall rules"`
}

// Fields implements [storage.Schema] for EgressRulesFile.
func (f EgressRulesFile) Fields() storage.FieldSet {
	return storage.NormalizeFields(f)
}

// ProjectRules builds the full rule set from project config and required
// rules. Called CLI-side before BootstrapServicesPostStart composes the
// initial rule set that is pushed via FirewallAddRules.
func ProjectRules(cfg config.Config) []config.EgressRule {
	var rules []config.EgressRule
	rules = append(rules, cfg.RequiredFirewallRules()...)
	projectFw := cfg.Project().Security.Firewall
	if projectFw != nil {
		rules = append(rules, projectFw.Rules...)
		for _, d := range projectFw.AddDomains {
			rules = append(rules, config.EgressRule{Dst: d, Proto: "tls", Port: 443, Action: "allow"})
		}
	}
	return rules
}

// NewRulesStore creates a storage.Store[EgressRulesFile] for egress-rules.yaml.
// The store uses the firewall data subdirectory for file discovery.
func NewRulesStore(cfg config.Config) (*storage.Store[EgressRulesFile], error) {
	dataDir, err := cfg.FirewallDataSubdir()
	if err != nil {
		return nil, fmt.Errorf("firewall: resolving data dir: %w", err)
	}
	return storage.NewStore[EgressRulesFile](
		storage.WithFilenames(cfg.EgressRulesFileName()),
		storage.WithPaths(dataDir),
		storage.WithLock(), // Cross-process flock — multiple CLI/daemon instances share this file.
	)
}

// ValidateDst checks that a destination is a valid lowercase domain name,
// wildcard domain, IP address, or CIDR block. Exported so CLI commands can
// pre-validate before attempting store mutations.
//
// Domain validation is based on Go's net.isDomainName (RFC 1035 / RFC 1123
// label constraints) with two deliberate deviations: uppercase is rejected to
// enforce normalized storage, and the root domain "." is not accepted.
// Underscores are allowed for SRV/DMARC compatibility.
func ValidateDst(dst string) error {
	if dst == "" {
		return fmt.Errorf("empty destination")
	}

	// Strip wildcard prefix and FQDN trailing dot for validation.
	normalized := normalizeDomain(dst)
	if normalized == "" {
		return fmt.Errorf("invalid destination %q", dst)
	}

	// IPs and CIDRs have their own format.
	if isIPOrCIDR(normalized) {
		// Wildcard prefix only makes sense for domains, not IPs/CIDRs.
		// Without this check, ".192.168.1.1" passes validation but
		// downstream generators (CoreDNS, Envoy, certs) see the raw Dst
		// with the leading dot, which fails isIPOrCIDR and causes the IP
		// to be misclassified as a domain.
		if strings.HasPrefix(dst, ".") {
			return fmt.Errorf("invalid destination %q: wildcard prefix not allowed on IP/CIDR", dst)
		}
		return nil
	}

	// Max 253 chars after stripping wildcard/FQDN affixes.
	if len(normalized) > 253 {
		return fmt.Errorf("destination %q exceeds 253 characters", dst)
	}

	last := byte('.')
	nonNumeric := false
	partlen := 0
	for i := range len(normalized) {
		c := normalized[i]
		switch {
		case c >= 'a' && c <= 'z' || c == '_':
			nonNumeric = true
			partlen++
		case c >= '0' && c <= '9':
			partlen++
		case c == '-':
			if last == '.' {
				return fmt.Errorf("invalid destination %q: label starts with hyphen", dst)
			}
			nonNumeric = true
			partlen++
		case c == '.':
			if last == '.' || last == '-' {
				return fmt.Errorf("invalid destination %q: empty label or label ends with hyphen", dst)
			}
			if partlen > 63 {
				return fmt.Errorf("invalid destination %q: label exceeds 63 characters", dst)
			}
			partlen = 0
		case c >= 'A' && c <= 'Z':
			return fmt.Errorf("invalid destination %q: uppercase letters not allowed (use lowercase)", dst)
		default:
			return fmt.Errorf("invalid destination %q: invalid character %q", dst, string(rune(c)))
		}
		last = c
	}
	if last == '.' {
		return fmt.Errorf("invalid destination %q: trailing empty label", dst)
	}
	if last == '-' {
		return fmt.Errorf("invalid destination %q: last label ends with hyphen", dst)
	}
	if partlen > 63 {
		return fmt.Errorf("invalid destination %q: last label exceeds 63 characters", dst)
	}
	if !nonNumeric {
		return fmt.Errorf("invalid destination %q: domain must not be purely numeric", dst)
	}
	return nil
}

// NormalizeRule fills in missing fields before storage so rules are explicit and
// unambiguous. Empty proto defaults to "tls", empty action to "allow", and TLS
// rules with no port default to 443. Existing non-zero values are never overridden.
// Users should set full rules — this is a storage safety net, not a feature.
func NormalizeRule(r config.EgressRule) config.EgressRule {
	if r.Proto == "" {
		r.Proto = "tls"
	}
	if r.Action == "" {
		r.Action = "allow"
	}
	if r.Port == 0 && strings.ToLower(r.Proto) == "tls" {
		r.Port = 443
	}
	return r
}

// RuleKey returns the dedup key for an egress rule: dst:proto:port.
// The Dst is used verbatim so that ".claude.ai" and "claude.ai" are distinct
// rules — a wildcard and its apex carry independent semantics (e.g., different
// PathRules) and must not be collapsed.
func RuleKey(r config.EgressRule) string {
	return fmt.Sprintf("%s:%s:%d", r.Dst, r.Proto, r.Port)
}

// RoutesFromRules projects a rule set into the BPF route_map entry form.
// Only "allow" rules produce routes — deny rules and rules with a non-
// port (ssh path ports, etc.) fall through the fast path to Envoy
// anyway. Destinations are normalized before hashing so the resulting
// DomainHash matches whatever CoreDNS writes into dns_cache at resolve
// time (INV: normalizeDomain + ebpf.DomainHash form the shared hashing
// contract across firewall / dnsbpf / ebpf).
//
// envoyPort is the single Envoy egress listener port — the route_map's
// only purpose is to route matched flows there, so every entry carries
// the same value. Callers pass cfg.EnvoyEgressPort().
func RoutesFromRules(rules []config.EgressRule, envoyPort uint16) []ebpf.Route {
	out := make([]ebpf.Route, 0, len(rules))
	for _, r := range rules {
		if strings.ToLower(r.Action) != "allow" {
			continue
		}
		dst := normalizeDomain(r.Dst)
		if dst == "" || isIPOrCIDR(dst) {
			continue
		}
		port := r.Port
		if port <= 0 || port > 0xffff {
			continue
		}
		out = append(out, ebpf.Route{
			DomainHash: ebpf.DomainHash(dst),
			DstPort:    uint16(port),
			EnvoyPort:  envoyPort,
		})
	}
	return out
}

// NormalizeAndDedup normalizes all rules and removes duplicates.
// This handles legacy store files that contain port:0 rules written before
// NormalizeRule defaulted TLS to 443 — after normalization those become
// duplicates of the correctly-ported entries.
//
// Wildcard (.claude.ai) and exact (claude.ai) rules are NOT deduped against
// each other — they are semantically distinct. A user may want unrestricted
// subdomain access while restricting paths on the apex, or vice versa.
func NormalizeAndDedup(rules []config.EgressRule) ([]config.EgressRule, []string) {
	var warnings []string
	seen := make(map[string]struct{}, len(rules))
	out := make([]config.EgressRule, 0, len(rules))
	for _, r := range rules {
		r = NormalizeRule(r)
		// Skip rules that normalize to an empty domain (e.g., "." or "..").
		if normalizeDomain(r.Dst) == "" {
			warnings = append(warnings, fmt.Sprintf("skipping rule with empty domain after normalization (dst=%q)", r.Dst))
			continue
		}
		key := RuleKey(r)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	return out, warnings
}
