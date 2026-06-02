package firewall

import (
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"gopkg.in/yaml.v3"
)

// envoy_types.go holds the data model of the layered Envoy generator:
//   - cross-layer glue shared with the eBPF route layer + Stack (ALSConfig,
//     EnvoyPorts, TCPMapping/TCPMappings);
//   - genCtx — the mutable context threaded through a permutation's layer
//     methods (it carries the rule, the chain being built, and the shared
//     EnvoyConfig);
//   - layer — a single building-block method (genCtx in, error out);
//   - the EnvoyConfig accumulator — the upsert-by-key deliverable.
//
// The orchestrator (envoy_config.go) is protocol-agnostic: the deriver hands it
// the ordered list of layer methods for each permutation and it just chains
// them through one genCtx. All protocol knowledge lives in the layer files +
// the deriver's table.

// ──────────────────────────────────────────────────────────────────────────
// Cross-layer glue (shared with rules_store / handler / stack / eBPF)
// ──────────────────────────────────────────────────────────────────────────

// ALSConfig configures the Envoy access logger's upstream cluster. MTLS=true
// targets the otel-collector's mTLS receiver on Port; MTLS=false omits the OTel
// sink + cluster (stdout-only) — infra services must never cross into the
// untrusted otel-collector:4317 lane reserved for agent containers.
type ALSConfig struct {
	Port int
	MTLS bool
}

// EnvoyPorts holds the port layout for the Envoy proxy.
type EnvoyPorts struct {
	EgressPort  int // Main shared egress listener port.
	TCPPortBase int // Starting port for the per-rule TCP/SSH listeners.
	UDPPortBase int // Starting port for the per-rule raw-UDP (udp_proxy) listeners.
	HealthPort  int // Dedicated health-check listener port.
}

// Validate checks that all ports are in valid range and no two ports collide.
func (p EnvoyPorts) Validate() error {
	named := []struct {
		name string
		port int
	}{
		{"EgressPort", p.EgressPort},
		{"TCPPortBase", p.TCPPortBase},
		{"UDPPortBase", p.UDPPortBase},
		{"HealthPort", p.HealthPort},
	}
	for _, n := range named {
		if n.port <= 0 || n.port > 65535 {
			return fmt.Errorf("envoy ports: %s=%d is out of valid range (1-65535)", n.name, n.port)
		}
	}
	seen := make(map[int]string, len(named))
	for _, n := range named {
		if prev, exists := seen[n.port]; exists {
			return fmt.Errorf("envoy ports: %s and %s both use port %d", prev, n.name, n.port)
		}
		seen[n.port] = n.name
	}
	return nil
}

// TCPMapping describes a per-destination eBPF DNAT entry for non-TLS traffic;
// each TCP/SSH rule gets a dedicated Envoy listener port.
type TCPMapping struct {
	Dst       string
	DstPort   int
	EnvoyPort int
}

// TCPMappings computes TCP/SSH port mappings from egress rules. Deterministic.
// Consumed by the generator (listener layout) and rules_store.RoutesFromRules
// (eBPF DNAT route_map) — the two must stay in lockstep.
func TCPMappings(rules []config.EgressRule, ports EnvoyPorts) []TCPMapping {
	// TCP/SSH skip only CIDR. An FQDN OR a single-IP dst each gets its own
	// dedicated listener (FQDN → LOGICAL_DNS pin, IP → STATIC pin; see layersFor);
	// only a CIDR range — which has no single host to pin — rides the shared egress
	// listener via prefix_ranges + ORIGINAL_DST. This mirrors UDPMappings: the eBPF
	// connect4 NAT rewrites the socket dst before connect, so SO_ORIGINAL_DST can't
	// recover a single IP on the shared listener — a dedicated STATIC listener per IP
	// is the only form that routes correctly. UDP-CIDR has no shared fallback and
	// fails closed; TCP-CIDR rides prefix_ranges (a separate datapath gap tracked
	// elsewhere).
	return dedicatedMappings(rules, ports.TCPPortBase,
		func(p string) bool { return p == "ssh" || p == "tcp" }, tcpDefaultPort, isCIDR)
}

