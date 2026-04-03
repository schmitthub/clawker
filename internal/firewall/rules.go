package firewall

import (
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/storage"
)

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
	if last == '-' {
		return fmt.Errorf("invalid destination %q: last label ends with hyphen", dst)
	}
	if partlen > 63 {
		return fmt.Errorf("invalid destination %q: last label exceeds 63 characters", dst)
	}
	if !nonNumeric {
		return fmt.Errorf("invalid destination %q: domain must contain at least one letter", dst)
	}
	return nil
}

// normalizeRule fills in missing fields before storage so rules are explicit and
// unambiguous. Empty proto defaults to "tls", empty action to "allow", and TLS
// rules with no port default to 443. Existing non-zero values are never overridden.
// Users should set full rules — this is a storage safety net, not a feature.
func normalizeRule(r config.EgressRule) config.EgressRule {
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

// ruleKey returns the dedup key for an egress rule: dst:proto:port.
// The Dst is used verbatim so that ".claude.ai" and "claude.ai" are distinct
// rules — a wildcard and its apex carry independent semantics (e.g., different
// PathRules) and must not be collapsed.
func ruleKey(r config.EgressRule) string {
	return fmt.Sprintf("%s:%s:%d", r.Dst, r.Proto, r.Port)
}

// normalizeAndDedup normalizes all rules and removes duplicates.
// This handles legacy store files that contain port:0 rules written before
// normalizeRule defaulted TLS to 443 — after normalization those become
// duplicates of the correctly-ported entries.
//
// Wildcard (.claude.ai) and exact (claude.ai) rules are NOT deduped against
// each other — they are semantically distinct. A user may want unrestricted
// subdomain access while restricting paths on the apex, or vice versa.
func normalizeAndDedup(rules []config.EgressRule) ([]config.EgressRule, []string) {
	var warnings []string
	seen := make(map[string]struct{}, len(rules))
	out := make([]config.EgressRule, 0, len(rules))
	for _, r := range rules {
		r = normalizeRule(r)
		// Skip rules that normalize to an empty domain (e.g., "." or "..").
		if normalizeDomain(r.Dst) == "" {
			warnings = append(warnings, fmt.Sprintf("skipping rule with empty domain after normalization (dst=%q)", r.Dst))
			continue
		}
		key := ruleKey(r)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	return out, warnings
}
