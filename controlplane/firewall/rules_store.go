package firewall

import (
	"fmt"
	"net"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	ebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/config"
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

// ValidateRule fully validates a single egress rule sourced from clawker.yaml
// (the launch path, via BootstrapServicesPreStart → FirewallAddRules) or a CLI
// `firewall add`. It checks destination syntax, the dynamic port spec, and every
// allow/deny field. A failure aborts rule ingestion — and therefore the
// container launch — so the CLI surfaces the bad rule to the user, instead of
// the rule being accepted as ADDED and then silently dropped at the later
// NormalizeAndDedup reconcile step.
//
// Proto is intentionally NOT rejected: an unrecognized proto token is a
// deliberate soft-skip caught by the Envoy deny floor (a safety net, not a
// config error), so opaque L7 names for TCP pass-through stay valid.
func ValidateRule(r config.EgressRule) error {
	if err := ValidateDst(r.Dst); err != nil {
		return err
	}
	if err := r.ValidatePortSpec(); err != nil {
		return fmt.Errorf("invalid port %q: %w", r.Port, err)
	}
	if err := validateActionField("action", r.Action); err != nil {
		return err
	}
	if err := validateActionField("path_default", r.PathDefault); err != nil {
		return err
	}
	for _, pr := range r.PathRules {
		if strings.TrimSpace(pr.Path) == "" {
			return fmt.Errorf("path rule with empty path")
		}
		if err := validateActionField("path rule action", pr.Action); err != nil {
			return err
		}
		for _, m := range pr.Methods {
			if err := validateMethod(m); err != nil {
				return err
			}
		}
	}
	return nil
}

// methodTokenRe matches a safe HTTP method token. Methods are embedded into a
// safe_regex alternation in the generated route match, so they must be confined
// to a metacharacter-free charset — a token starting with a letter, followed by
// letters or hyphens (covers GET/POST/PATCH and extensions like MKCALENDAR /
// PROPFIND-style WebDAV verbs) — to prevent regex injection.
var methodTokenRe = regexp.MustCompile(`^[A-Za-z][A-Za-z-]*$`)

// validateMethod rejects an HTTP method token that is empty or carries
// characters outside the safe token charset. Case-insensitive (NormalizeRule
// uppercases); the check is on shape, not a fixed verb allow-list, so custom
// methods stay expressible.
func validateMethod(m string) error {
	if !methodTokenRe.MatchString(strings.TrimSpace(m)) {
		return fmt.Errorf("invalid HTTP method %q: must match %s", m, methodTokenRe.String())
	}
	return nil
}

// validateActionField checks an allow/deny field. Empty is accepted (the
// protocol/path default applies); any other value is rejected. A mistyped action
// like "deny"→"dney" would otherwise silently coerce to allow — an inverted-
// policy footgun that turns an intended block into an open egress path.
func validateActionField(name, val string) error {
	switch strings.ToLower(val) {
	case "", "allow", "deny":
		return nil
	default:
		return fmt.Errorf("invalid %s %q: must be \"allow\" or \"deny\"", name, val)
	}
}

// NormalizeRule fills in missing fields before storage so rules are explicit and
// unambiguous. `proto: tls` is silently translated to `proto: https` (legacy
// alias; "tls" was always TLS-terminated HCM-inspected HTTPS — the rename
// disambiguates from raw TLS proxying). Empty proto defaults to "https" (the
// common case). Empty action defaults to "allow". Default port is 443 for
// https/wss, 80 for http/ws, 22 for ssh. Existing non-zero values are never
// overridden.
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
	if r.Port == "" {
		switch strings.ToLower(r.Proto) {
		case "https", "wss":
			r.Port = "443"
		case "http", "ws":
			r.Port = "80"
		case "ssh":
			r.Port = "22"
		}
	}
	// Normalize each path rule's method set (uppercase, dedup, sort) so the
	// generated :method matcher is deterministic. Copy the slice + elements so a
	// shared backing array from the caller is never mutated in place.
	if len(r.PathRules) > 0 {
		prs := make([]config.PathRule, len(r.PathRules))
		copy(prs, r.PathRules)
		for i := range prs {
			prs[i].Methods = normalizeMethods(prs[i].Methods)
		}
		r.PathRules = prs
	}
	return r
}