// UDPMappings computes raw-UDP port mappings from egress rules — the udp_proxy
// peer of TCPMappings. Deterministic, indexed from ports.UDPPortBase. Consumed by
// the generator's raw-UDP listener layout AND by RoutesFromRules, which projects
// each mapping into an L4ProtoUDP route_map entry so the eBPF connect4/sendmsg4
// SOCK_DGRAM path redirects the datagram to this dedicated udp_proxy listener
// (mirroring how RoutesFromRules mirrors TCPMappings).
func UDPMappings(rules []config.EgressRule, ports EnvoyPorts) []TCPMapping {
	// UDP skips only CIDR — a UDP-IP rule still needs its own dedicated listener
	// (UDP has no filter chains to ride the shared listener); UDP-CIDR fails closed.
	return dedicatedMappings(rules, ports.UDPPortBase,
		func(p string) bool { return p == "udp" }, udpDefaultPort, isCIDR)
}

// dedicatedMappings is the shared, deterministic per-rule dedicated-listener port
// assignment behind TCPMappings + UDPMappings: allow-only, matching the proto class,
// indexed from base in rule order. portFn resolves each rule's effective dst port.
// Each matching rule (after the port_range fan-out) consumes one base+idx slot → one
// dedicated listener + one pinned cluster.
//
// skipDst gates which dst types are NOT served by a dedicated listener. Both TCP/SSH
// and UDP skip ONLY CIDR: an FQDN or a single-IP dst gets its own dedicated listener
// (FQDN → LOGICAL_DNS pin, IP → STATIC pin), while a CIDR range — which has no single
// host to pin — is handled elsewhere (TCP/SSH → shared egress listener via
// prefixRangeTransportLayer + ORIGINAL_DST; UDP → fails closed, no filter chains to
// range-gate on).
func dedicatedMappings(rules []config.EgressRule, base int, protoMatch func(string) bool, portFn func(config.EgressRule) int, skipDst func(string) bool) []TCPMapping {
	var mappings []TCPMapping
	idx := 0
	for _, r := range rules {
		// Both allow AND deny opaque rules get a dedicated listener slot: an allow
		// listener pins the upstream, a deny listener blackholes (deny cluster).
		// Deny needs a slot so layersFor can build its deny chain and RoutesFromRules
		// can redirect the denied port to it (active deny + logging) — resolveOpaque
		// PortConflicts guarantees allow/deny ports are disjoint per proto, so the two
		// never claim the same (host, port) slot.
		if skipDst(r.Dst) {
			continue
		}
		if !protoMatch(strings.ToLower(r.Proto)) {
			continue
		}
		// A port_range expands into one mapping (→ one dedicated listener + one
		// pinned cluster) per in-range port; each consumes its own base+idx slot.
		// The per-port pin is the self-secure atom — never ORIGINAL_DST (which would
		// forward to whatever dst arrives, delegating host enforcement to the datapath).
		for _, port := range dedicatedPorts(r, portFn) {
			mappings = append(mappings, TCPMapping{
				Dst:       normalizeDomain(r.Dst),
				DstPort:   port,
				EnvoyPort: base + idx,
			})
			idx++
		}
	}
	return mappings
}

// dedicatedPorts returns the destination ports an opaque rule expands to: every
// port in its PortRange (inclusive) if set, otherwise the single port from
// portFn. The order is deterministic (ascending) so listener-port assignment is
// stable across generations and stays in lockstep with the eBPF route_map.
func dedicatedPorts(r config.EgressRule, portFn func(config.EgressRule) int) []int {
	// A single port ("443") yields lo==hi → one port; a range ("9000-9100")
	// fans into one port per value. Empty/invalid Port (ok==false) falls back to
	// the protocol-specific default. Invalid specs are dropped upstream in
	// NormalizeAndDedup, so ok==false here means "unset", not "malformed".
	if lo, hi, ok := r.PortSpan(); ok {
		ports := make([]int, 0, hi-lo+1)
		for p := lo; p <= hi; p++ {
			ports = append(ports, p)
		}
		return ports
	}
	return []int{portFn(r)}
}

