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
// unambiguous. `proto: tls` is silently translated to `proto: https` (legacy
// alias; "tls" was always TLS-terminated HCM-inspected HTTPS — the rename
// disambiguates from raw TLS proxying). Empty proto defaults to "https" (the
// common case). Empty action defaults to "allow". Default port is 443 for
// https, 80 for http, 22 for ssh. Existing non-zero values are never overridden.
// Config-side values stay present-tense (allow/deny) — the access log emits
// past-tense verdict values (allowed/denied) independently.
func NormalizeRule(r config.EgressRule) config.EgressRule {
	if strings.ToLower(r.Proto) == "tls" {
		r.Proto = "https"
	}
	if r.Proto == "" {
		r.Proto = "https"
	}
	if r.Action == "" {
		r.Action = "allow"
	}
	if r.Port == 0 {
		switch strings.ToLower(r.Proto) {
		case "https":
			r.Port = 443
		case "http":
			r.Port = 80
		case "ssh":
			r.Port = 22
		}
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

// EffectivePathDefault resolves the catch-all action for HTTP paths under a
// rule that don't match any explicit PathRule entry. Explicit PathDefault
// always wins; otherwise the action is inferred from the path_rules
// composition so a user who runs `firewall add foo.com --path /x --action
// deny` gets denylist semantics (allow all paths except /x) without
// having to know about the path_default knob:
//
//   - r.PathDefault non-empty               → r.PathDefault   (explicit override)
//   - any PathRule with Action="allow"      → "deny"          (allowlist mode)
//   - all PathRules have Action="deny"      → "allow"         (denylist mode)
//   - no PathRules                          → "allow"         (vacuous; callers
//     don't query this)
func EffectivePathDefault(r config.EgressRule) string {
	if r.PathDefault != "" {
		return r.PathDefault
	}
	for _, pr := range r.PathRules {
		if strings.EqualFold(pr.Action, "allow") {
			return "deny"
		}
	}
	return "allow"
}

// MergeRule merges incoming into existing for the same RuleKey. Caller wins
// on Action; PathRules is unioned by Path with caller winning on same-path
// collision. Callers MUST pre-normalize via NormalizeRule so scalar defaults
// are populated before merge.
//
// PathDefault is preserved from existing when incoming.PathDefault is "" so
// a bare CLI add (`clawker firewall add foo.com`) does not silently clear a
// yaml-set default on the same key. A non-empty incoming.PathDefault wins.
// The `string` proto field cannot distinguish "unset" from "explicitly
// empty" — yaml authors who want to clear a default remove the rule and
// re-add without it.
func MergeRule(existing, incoming config.EgressRule) config.EgressRule {
	out := existing
	out.Action = incoming.Action
	if incoming.PathDefault != "" {
		out.PathDefault = incoming.PathDefault
	}
	out.PathRules = mergePathRules(existing.PathRules, incoming.PathRules)
	return out
}

// mergePathRules unions existing and incoming by Path. Existing-side ordering
// is preserved; an incoming PathRule whose Path matches an existing entry
// overwrites that slot in place. Incoming-only PathRules are appended in
// input order.
func mergePathRules(existing, incoming []config.PathRule) []config.PathRule {
	if len(incoming) == 0 {
		return existing
	}
	out := make([]config.PathRule, len(existing))
	copy(out, existing)
	index := make(map[string]int, len(out))
	for i, p := range out {
		index[p.Path] = i
	}
	for _, p := range incoming {
		if i, exists := index[p.Path]; exists {
			out[i] = p
			continue
		}
		index[p.Path] = len(out)
		out = append(out, p)
	}
	return out
}

// RoutesFromRules projects a rule set into the BPF route_map entry form.
// Destinations are normalized before hashing so the resulting DomainHash
// matches whatever CoreDNS writes into dns_cache at resolve time (INV:
// normalizeDomain + ebpf.DomainHash form the shared hashing contract
// across firewall / dnsbpf / ebpf).
//
// TLS/HTTP rules route to the main egress listener (ports.EgressPort).
// SSH/TCP rules route to their dedicated per-rule TCP listener port
// (ports.TCPPortBase + index). The TCP/SSH branch drives routes directly
// from TCPMappings so eBPF routes and Envoy listeners stay in lockstep:
// matching allow semantics (empty Action == allow), matching IP/CIDR
// filtering, and matching tcpDefaultPort defaulting (ssh→22, tcp→443)
// for rules with Port==0. Any divergence here silently misroutes traffic
// (e.g. SSH landing on the main TLS listener — tls_inspector sees raw
// TCP, no SNI match, deny chain resets).
func RoutesFromRules(rules []config.EgressRule, ports EnvoyPorts) []ebpf.Route {
	out := make([]ebpf.Route, 0, len(rules))

	// TCP/SSH: TCPMappings is the source of truth for which rules
	// produce a listener, the effective destination port, and the Envoy
	// listener port. Mirror its output one-to-one.
	for _, m := range TCPMappings(rules, ports) {
		if m.Dst == "" {
			continue
		}
		out = append(out, ebpf.Route{
			DomainHash: ebpf.DomainHash(m.Dst),
			DstPort:    uint16(m.DstPort),
			EnvoyPort:  uint16(m.EnvoyPort),
		})
	}

	// TLS/HTTP: second pass. Apply the same action/IP filtering as
	// TCPMappings so the two paths agree on which rules are "allow".
	for _, r := range rules {
		action := strings.ToLower(r.Action)
		if action != "allow" && action != "" {
			continue
		}
		proto := strings.ToLower(r.Proto)
		if proto == "ssh" || proto == "tcp" {
			continue // handled above
		}
		if isIPOrCIDR(r.Dst) {
			continue
		}
		dst := normalizeDomain(r.Dst)
		if dst == "" {
			continue
		}
		// TLS rules reach here post-NormalizeRule with Port==443 (the
		// pre-Submit store write ensures that); an explicit zero at this
		// point is a misconfigured rule and we drop it rather than guess.
		if r.Port <= 0 || r.Port > 0xffff {
			continue
		}
		out = append(out, ebpf.Route{
			DomainHash: ebpf.DomainHash(dst),
			DstPort:    uint16(r.Port),
			EnvoyPort:  uint16(ports.EgressPort),
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