// normalizeMethods canonicalizes a path rule's HTTP method set: uppercase, trim,
// drop empties, dedup, and sort. Returns nil for an empty/all-empty input so the
// "all methods" case stays a nil slice (no :method matcher emitted).
func normalizeMethods(methods []string) []string {
	if len(methods) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(methods))
	out := make([]string, 0, len(methods))
	for _, m := range methods {
		m = strings.ToUpper(strings.TrimSpace(m))
		if m == "" {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// RuleKey returns the dedup key for an egress rule: dst:proto:port.
// The Dst is used verbatim so that ".claude.ai" and "claude.ai" are distinct
// rules — a wildcard and its apex carry independent semantics (e.g., different
// PathRules) and must not be collapsed.
func RuleKey(r config.EgressRule) string {
	// Port is the dynamic spec string ("443" or "9000-9100"), so it folds into
	// the key verbatim: two opaque rules for one dst:proto that differ only by
	// their port range stay distinct (a bare range and a single port produce
	// different keys), and NormalizeAndDedup won't collapse them.
	return fmt.Sprintf("%s:%s:%s", r.Dst, r.Proto, r.Port)
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
// TLS/HTTP rules (http/https/ws/wss) emit L4ProtoTCP routes to the
// main egress listener (ports.EgressPort). https/wss FQDN rules also
// emit L4ProtoUDP routes to EgressPort for the h3-over-https QUIC sibling
// (quicSNIChainLayer). SSH/TCP rules route to their dedicated per-rule TCP
// listener port (ports.TCPPortBase + index). Raw UDP rules route to their
// dedicated per-rule UDP listener port (ports.UDPPortBase + index),
// emitting L4ProtoUDP routes. The TCP/SSH and raw-UDP branches drive
// routes directly from TCPMappings and UDPMappings respectively so eBPF
// routes and Envoy listeners stay in lockstep: matching allow semantics
// (empty Action == allow), matching IP/CIDR filtering, and matching
// tcpDefaultPort / udpDefaultPort defaulting for rules with Port==0.
// Any divergence here silently misroutes traffic (e.g. SSH landing on
// the main TLS listener — tls_inspector sees raw TCP, no SNI match,
// deny chain resets).
func RoutesFromRules(rules []config.EgressRule, ports EnvoyPorts) []ebpf.Route {
	out := make([]ebpf.Route, 0, len(rules))

	// mappingRoute projects a dedicated-listener mapping (TCP/SSH or raw UDP) into
	// an eBPF route. A single-IP dst (TCPMappings/UDPMappings skip only CIDR) was
	// never resolved by CoreDNS, so it has no dns_cache entry — carry SeedIP and
	// SyncRoutes seeds dns_cache[ip]=DomainHash(ip), letting connect4/sendmsg4 hit on
	// the literal IP and redirect to the dedicated STATIC-pinned listener. An FQDN
	// has no SeedIP (its dns_cache entry comes from CoreDNS).
	mappingRoute := func(m TCPMapping, proto uint8) ebpf.Route {
		r := ebpf.Route{
			DomainHash: ebpf.DomainHash(m.Dst),
			DstPort:    uint16(m.DstPort),
			EnvoyPort:  uint16(m.EnvoyPort),
			L4Proto:    proto,
		}
		if v4 := net.ParseIP(m.Dst).To4(); v4 != nil {
			r.SeedIP = ebpf.IPToUint32(v4)
		}
		return r
	}

	// TCP/SSH: TCPMappings is the source of truth for which rules produce a
	// dedicated listener, the effective destination port, and the Envoy listener
	// port. It expands a port_range into one mapping per in-range port, so this pass
	// mirrors that fan-out one-to-one. m.Dst is an FQDN or a single IP literal (CIDR
	// is skipped — it rides the shared egress listener); a bare IP carries SeedIP.
	for _, m := range TCPMappings(rules, ports) {
		if m.Dst == "" {
			continue
		}
		out = append(out, mappingRoute(m, ebpf.L4ProtoTCP))
	}

	// Raw UDP: UDPMappings is the source of truth for which udp rules get a
	// dedicated udp_proxy listener and its port — the L4ProtoUDP peer of the TCP
	// pass above. The l4_proto discriminator keeps a co-keyed tcp/https route on the
	// same {domain, port} independent. Like the TCP pass, m.Dst is an FQDN or a
	// single IP literal (CIDR skipped, fails closed); a bare IP carries SeedIP.
	//
	// udpSeen dedups L4ProtoUDP routes across BOTH this pass and the h3-over-https
	// pass below — the raw-udp pass runs first, so an explicit `proto: udp` rule
	// stays authoritative on a {domain, port} collision (e.g. udp:443 + https).
	udpSeen := make(map[string]struct{})
	for _, m := range UDPMappings(rules, ports) {
		if m.Dst == "" {
			continue
		}
		udpSeen[fmt.Sprintf("%s:%d", m.Dst, m.DstPort)] = struct{}{}
		out = append(out, mappingRoute(m, ebpf.L4ProtoUDP))
	}

	// TLS/HTTP (http/https/ws/wss): second pass — all ride the shared egress
	// listener, so the route just maps (domain, dst port) → EgressPort. ws/wss
	// are http/https with a websocket upgrade enrichment in Envoy; at the eBPF
	// layer they are indistinguishable from their base proto (same host:port →
	// same egress redirect), so a ws+http or wss+https pair for one origin must
	// collapse to a SINGLE route. Dedup by (domain, dst port) — emitting both
	// would write the same route_map key twice (harmless but noisy) and obscures
	// the one-stack-per-origin invariant the Envoy generator enforces.
	seen := make(map[string]struct{})
	for _, r := range rules {
		action := strings.ToLower(r.Action)
		if action != "allow" && action != "" {
			continue
		}
		proto := strings.ToLower(r.Proto)
		if proto == "ssh" || proto == "tcp" {
			continue // handled above (TCPMappings → dedicated TCP listener)
		}
		if proto == "udp" {
			continue // handled above (UDPMappings → dedicated udp_proxy listener, L4ProtoUDP)
		}
		if isIPOrCIDR(r.Dst) {
			continue
		}
		dst := normalizeDomain(r.Dst)
		if dst == "" {
			continue
		}
		// TLS rules reach here post-NormalizeRule with a single Port (the
		// pre-Submit store write fills the proto default, e.g. 443). A range or
		// missing single port is a misconfigured TLS rule and we drop it rather
		// than guess (port_range is opaque-only; invalid specs are already
		// dropped by NormalizeAndDedup).
		port, ok := r.SinglePort()
		if !ok {
			continue
		}
		key := fmt.Sprintf("%s:%d", dst, port)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ebpf.Route{
			DomainHash: ebpf.DomainHash(dst),
			DstPort:    uint16(port),
			EnvoyPort:  uint16(ports.EgressPort),
			L4Proto:    ebpf.L4ProtoTCP,
		})

		// h3-over-https: a TLS-bearing FQDN rule (https/wss) also gets a QUIC/h3
		// listener in Envoy (quicSNIChainLayer, on UDP EgressPort) that the TCP
		// chain advertises via alt-svc. Project an L4ProtoUDP route to the SAME
		// EgressPort so the eBPF layer redirects the agent's QUIC datagrams to that
		// listener instead of denying them — this is what flips a QUIC attempt to an
		// allowed-domain from "denied" to "allowed". Plaintext http/ws have no
		// cleartext h3 sibling, so they get no UDP route. Skipped if a raw `udp`
		// rule already claimed this {domain, port} (raw-udp pass is authoritative).
		// Both connected (connect4) and unconnected (sendmsg4) QUIC datagrams
		// follow this route; recvmsg4/getpeername4 restore the reply source from
		// udp_flow_map so the app observes responses as if from the original dst.
		if proto == "https" || proto == "wss" {
			if _, dup := udpSeen[key]; !dup {
				udpSeen[key] = struct{}{}
				out = append(out, ebpf.Route{
					DomainHash: ebpf.DomainHash(dst),
					DstPort:    uint16(port),
					EnvoyPort:  uint16(ports.EgressPort),
					L4Proto:    ebpf.L4ProtoUDP,
				})
			}
		}
	}
	return out
}

// SeedDomainsFromRules returns the destination strings that RoutesFromRules
// seeds into dns_cache as IP literals — one entry per distinct bare-IPv4
// dedicated-listener mapping (TCP/SSH/UDP). These are exactly the strings whose
// ebpf.DomainHash equals the SeedIP routes' DomainHash, so feeding them through
// the netlogger ReverseDNSMap lets it attribute those seeded entries (dst_host
// becomes the IP literal) instead of logging them as unattributed.
//
// CoreDNS never resolves a literal IP, so these dsts are absent from
// AllResolvableDomains (the Corefile zone set); the netlogger DomainSource is
// the union of the two (see Handler.ReverseDNSDomains). This mirrors
// mappingRoute's SeedIP condition in RoutesFromRules exactly — both walk
// TCPMappings + UDPMappings and key off net.ParseIP(dst).To4(); the TLS pass
// never seeds because it skips IP/CIDR destinations.
func SeedDomainsFromRules(rules []config.EgressRule, ports EnvoyPorts) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(dst string) {
		if net.ParseIP(dst).To4() == nil {
			return // FQDN (CoreDNS-resolved) or non-IPv4 — no SeedIP, no dns_cache seed
		}
		if _, dup := seen[dst]; dup {
			return // one IP rule may fan out to many ports → one dns_cache key
		}
		seen[dst] = struct{}{}
		out = append(out, dst)
	}
	for _, m := range TCPMappings(rules, ports) {
		add(m.Dst)
	}
	for _, m := range UDPMappings(rules, ports) {
		add(m.Dst)
	}
	return out
}

// NormalizeAndDedup normalizes all rules and removes duplicates.
// This handles store files that contain rules with an unset port: NormalizeRule
// fills the proto default (e.g. https → 443) so they become duplicates of the
// correctly-ported entries and collapse during dedup.
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
		// Validate the dynamic port spec at ingestion. A malformed port/range is
		// dropped with an operator warning rather than silently collapsing to a
		// protocol default — that would widen egress past what the rule intended.
		if err := r.ValidatePortSpec(); err != nil {
			warnings = append(warnings, fmt.Sprintf("skipping rule %s:%s: %v", r.Dst, r.Proto, err))
			continue
		}
		// Surface path/method rules set on a non-HTTP proto: they are ignored at
		// generation (no L7 request line), so warn rather than silently no-op.
		if w := pathRuleEnforcementWarning(r); w != "" {
			warnings = append(warnings, w)
		}
		// Dedup only TRULY-identical entries: fold the action into the dedup key so
		// a same dst:proto:port allow AND deny both survive. RuleKey alone (no action)
		// keeps just the first and silently drops the other — the deny would vanish.
		// Both surviving lets resolveOpaquePortConflicts carve (when a range is
		// present) or the generator reject loud (an all-single allow/deny clash).
		action := "allow"
		if isDenyAction(r.Action) {
			action = "deny"
		}
		key := RuleKey(r) + "\x00" + action
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	// Resolve opaque port conflicts AFTER exact-key dedup: a range and a single
	// port for the same dst:proto have distinct RuleKeys, so the dedup above keeps
	// both — and each opaque rule fans into one dedicated STATIC listener PER
	// in-range port (named tcp_<dst>_<port> / udp_<dst>_<port>), so an overlapping
	// port would emit the SAME listener twice and fail generation. This merges
	// same-action overlaps into one span set AND carves denied ports out of allow
	// spans (DENY ALWAYS WINS), emitting an explicit deny rule for each denied span.
	// A cross-proto overlap on the same listener family (e.g. tcp + ssh on one
	// dst:port) is a genuine conflict left to the generator's span-aware collision
	// check, which fails closed.
	out, resolveWarnings := resolveOpaquePortConflicts(out)
	warnings = append(warnings, resolveWarnings...)
	return out, warnings
}