// parsePortRange parses an inclusive "lo-hi" port range. Returns ok=false for an
// empty or malformed value (caller falls back to the single Port) — a malformed
// range pins FEWER ports, never more, so the fallback is fail-closed.
// parsePortRange was removed: the dynamic Port field is now parsed and
// validated by config.ParsePortSpec (see internal/config/egress_port.go),
// reached via EgressRule.PortSpan / EgressRule.SinglePort.

// dedicatedPortKey is the lookup key the deriver uses to map a rule to its
// pre-assigned dedicated-listener port (host + effective dst port).
func dedicatedPortKey(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}

// tcpDefaultPort returns the effective destination port for a TCP/SSH rule.
func tcpDefaultPort(r config.EgressRule) int {
	if p, ok := r.SinglePort(); ok {
		return p
	}
	if strings.EqualFold(r.Proto, "ssh") {
		return sshDefaultPort
	}
	return defaultDestPort
}

// ──────────────────────────────────────────────────────────────────────────
// The threaded context + the layer method
// ──────────────────────────────────────────────────────────────────────────

// genCtx is the mutable context threaded through one permutation's layer
// methods. It carries the source rule + generation inputs (read-only to
// layers), the single shared EnvoyConfig being built, and the per-permutation
// chain the layers enrich. A layer method mutates genCtx and returns an error;
// the orchestrator just keeps passing the same genCtx down the method list.
type genCtx struct {
	// inputs (set once per permutation; layers read, never mutate)
	rule  config.EgressRule
	ports EnvoyPorts
	als   ALSConfig

	// shared output (the EnvoyConfig the context "contains")
	cfg *EnvoyConfig

	// the per-permutation filter chain being built — generic Envoy primitives,
	// no protocol-named fields. Filled procedurally in a single forward pass:
	// the transport block sets listener + match + socket (+ tlsTerminated); the
	// upstream block sets upstreamCluster + clusters (+ any http filters it
	// contributes); the app block renders the terminal (HCM/tcp_proxy) LAST,
	// reading the already-populated ctx. Nothing runs after the app block to
	// patch its terminal — there is no second pass.
	listener string
	match    map[string]any   // filter_chain_match
	socket   map[string]any   // transport_socket (downstream; nil = cleartext)
	filters  []any            // network filters (terminal: HCM or tcp_proxy)
	clusters []map[string]any // upstream clusters this permutation needs

	// scheme-derived port facts the transport block sets so the proto-agnostic
	// app block need not know http-vs-https port defaults: port is the effective
	// destination port; bareHostPort is the scheme's default port (80 http / 443
	// https) — the port-less Host form belongs to that port's vhost.
	port         int
	bareHostPort int

	// HCM codec + h3 advertisement the transport block decides so the shared app
	// block stays proto-agnostic: hcmCodec is the HCM codec_type ("" → AUTO; the
	// QUIC transport sets "HTTP3" + http3_protocol_options); advertiseH3 makes
	// the rendered allow vhost emit an alt-svc h3 response header (set by the TCP
	// tls transport so clients discover the sibling QUIC listener).
	hcmCodec    string
	advertiseH3 bool

	// crypto/upstream facts the transport + upstream blocks decide and write
	// BEFORE the app block renders its terminal. The app block only reads these.
	tlsTerminated       bool   // downstream TLS terminated here (access-log tls.established + server.address source)
	upstreamCluster     string // cluster the app block's routes target
	upstreamFollowsHost bool   // upstream resolves from the request Host (DFP) — app keeps the DFP filter live on this vhost
	httpFilters         []any  // HTTP filters contributed by earlier blocks (e.g. sni-lock); the app block appends the router terminal

	// websocket marks this http/https permutation as ws/wss-enriched: the app
	// block adds per-route upgrade_configs:[websocket] + HCM allow_connect (h2) /
	// allow_extended_connect (h3), and the https upstream block pins the cluster
	// to http/1.1. Set by wsEnrichLayer (prepended by the deriver) when the rule's
	// origin is named by a ws/wss rule — NOT a separate chain, an enrichment of
	// the one http/https stack for that origin.
	websocket bool
}

// layer is one building-block method. It mutates ctx (enriches the chain) and
// returns an error; a layer fails closed if its precondition is unmet (e.g. an
// app layer with no transport beneath it). Self-contained: it reads only ctx,
// never the proto token.
type layer func(ctx *genCtx) error

// commit folds the finished chain into the shared EnvoyConfig: clusters first,
// then the assembled filter chain (skipped when no terminal was built — e.g. a
// deny/empty permutation).
func (ctx *genCtx) commit() error {
	for _, cl := range ctx.clusters {
		if err := ctx.cfg.AddCluster(cl); err != nil {
			return err
		}
	}
	if len(ctx.filters) == 0 {
		return nil
	}
	chain := map[string]any{"filters": ctx.filters}
	if ctx.match != nil {
		chain["filter_chain_match"] = ctx.match
	}
	if ctx.socket != nil {
		chain["transport_socket"] = ctx.socket
	}
	return ctx.cfg.addChain(ctx.listener, chain)
}

// ──────────────────────────────────────────────────────────────────────────
// EnvoyConfig — the upsert-by-key deliverable
// ──────────────────────────────────────────────────────────────────────────

// EnvoyConfig is the growing deliverable: a generic Envoy bootstrap tree that
// permutations aggregate into (upsert by key), marshalled to YAML once. Storage
// is generic so a layer may attach any Envoy key/value; the small typed surface
// owns the structural invariants that, left to free-form writes, previously
// shipped a config Envoy rejects — cluster-name uniqueness and
// filter_chain_match uniqueness — failing generation CLOSED on a violation.
type EnvoyConfig struct {
	admin     map[string]any
	listeners map[string]*envoyListener
	clusters  map[string]map[string]any
	seenPerms map[string]struct{}
}

// envoyListener accumulates one listener. chainBySig indexes chains by their
// filter_chain_match signature so a second chain with the same match is merged
// (HCM vhosts unioned) rather than emitted as a duplicate Envoy would reject.
type envoyListener struct {
	base        map[string]any
	chains      []map[string]any
	chainBySig  map[string]int
	defaultDeny map[string]any
}

// NewEnvoyConfig returns an empty accumulator.
func NewEnvoyConfig() *EnvoyConfig {
	return &EnvoyConfig{
		listeners: map[string]*envoyListener{},
		clusters:  map[string]map[string]any{},
		seenPerms: map[string]struct{}{},
	}
}

// SetAdmin sets the top-level admin block (last write wins).
func (c *EnvoyConfig) SetAdmin(admin map[string]any) { c.admin = admin }

// HasListener reports whether the named listener has been created.
func (c *EnvoyConfig) HasListener(name string) bool {
	_, ok := c.listeners[name]
	return ok
}

// EnsureListener returns (creating on first call, bound to address:port) the
// named listener. Idempotent — the first call's address wins.
func (c *EnvoyConfig) EnsureListener(name, address string, port int) {
	if _, ok := c.listeners[name]; ok {
		return
	}
	c.listeners[name] = &envoyListener{
		base: map[string]any{
			"name": name,
			"address": map[string]any{
				"socket_address": map[string]any{"address": address, "port_value": port},
			},
		},
		chainBySig: map[string]int{},
	}
}

// EnsureQUICListener returns (creating on first call) the named UDP/QUIC
// listener, bound to address:port over UDP with quic_options. Idempotent — the
// first call wins. The QUIC peer of EnsureListener: filter chains attached here
// carry a QuicDownstreamTransport socket and an HTTP/3 HCM.
func (c *EnvoyConfig) EnsureQUICListener(name, address string, port int) {
	if _, ok := c.listeners[name]; ok {
		return
	}
	c.listeners[name] = &envoyListener{
		base: map[string]any{
			"name": name,
			"address": map[string]any{
				"socket_address": map[string]any{"protocol": "UDP", "address": address, "port_value": port},
			},
			"udp_listener_config": map[string]any{
				"quic_options":             map[string]any{},
				"downstream_socket_config": map[string]any{"prefer_gro": true},
			},
		},
		chainBySig: map[string]int{},
	}
}