// resolveOpaquePortConflicts is the merge brain for opaque (tcp/ssh/udp) port
// rules. A range is just shorthand for a set of per-port chains, so this folds
// every opaque rule into two port-span sets per (dst, proto) — allow and deny —
// and resolves overlaps with ONE invariant: DENY ALWAYS WINS.
//
//   - Same-action overlapping spans merge (a range + a single port inside it,
//     or two partial ranges, collapse into one span set). Without this an opaque
//     rule fans into one dedicated STATIC listener PER in-range port (named
//     tcp_<dst>_<port> / udp_<dst>_<port>), so an overlapping port would emit the
//     SAME listener twice and fail generation.
//   - Any port that appears in a deny span is CARVED OUT of the allow spans
//     (subtractSpans) and emitted as an explicit deny rule. Example: `tcp 45-50
//     allow` with `tcp 47 deny` yields allow {45-46, 48-50} plus deny {47};
//     `tcp 45-50 deny` with `tcp 47 allow` yields deny {45-50} and the single
//     allow is swallowed. Deny spans are emitted verbatim and ALWAYS persist —
//     a denied port is never silently allowed by an overlapping allow range.
//
// The output is the flat, conflict-free span set the generator loops over: allow
// spans build pinned upstream listeners, deny spans build blackhole (deny-cluster)
// listeners. Cross-proto overlaps on one listener family (e.g. tcp + ssh on one
// dst:port) are a genuine conflict resolved separately by the generator's
// span-aware collision check — they are NOT merged here. Non-opaque rules and
// rules with an unset port pass through verbatim, in input order.
func resolveOpaquePortConflicts(rules []config.EgressRule) ([]config.EgressRule, []string) {
	type group struct {
		allowTmpl  *config.EgressRule
		denyTmpl   *config.EgressRule
		allowSpans [][2]int
		denySpans  [][2]int
	}
	groups := make(map[string]*group)
	// order records emission slots: each is either a verbatim rule (rule != nil)
	// or a group key (the first time that (dst,proto) group is seen), preserving
	// input order.
	type slot struct {
		rule *config.EgressRule
		key  string
	}
	var order []slot

	for i := range rules {
		r := rules[i]
		lo, hi, ok := r.PortSpan()
		if !ok || !isOpaqueProto(r.Proto) {
			rr := r
			order = append(order, slot{rule: &rr})
			continue
		}
		key := fmt.Sprintf("%s\x00%s", normalizeDomain(r.Dst), strings.ToLower(r.Proto))
		g, exists := groups[key]
		if !exists {
			g = &group{}
			groups[key] = g
			order = append(order, slot{key: key})
		}
		if isDenyAction(r.Action) {
			if g.denyTmpl == nil {
				rr := r
				g.denyTmpl = &rr
			}
			g.denySpans = append(g.denySpans, [2]int{lo, hi})
		} else {
			if g.allowTmpl == nil {
				rr := r
				g.allowTmpl = &rr
			}
			g.allowSpans = append(g.allowSpans, [2]int{lo, hi})
		}
	}

	var warnings []string
	out := make([]config.EgressRule, 0, len(rules))
	for _, s := range order {
		if s.rule != nil {
			out = append(out, *s.rule)
			continue
		}
		g := groups[s.key]
		mergedDeny := mergeOverlappingSpans(g.denySpans)
		mergedAllow := mergeOverlappingSpans(g.allowSpans)
		// A carve REQUIRES a range: the deny-wins carve-out exists so an allow
		// range can express "this span EXCEPT these ports". A single allow port and
		// a single deny port for the SAME port is not a carve — it is a contradictory
		// config with no range to resolve, so it must FAIL LOUD, not silently collapse
		// to deny. Exclude those all-single clashes from the carve set and leave BOTH
		// rules in the output; the generator's checkOpaquePortActionConflicts rejects
		// them. A deny span that overlaps an allow RANGE (or a deny range over an allow
		// single) still carves — a range is present.
		allowSingles := make(map[int]bool)
		for _, sp := range mergedAllow {
			if sp[0] == sp[1] {
				allowSingles[sp[0]] = true
			}
		}
		carveDeny := make([][2]int, 0, len(mergedDeny))
		for _, sp := range mergedDeny {
			if sp[0] == sp[1] && allowSingles[sp[0]] {
				continue // all-single allow/deny clash: leave both → loud rejection
			}
			carveDeny = append(carveDeny, sp)
		}
		allowOut := subtractSpans(mergedAllow, carveDeny)

		dst, proto := "", ""
		if g.denyTmpl != nil {
			dst, proto = g.denyTmpl.Dst, strings.ToLower(g.denyTmpl.Proto)
		} else if g.allowTmpl != nil {
			dst, proto = g.allowTmpl.Dst, strings.ToLower(g.allowTmpl.Proto)
		}
		if mergedSpansChanged(g.allowSpans, mergedAllow) || mergedSpansChanged(g.denySpans, mergedDeny) {
			warnings = append(warnings, fmt.Sprintf("coalesced overlapping %s rules for %q", proto, dst))
		}
		if len(mergedDeny) > 0 && !spansEqual(mergedAllow, allowOut) {
			warnings = append(warnings, fmt.Sprintf(
				"deny wins for %s %q: denied ports %s carved out of allow (allow now %s)",
				proto, dst, formatSpans(mergedDeny), formatSpans(allowOut)))
		}

		// Deny spans first (deny always persists), then carved allow spans —
		// both ascending, deterministic for the eBPF/Envoy port assignment.
		for _, sp := range mergedDeny {
			m := *g.denyTmpl
			m.Port = formatSpan(sp)
			out = append(out, m)
		}
		for _, sp := range allowOut {
			m := *g.allowTmpl
			m.Port = formatSpan(sp)
			out = append(out, m)
		}
	}
	return out, warnings
}