// EnsureRawUDPListener creates (first call wins) a plain UDP listener for opaque
// datagram forwarding — a UDP socket_address with NO quic_options (the raw-udp
// peer of EnsureQUICListener). The udp_proxy listener_filter and its pinned route
// are attached by the terminal layer via SetListenerField; this listener carries
// NO filter_chains (matching examples/udp/envoy.yaml).
func (c *EnvoyConfig) EnsureRawUDPListener(name, address string, port int) {
	if _, ok := c.listeners[name]; ok {
		return
	}
	c.listeners[name] = &envoyListener{
		base: map[string]any{
			"name": name,
			"address": map[string]any{
				"socket_address": map[string]any{"protocol": "UDP", "address": address, "port_value": port},
			},
		},
		chainBySig: map[string]int{},
	}
}

// SetListenerField sets a top-level field on a listener (e.g. listener_filters).
func (c *EnvoyConfig) SetListenerField(listener, key string, value any) error {
	l, ok := c.listeners[listener]
	if !ok {
		return fmt.Errorf("envoy config: SetListenerField on unknown listener %q", listener)
	}
	l.base[key] = value
	return nil
}

// addChain appends a filter chain to a listener. A second chain with the same
// filter_chain_match is only legal if both are HCM chains — then their
// virtual_hosts are merged (the plaintext-HTTP case: all hosts share one
// raw_buffer chain, Host-routed). Same-match chains that are NOT both HCM (e.g.
// two tcp_proxy chains on one port) fail generation closed.
func (c *EnvoyConfig) addChain(listener string, chain map[string]any) error {
	l, ok := c.listeners[listener]
	if !ok {
		return fmt.Errorf("envoy config: addChain on unknown listener %q", listener)
	}
	sig, err := matchSignature(chain["filter_chain_match"])
	if err != nil {
		return err
	}
	if idx, dup := l.chainBySig[sig]; dup {
		if err := mergeHCMVHosts(l.chains[idx], chain); err != nil {
			return fmt.Errorf("envoy config: listener %q: duplicate filter_chain_match %q on non-mergeable chains: %w", listener, sig, err)
		}
		return nil
	}
	l.chainBySig[sig] = len(l.chains)
	l.chains = append(l.chains, chain)
	return nil
}

// AddCluster registers cluster, deduping by "name". An identical re-add is a
// no-op; a conflicting body is an error.
func (c *EnvoyConfig) AddCluster(cluster map[string]any) error {
	name, _ := cluster["name"].(string)
	if name == "" {
		return fmt.Errorf("envoy config: AddCluster with empty/absent name")
	}
	if existing, ok := c.clusters[name]; ok {
		if !reflect.DeepEqual(existing, cluster) {
			return fmt.Errorf("envoy config: conflicting cluster definitions for name %q", name)
		}
		return nil
	}
	c.clusters[name] = cluster
	return nil
}

// AddListener stores a fully pre-built listener map (its own filter_chains
// baked in) under its "name" field — the listener peer of AddCluster, for a
// once-per-generation listener that the layer/chain machinery doesn't build
// (e.g. the health-check listener). listenerList emits base verbatim when no
// chains are attached, so the baked-in filter_chains survive. Conflicting
// redefinition of an existing name is an error; an identical re-add is a no-op.
func (c *EnvoyConfig) AddListener(listener map[string]any) error {
	name, _ := listener["name"].(string)
	if name == "" {
		return fmt.Errorf("envoy config: AddListener with empty/absent name")
	}
	if existing, ok := c.listeners[name]; ok {
		if !reflect.DeepEqual(existing.base, listener) || len(existing.chains) > 0 {
			return fmt.Errorf("envoy config: conflicting listener definitions for name %q", name)
		}
		return nil
	}
	c.listeners[name] = &envoyListener{base: listener, chainBySig: map[string]int{}}
	return nil
}

// ClaimPermutation records key and reports whether it is newly seen (false on a
// repeat), so the orchestrator can skip a duplicate permutation.
func (c *EnvoyConfig) ClaimPermutation(key string) bool {
	if _, seen := c.seenPerms[key]; seen {
		return false
	}
	c.seenPerms[key] = struct{}{}
	return true
}

// SetUnmatchedDeny installs the listener-level default_filter_chain — the
// terminal reached when no chain matches (the zero-layer deny). Last write wins.
func (c *EnvoyConfig) SetUnmatchedDeny(listener string, chain map[string]any) error {
	l, ok := c.listeners[listener]
	if !ok {
		return fmt.Errorf("envoy config: SetUnmatchedDeny on unknown listener %q", listener)
	}
	l.defaultDeny = chain
	return nil
}

// Bytes assembles the bootstrap tree (listeners and clusters name-sorted) and
// marshals it to YAML.
func (c *EnvoyConfig) Bytes() ([]byte, error) {
	root := map[string]any{
		"static_resources": map[string]any{
			"listeners": c.listenerList(),
			"clusters":  c.clusterList(),
		},
	}
	if c.admin != nil {
		root["admin"] = c.admin
	}
	return yaml.Marshal(root)
}

func (c *EnvoyConfig) listenerList() []map[string]any {
	names := make([]string, 0, len(c.listeners))
	for n := range c.listeners {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		l := c.listeners[n]
		m := map[string]any{}
		maps.Copy(m, l.base)
		if len(l.chains) > 0 {
			m["filter_chains"] = l.chains
		}
		if l.defaultDeny != nil {
			m["default_filter_chain"] = l.defaultDeny
		}
		out = append(out, m)
	}
	return out
}

func (c *EnvoyConfig) clusterList() []map[string]any {
	names := make([]string, 0, len(c.clusters))
	for n := range c.clusters {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		out = append(out, c.clusters[n])
	}
	return out
}

// matchSignature returns a deterministic signature of a filter_chain_match for
// dedup (yaml.v3 sorts map keys). A nil match signs as "".
func matchSignature(match any) (string, error) {
	if match == nil {
		return "", nil
	}
	b, err := yaml.Marshal(match)
	if err != nil {
		return "", fmt.Errorf("marshal filter_chain_match for dedup: %w", err)
	}
	return string(b), nil
}

// mergeHCMVHosts unions the virtual_hosts of incoming into existing (dedup by
// vhost name, so a shared deny_all collapses). Both chains must be single-HCM
// chains; otherwise it errors (the caller turns that into a fail-closed
// duplicate-match error). This is the generic structural reconciliation behind
// the plaintext-HTTP shared chain — no protocol-routing logic.
func mergeHCMVHosts(existing, incoming map[string]any) error {
	ev, err := hcmVHosts(existing)
	if err != nil {
		return err
	}
	iv, err := hcmVHosts(incoming)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(ev)+len(iv))
	merged := make([]any, 0, len(ev)+len(iv))
	for _, v := range append(append([]any{}, ev...), iv...) {
		name, _ := v.(map[string]any)["name"].(string)
		if seen[name] {
			continue
		}
		seen[name] = true
		merged = append(merged, v)
	}
	setHCMVHosts(existing, merged)
	return nil
}

// hcmVHosts returns a single-HCM chain's virtual_hosts, or an error if the chain
// is not a single http_connection_manager filter.
func hcmVHosts(chain map[string]any) ([]any, error) {
	filters, _ := chain["filters"].([]any)
	if len(filters) != 1 {
		return nil, fmt.Errorf("chain has %d network filters, expected a single HCM", len(filters))
	}
	f, _ := filters[0].(map[string]any)
	if f == nil || f["name"] != "envoy.filters.network.http_connection_manager" {
		return nil, fmt.Errorf("chain terminal is not an HCM")
	}
	tc, _ := f["typed_config"].(map[string]any)
	rc, _ := tc["route_config"].(map[string]any)
	vh, _ := rc["virtual_hosts"].([]any)
	return vh, nil
}

func setHCMVHosts(chain map[string]any, vhosts []any) {
	rc := chain["filters"].([]any)[0].(map[string]any)["typed_config"].(map[string]any)["route_config"].(map[string]any)
	rc["virtual_hosts"] = vhosts
}