// isDenyAction reports whether an egress rule action denies. Empty action
// defaults to allow (see NormalizeRule), so only an explicit "deny" denies.
func isDenyAction(action string) bool {
	return strings.ToLower(action) == "deny"
}

// subtractSpans removes every port covered by a deny span from the allow spans,
// returning the remaining allow sub-spans. A denied port is never left in the
// result — this is the deny-wins carve-out, an egress-security boundary. The deny
// spans are sorted internally before the running-cursor advance rather than
// trusting callers to pass sorted input: an unsorted deny would silently skip
// earlier spans and leave denied ports allowed (a fail-open shape). The allow
// spans are consumed in input order and the result preserves that order, so
// callers should pass merged (sorted, disjoint) allow spans for a clean result.
func subtractSpans(allow, deny [][2]int) [][2]int {
	if len(deny) == 0 {
		return allow
	}
	sortedDeny := make([][2]int, len(deny))
	copy(sortedDeny, deny)
	sort.Slice(sortedDeny, func(i, j int) bool {
		if sortedDeny[i][0] != sortedDeny[j][0] {
			return sortedDeny[i][0] < sortedDeny[j][0]
		}
		return sortedDeny[i][1] < sortedDeny[j][1]
	})
	var out [][2]int
	for _, a := range allow {
		lo := a[0]
		for _, d := range sortedDeny {
			if d[1] < lo || d[0] > a[1] {
				continue // no overlap with the remaining [lo, a[1]]
			}
			if d[0] > lo {
				out = append(out, [2]int{lo, d[0] - 1})
			}
			if d[1]+1 > lo {
				lo = d[1] + 1
			}
			if lo > a[1] {
				break
			}
		}
		if lo <= a[1] {
			out = append(out, [2]int{lo, a[1]})
		}
	}
	return out
}

// mergedSpansChanged reports whether merging collapsed the input (overlap was
// present), used only to decide whether to emit a coalesce warning.
func mergedSpansChanged(in, merged [][2]int) bool {
	return len(merged) < len(in)
}

// spansEqual reports whether two span slices are identical (same order, same
// bounds). Both are merged output, so order is canonical (ascending).
func spansEqual(a, b [][2]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// rulesCanonicalEqual reports whether two NormalizeAndDedup outputs describe the
// same rule set, independent of slice order. resolveOpaquePortConflicts can carve
// an opaque allow range into multiple span rules and the resulting order is not
// stable across an append+re-merge, so a plain reflect.DeepEqual on the slices
// would report a spurious difference. Within a canonical set each
// (RuleKey, action) pair is unique (carved spans have distinct ports; allow/deny
// differ in action), so sorting by that key yields a stable total order to
// compare. Used by addRulesToStore to gate the write+reconcile on a real change.
func rulesCanonicalEqual(a, b []config.EgressRule) bool {
	if len(a) != len(b) {
		return false
	}
	sortKey := func(r config.EgressRule) string {
		action := "allow"
		if isDenyAction(r.Action) {
			action = "deny"
		}
		return RuleKey(r) + "\x00" + action
	}
	as := append([]config.EgressRule(nil), a...)
	bs := append([]config.EgressRule(nil), b...)
	sort.Slice(as, func(i, j int) bool { return sortKey(as[i]) < sortKey(as[j]) })
	sort.Slice(bs, func(i, j int) bool { return sortKey(bs[i]) < sortKey(bs[j]) })
	return reflect.DeepEqual(as, bs)
}

// isOpaqueProto reports whether the proto produces a dedicated per-port listener
// (and thus expands a port range into one listener per port). Only these protos
// are subject to port-overlap coalescing.
func isOpaqueProto(proto string) bool {
	switch strings.ToLower(proto) {
	case "tcp", "ssh", "udp":
		return true
	default:
		return false
	}
}

// pathRuleEnforcementWarning returns a warning when a rule carries path rules or
// method gates on a proto that has no L7 HTTP request line to enforce them
// against (opaque tcp/ssh/udp, or any unsupported token). It mirrors the
// generator's behavior — those rules are silently ignored at generation — by
// surfacing the no-op so an operator isn't lulled into a false sense of
// enforcement. Returns "" when nothing is ignored. Shares the HTTP-family
// definition with the CLI input gate via adminv1.IsHTTPFamilyProto.
func pathRuleEnforcementWarning(r config.EgressRule) string {
	if adminv1.IsHTTPFamilyProto(r.Proto) || len(r.PathRules) == 0 {
		return ""
	}
	hasMethods := false
	for _, pr := range r.PathRules {
		if len(pr.Methods) > 0 {
			hasMethods = true
			break
		}
	}
	what := "path rules"
	if hasMethods {
		what = "path/method rules"
	}
	return fmt.Sprintf("ignoring %s on %s:%s — not an HTTP-family proto (http/https/ws/wss); no L7 request line to inspect", what, r.Dst, r.Proto)
}

// mergeOverlappingSpans sorts [lo,hi] spans ascending and merges any that
// overlap (share ≥1 port, i.e. next.lo <= cur.hi). Adjacent-but-disjoint spans
// are left separate.
func mergeOverlappingSpans(spans [][2]int) [][2]int {
	if len(spans) <= 1 {
		return spans
	}
	sorted := make([][2]int, len(spans))
	copy(sorted, spans)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i][0] != sorted[j][0] {
			return sorted[i][0] < sorted[j][0]
		}
		return sorted[i][1] < sorted[j][1]
	})
	merged := [][2]int{sorted[0]}
	for _, s := range sorted[1:] {
		last := &merged[len(merged)-1]
		if s[0] <= last[1] { // overlap (shared port)
			if s[1] > last[1] {
				last[1] = s[1]
			}
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

// formatSpan renders a [lo,hi] span as the dynamic Port field: "443" for a
// single port, "lo-hi" for a range.
func formatSpan(sp [2]int) string {
	if sp[0] == sp[1] {
		return strconv.Itoa(sp[0])
	}
	return strconv.Itoa(sp[0]) + "-" + strconv.Itoa(sp[1])
}

// formatSpans renders a list of spans for an operator warning.
func formatSpans(spans [][2]int) string {
	parts := make([]string, len(spans))
	for i, sp := range spans {
		parts[i] = formatSpan(sp)
	}
	return strings.Join(parts, ",")
}
