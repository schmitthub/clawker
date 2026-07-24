package firewall_test

import (
	"hash/fnv"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/firewall"
	ebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/config"
)

// TestValidateDst exercises the pure ValidateDst function across the full
// valid/invalid destination matrix. Lives at the store layer because
// ValidateDst is the gatekeeper for anything written to egress-rules.yaml.
func TestValidateDst(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		dst     string
		wantErr bool
	}{
		// Valid domains.
		{name: "simple domain", dst: "example.com"},
		{name: "subdomain", dst: "api.github.com"},
		{name: "deep subdomain", dst: "a.b.c.example.com"},
		{name: "wildcard", dst: ".example.com"},
		{name: "trailing dot", dst: "example.com."},
		{name: "wildcard trailing dot", dst: ".example.com."},
		{name: "with hyphen", dst: "my-api.example.com"},
		{name: "with digits", dst: "api2.example.com"},
		{name: "all digits label", dst: "123.example.com"},
		{name: "single label", dst: "localhost"},
		{name: "underscore", dst: "_dmarc.example.com"},
		{name: "digits with hyphen", dst: "123-456"},

		// Case — must be lowercase.
		{name: "uppercase", dst: "EXAMPLE.COM", wantErr: true},
		{name: "mixed case", dst: "Api.GitHub.Com", wantErr: true},
		{name: "wildcard uppercase", dst: ".EXAMPLE.COM", wantErr: true},

		// Multi-dot TLD and new gTLD.
		{name: "co.uk", dst: "api.example.co.uk"},
		{name: "new gTLD", dst: "my.example.technology"},

		// Domain length boundaries (253 chars max after normalization).
		{
			name: "total 253 chars valid",
			dst: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." +
				strings.Repeat("c", 63) + "." + strings.Repeat("d", 61),
			wantErr: false,
		},
		{
			name: "total 254 chars invalid",
			dst: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." +
				strings.Repeat("c", 63) + "." + strings.Repeat("d", 62),
			wantErr: true,
		},

		// Valid IPs and CIDRs.
		{name: "IPv4", dst: "192.168.1.1"},
		{name: "IPv6", dst: "::1"},
		{name: "CIDR", dst: "10.0.0.0/8"},

		// Wildcard-prefixed IPs/CIDRs — wildcards only make sense for domains.
		{name: "wildcard IPv4", dst: ".192.168.1.1", wantErr: true},
		{name: "wildcard CIDR", dst: ".10.0.0.0/8", wantErr: true},
		{name: "wildcard IPv6", dst: ".::1", wantErr: true},

		// Invalid.
		{name: "empty", dst: "", wantErr: true},
		{name: "just dot", dst: ".", wantErr: true},
		{name: "just dots", dst: "..", wantErr: true},
		{name: "spaces", dst: "example .com", wantErr: true},
		{name: "leading hyphen", dst: "-example.com", wantErr: true},
		{name: "trailing hyphen label", dst: "example-.com", wantErr: true},
		{name: "special chars", dst: "example!.com", wantErr: true},
		{name: "at sign", dst: "user@example.com", wantErr: true},
		{name: "path", dst: "example.com/path", wantErr: true},
		{name: "port", dst: "example.com:443", wantErr: true},
		{name: "scheme", dst: "https://example.com", wantErr: true},
		{name: "empty label", dst: "example..com", wantErr: true},
		{name: "double trailing dot", dst: "example.com..", wantErr: true},
		{name: "wildcard double trailing dot", dst: ".example.com..", wantErr: true},
		{name: "label too long", dst: strings.Repeat("a", 64) + ".com", wantErr: true},
		{name: "all numeric", dst: "123.456", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := firewall.ValidateDst(tt.dst)
			if tt.wantErr {
				assert.Error(t, err, "ValidateDst(%q) should fail", tt.dst)
			} else {
				assert.NoError(t, err, "ValidateDst(%q) should succeed", tt.dst)
			}
		})
	}
}

// TestRoutesFromRules_TCPMappingsParity locks in the invariant that
// RoutesFromRules and TCPMappings agree on (a) which rules produce
// output, (b) the effective destination port for TCP/SSH rules, and
// (c) the Envoy listener port assigned per rule. A mismatch silently
// misroutes traffic — e.g. an SSH rule whose BPF route points at the
// main TLS listener gets reset by tls_inspector's deny chain.
// routePresent reports whether out contains a route matching the structural
// routing fields (Identity, DstPort, EnvoyPort, L4Proto), ignoring SeedIP. The
// mapping↔route parity loops use it to assert no Envoy listener is orphaned without
// re-deriving SeedIP (the table cases pin that exactly via require.Equal).
func routePresent(out []ebpf.Route, id ebpf.RouteIdentity, dstPort, envoyPort uint16, l4 uint8) bool {
	for _, r := range out {
		if r.Identity == id && r.DstPort == dstPort && r.EnvoyPort == envoyPort && r.L4Proto == l4 {
			return true
		}
	}
	return false
}

// tid is an arbitrary-but-stable test mapping from dst to a fake route
// identity, standing in for the production IdentityAllocator (which
// allocates, never derives). Any pure function of dst works here; the
// tests only need per-dst stability within a run.
func tid(dst string) ebpf.RouteIdentity {
	h := fnv.New32a()
	h.Write([]byte(dst))
	return ebpf.RouteIdentity(h.Sum32())
}

// tidResolver adapts tid to the IdentityResolver shape RoutesFromRules takes.
func tidResolver(dst string) (ebpf.RouteIdentity, bool) { return tid(dst), true }

func TestRoutesFromRules_TCPMappingsParity(t *testing.T) {
	t.Parallel()
	ports := firewall.EnvoyPorts{EgressPort: 10000, TCPPortBase: 11000, UDPPortBase: 12000}

	tests := []struct {
		name  string
		rules []config.EgressRule
		want  []ebpf.Route
	}{
		{
			name: "tcp rule with empty action is treated as allow",
			rules: []config.EgressRule{
				{Dst: "github.com", Proto: "tcp", Port: "8080" /* Action: "" */},
			},
			want: []ebpf.Route{
				{Identity: tid("github.com"), DstPort: 8080, EnvoyPort: 11000, L4Proto: ebpf.L4ProtoTCP, SeedIP: 0},
			},
		},
		{
			name: "ssh rule with port=0 uses default 22",
			rules: []config.EgressRule{
				{Dst: "git.example.com", Proto: "ssh", Action: "allow" /* Port: 0 */},
			},
			want: []ebpf.Route{
				{Identity: tid("git.example.com"), DstPort: 22, EnvoyPort: 11000, L4Proto: ebpf.L4ProtoTCP, SeedIP: 0},
			},
		},
		{
			name: "tcp rule with port=0 uses default 443",
			rules: []config.EgressRule{
				{Dst: "a.example.com", Proto: "tcp", Action: "allow" /* Port: 0 */},
			},
			want: []ebpf.Route{
				{Identity: tid("a.example.com"), DstPort: 443, EnvoyPort: 11000, L4Proto: ebpf.L4ProtoTCP, SeedIP: 0},
			},
		},
		{
			name: "multiple tcp rules assign sequential EnvoyPorts, port defaulting does not desync the index",
			rules: []config.EgressRule{
				{Dst: "a.example.com", Proto: "tcp", Action: "allow" /* Port: 0 */},
				{Dst: "b.example.com", Proto: "tcp", Action: "allow", Port: "8080"},
			},
			want: []ebpf.Route{
				{Identity: tid("a.example.com"), DstPort: 443, EnvoyPort: 11000, L4Proto: ebpf.L4ProtoTCP, SeedIP: 0},
				{Identity: tid("b.example.com"), DstPort: 8080, EnvoyPort: 11001, L4Proto: ebpf.L4ProtoTCP, SeedIP: 0},
			},
		},
		{
			// An https FQDN rule routes BOTH the TCP egress chain and the QUIC/h3
			// sibling (quicSNIChainLayer, on UDP EgressPort): one L4ProtoTCP route
			// and one L4ProtoUDP route, both to EgressPort. The l4_proto byte keeps
			// them distinct on the same {domain, port}.
			name: "https rule routes both the TCP egress chain and the QUIC/h3 sibling",
			rules: []config.EgressRule{
				{Dst: "api.example.com", Proto: "https", Action: "allow", Port: "443"},
			},
			want: []ebpf.Route{
				{Identity: tid("api.example.com"), DstPort: 443, EnvoyPort: 10000, L4Proto: ebpf.L4ProtoTCP, SeedIP: 0},
				{Identity: tid("api.example.com"), DstPort: 443, EnvoyPort: 10000, L4Proto: ebpf.L4ProtoUDP, SeedIP: 0},
			},
		},
		{
			// An OPAQUE deny (tcp/ssh/udp) now builds a first-class dedicated deny
			// listener (blackhole), so it MUST get an eBPF route to that listener —
			// otherwise the listener is orphaned (the parity invariant below). The
			// denied port is redirected to Envoy's deny chain → active reset +
			// action=denied access log, NOT a silent default-deny drop. A NON-opaque
			// deny (https) generates no listener, so it still produces no route.
			name: "opaque deny routes to its deny listener; non-opaque deny does not",
			rules: []config.EgressRule{
				{Dst: "api.example.com", Proto: "https", Action: "deny", Port: "443"},
				{Dst: "git.example.com", Proto: "ssh", Action: "deny"},
			},
			want: []ebpf.Route{
				{Identity: tid("git.example.com"), DstPort: 22, EnvoyPort: 11000, L4Proto: ebpf.L4ProtoTCP, SeedIP: 0},
			},
		},
		{
			// TCP-IP now gets a SEEDED dedicated-listener route (mirrors UDP-IP):
			// TCPMappings skips only CIDR, so a bare-IP tcp/ssh rule gets its own
			// STATIC-pinned listener at TCPPortBase+idx, and RoutesFromRules projects
			// it with SeedIP set so SyncRoutes seeds dns_cache[ip]=identity(ip) (no
			// CoreDNS resolution exists for a literal IP). The eBPF connect4 NAT
			// rewrites the socket dst, so a single IP CANNOT ride the shared egress
			// listener's prefix_ranges — the dedicated STATIC listener is the fix.
			// CIDR-https still gets no route: the TLS/HTTP pass skips isIPOrCIDR (a
			// CIDR rides the shared egress listener via prefix_ranges).
			name: "tcp-ip projects a seeded route; cidr-https gets none",
			rules: []config.EgressRule{
				{Dst: "10.0.0.1", Proto: "tcp", Action: "allow", Port: "22"},
				{Dst: "10.0.0.0/24", Proto: "https", Action: "allow", Port: "443"},
			},
			want: []ebpf.Route{
				{
					Identity:  tid("10.0.0.1"),
					DstPort:   22,
					EnvoyPort: 11000,
					L4Proto:   ebpf.L4ProtoTCP,
					SeedIP:    ebpf.IPToUint32(net.ParseIP("10.0.0.1").To4()),
				},
			},
		},
		{
			// UDP-IP DOES get a route: UDPMappings gives a bare-IP udp rule its own
			// dedicated STATIC-pinned listener, and RoutesFromRules projects it with
			// SeedIP set so SyncRoutes seeds dns_cache[ip]=identity(ip) — that lets
			// connect4/sendmsg4 hit on the literal IP (no CoreDNS resolution exists).
			name: "udp ip dst projects a seeded route",
			rules: []config.EgressRule{
				{Dst: "10.0.0.5", Proto: "udp", Action: "allow", Port: "3478"},
			},
			want: []ebpf.Route{
				{
					Identity:  tid("10.0.0.5"),
					DstPort:   3478,
					EnvoyPort: 12000,
					L4Proto:   ebpf.L4ProtoUDP,
					SeedIP:    ebpf.IPToUint32(net.ParseIP("10.0.0.5").To4()),
				},
			},
		},
		{
			// FQDN raw-UDP projects into the route_map keyed L4ProtoUDP, indexed from
			// UDPPortBase. Consumed by the connect4 SOCK_DGRAM lookup (connected UDP).
			name: "fqdn udp rule projects a UDP route from UDPPortBase",
			rules: []config.EgressRule{
				{Dst: "relay.example.com", Proto: "udp", Action: "allow", Port: "3478"},
			},
			want: []ebpf.Route{
				{
					Identity:  tid("relay.example.com"),
					DstPort:   3478,
					EnvoyPort: 12000,
					L4Proto:   ebpf.L4ProtoUDP,
					SeedIP:    0,
				},
			},
		},
		{
			// l4_proto keeps a tcp and a udp route on the SAME {domain, port} from
			// colliding: distinct keys, distinct Envoy listeners, both present.
			name: "tcp and udp on same domain+port coexist as two independent routes",
			rules: []config.EgressRule{
				{Dst: "dual.example.com", Proto: "tcp", Action: "allow", Port: "443"},
				{Dst: "dual.example.com", Proto: "udp", Action: "allow", Port: "443"},
			},
			want: []ebpf.Route{
				{
					Identity:  tid("dual.example.com"),
					DstPort:   443,
					EnvoyPort: 11000,
					L4Proto:   ebpf.L4ProtoTCP,
					SeedIP:    0,
				},
				{
					Identity:  tid("dual.example.com"),
					DstPort:   443,
					EnvoyPort: 12000,
					L4Proto:   ebpf.L4ProtoUDP,
					SeedIP:    0,
				},
			},
		},
		{
			// A port_range fans one opaque tcp rule into one dedicated listener per
			// in-range port; RoutesFromRules must mirror that fan-out one-to-one so
			// the eBPF route_map and the Envoy listener layout stay in lockstep.
			name: "tcp port_range fans into one sequential route per in-range port",
			rules: []config.EgressRule{
				{Dst: "cluster.example.com", Proto: "tcp", Action: "allow", Port: "9000-9002"},
			},
			want: []ebpf.Route{
				{
					Identity:  tid("cluster.example.com"),
					DstPort:   9000,
					EnvoyPort: 11000,
					L4Proto:   ebpf.L4ProtoTCP,
					SeedIP:    0,
				},
				{
					Identity:  tid("cluster.example.com"),
					DstPort:   9001,
					EnvoyPort: 11001,
					L4Proto:   ebpf.L4ProtoTCP,
					SeedIP:    0,
				},
				{
					Identity:  tid("cluster.example.com"),
					DstPort:   9002,
					EnvoyPort: 11002,
					L4Proto:   ebpf.L4ProtoTCP,
					SeedIP:    0,
				},
			},
		},
		{
			// ws/wss ride the shared egress listener as their base http/https proto.
			// A wss+https pair for one origin is ONE stack in Envoy, so it collapses
			// to ONE TCP egress route (the eBPF layer can't distinguish them — same
			// host:port → same redirect). Because https/wss are TLS-bearing they ALSO
			// get the QUIC/h3 sibling, so the pair yields exactly one TCP route + one
			// h3 (L4ProtoUDP) route to EgressPort — not a second egress route per rule.
			name: "wss+https collapse to one TCP egress route plus the shared h3 route",
			rules: []config.EgressRule{
				{Dst: "stream.example.com", Proto: "https", Action: "allow", Port: "443"},
				{Dst: "stream.example.com", Proto: "wss", Action: "allow", Port: "443"},
			},
			want: []ebpf.Route{
				{
					Identity:  tid("stream.example.com"),
					DstPort:   443,
					EnvoyPort: 10000,
					L4Proto:   ebpf.L4ProtoTCP,
					SeedIP:    0,
				},
				{
					Identity:  tid("stream.example.com"),
					DstPort:   443,
					EnvoyPort: 10000,
					L4Proto:   ebpf.L4ProtoUDP,
					SeedIP:    0,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, missed := firewall.RoutesFromRules(tt.rules, ports, tidResolver)
			require.Equal(t, tt.want, got)
			assert.Empty(t, missed, "tidResolver answers every dst — no misses expected")

			// Parity check: every TCP/SSH mapping TCPMappings produces must appear
			// in the route set, matched on the structural fields that drive listener
			// routing (Identity, DstPort, EnvoyPort, L4Proto). SeedIP exactness is
			// pinned by the table `want` above via require.Equal — re-deriving it here
			// would only mirror RoutesFromRules' own formula and could never fail. This
			// guards the orphaned-listener invariant against Envoy/eBPF-side drift.
			for _, m := range firewall.TCPMappings(tt.rules, ports) {
				assert.Truef(
					t,
					routePresent(got, tid(m.Dst), uint16(m.DstPort), uint16(m.EnvoyPort), ebpf.L4ProtoTCP),
					"TCPMappings mapping %s:%d has no matching BPF route — Envoy listener would be orphaned",
					m.Dst,
					m.DstPort,
				)
			}

			// UDP parity: every UDPMappings entry (FQDN or single IP — CIDR is dropped
			// by its isCIDR skipDst) must appear as an L4ProtoUDP route on the same
			// structural fields. SeedIP is pinned by the table cases, not re-derived here.
			for _, m := range firewall.UDPMappings(tt.rules, ports) {
				assert.Truef(
					t,
					routePresent(got, tid(m.Dst), uint16(m.DstPort), uint16(m.EnvoyPort), ebpf.L4ProtoUDP),
					"UDPMappings mapping %s:%d has no matching BPF route — udp_proxy listener would be orphaned",
					m.Dst,
					m.DstPort,
				)
			}
		})
	}
}

// TestRoutesFromRules_ResolverMissFailsClosed pins the fail-closed contract
// for a PARTIAL resolver miss: the missed dst gets NO route at all (never an
// Identity:0 route — 0 is the "none" sentinel and would alias in route_map),
// every other rule's routes are unaffected, and the miss is reported exactly
// once even when dedicated-listener fan-out asks about the dst repeatedly
// (one https lookup + one tcp-mapping lookup here).
func TestRoutesFromRules_ResolverMissFailsClosed(t *testing.T) {
	t.Parallel()
	ports := firewall.EnvoyPorts{EgressPort: 10000, TCPPortBase: 11000, UDPPortBase: 12000, HealthPort: 0}
	const missDst = "missing.example.com"

	missOne := func(dst string) (ebpf.RouteIdentity, bool) {
		if dst == missDst {
			return 0, false
		}
		return tid(dst), true
	}

	rules := []config.EgressRule{
		{
			Dst: "github.com", Proto: "https", Action: "allow", Port: "443",
			PathRules: nil, PathDefault: "", InsecureSkipTLSVerify: false,
		},
		{
			Dst: missDst, Proto: "https", Action: "allow", Port: "443",
			PathRules: nil, PathDefault: "", InsecureSkipTLSVerify: false,
		},
		{
			Dst: missDst, Proto: "tcp", Action: "allow", Port: "8080",
			PathRules: nil, PathDefault: "", InsecureSkipTLSVerify: false,
		},
	}

	got, missed := firewall.RoutesFromRules(rules, ports, missOne)

	assert.Equal(t, []string{missDst}, missed,
		"miss must be reported exactly once despite multiple lookups for the same dst")
	for _, r := range got {
		assert.NotZerof(t, r.Identity, "no route may carry Identity:0 — a miss must drop the route, got %+v", r)
		assert.NotEqualf(t, tid(missDst), r.Identity, "missed dst must produce no route, got %+v", r)
	}
	want := []ebpf.Route{
		{Identity: tid("github.com"), DstPort: 443, EnvoyPort: 10000, L4Proto: ebpf.L4ProtoTCP, SeedIP: 0},
		{Identity: tid("github.com"), DstPort: 443, EnvoyPort: 10000, L4Proto: ebpf.L4ProtoUDP, SeedIP: 0},
	}
	assert.Equal(t, want, got, "routes for resolvable dsts must be unaffected by the miss")
}

// TestEgressRulesFileFields_AllFieldsHaveDescriptions guards the storage
// schema contract: every YAML field on EgressRulesFile must carry a desc tag
// so the storeui TUI can display meaningful help text.
func TestEgressRulesFileFields_AllFieldsHaveDescriptions(t *testing.T) {
	fs := firewall.EgressRulesFile{}.Fields()
	for _, f := range fs.All() {
		assert.NotEmptyf(t, f.Description(), "field %q has no desc tag", f.Path())
	}
}

// TestNormalizeAndDedup_ResolvesOpaquePortConflicts is the gate that must run
// BEFORE Envoy generation. An opaque rule fans into one dedicated listener PER
// in-range port (tcp_<dst>_<port>), so a range and an overlapping single port for
// the same dst:proto would emit the SAME listener twice and fail generation.
// NormalizeAndDedup resolves opaque port conflicts up front with two rules:
//
//   - same-action overlapping spans MERGE into one span set (one owner per port);
//   - DENY ALWAYS WINS: any port in a deny span is carved out of the allow spans
//     and emitted as an explicit deny rule, so a denied port is never silently
//     allowed by an overlapping allow range (and vice-versa — an allow inside a
//     deny range is swallowed).
//
// Cross-proto overlap (tcp + ssh share the tcp_ listener family) is a genuine
// conflict left to the generator's span-aware collision check, NOT merged here.
func TestNormalizeAndDedup_ResolvesOpaquePortConflicts(t *testing.T) {
	t.Parallel()
	const dst = "45.79.112.203"
	// portsFor collects the resulting Port specs for a given proto+action, so a
	// case can assert exactly which rules survived resolution.
	portsFor := func(out []config.EgressRule, proto, action string) []string {
		var ports []string
		for _, r := range out {
			if r.Dst == dst && r.Proto == proto && r.Action == action {
				ports = append(ports, r.Port)
			}
		}
		return ports
	}

	tests := []struct {
		name string
		in   []config.EgressRule
		// each closure asserts the surviving rules for the case.
		want func(t *testing.T, out []config.EgressRule)
	}{
		{
			// allow range + allow single (inside): the exact scenario that broke on
			// first use. 4243 ∈ [4242,5000] → merge into the range, one owner per port.
			name: "allow range + allow single merges",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242-5000", Action: "allow"},
				{Dst: dst, Proto: "tcp", Port: "4243", Action: "allow"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.Equal(t, []string{"4242-5000"}, portsFor(out, "tcp", "allow"))
				assert.Empty(t, portsFor(out, "tcp", "deny"))
			},
		},
		{
			name: "allow single + allow range (reverse order) merges",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242", Action: "allow"},
				{Dst: dst, Proto: "tcp", Port: "4242-4243", Action: "allow"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.Equal(t, []string{"4242-4243"}, portsFor(out, "tcp", "allow"))
			},
		},
		{
			name: "two partially-overlapping allow ranges union",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242-4250", Action: "allow"},
				{Dst: dst, Proto: "tcp", Port: "4248-4260", Action: "allow"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.Equal(t, []string{"4242-4260"}, portsFor(out, "tcp", "allow"))
			},
		},
		{
			name: "disjoint allow ports stay separate",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242", Action: "allow"},
				{Dst: dst, Proto: "tcp", Port: "5000", Action: "allow"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.ElementsMatch(t, []string{"4242", "5000"}, portsFor(out, "tcp", "allow"))
			},
		},
		{
			// Adjacent-but-disjoint: 4242 and 4243-4244 touch but share no port, so
			// they land on distinct listeners and must NOT be merged.
			name: "adjacent disjoint allow ranges stay separate",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242", Action: "allow"},
				{Dst: dst, Proto: "tcp", Port: "4243-4244", Action: "allow"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.ElementsMatch(t, []string{"4242", "4243-4244"}, portsFor(out, "tcp", "allow"))
			},
		},
		{
			// allow range + deny single (inside): deny wins. 4242 carved out → an
			// explicit deny:4242 plus the surviving allow:4243.
			name: "allow range + deny single carves the port (deny wins)",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242-4243", Action: "allow"},
				{Dst: dst, Proto: "tcp", Port: "4242", Action: "deny"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.Equal(t, []string{"4243"}, portsFor(out, "tcp", "allow"), "denied port carved from allow")
				assert.Equal(t, []string{"4242"}, portsFor(out, "tcp", "deny"), "explicit deny persists")
			},
		},
		{
			// allow range + deny single in the MIDDLE splits the allow into two spans.
			name: "deny single in middle splits allow range",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242-4250", Action: "allow"},
				{Dst: dst, Proto: "tcp", Port: "4246", Action: "deny"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.ElementsMatch(t, []string{"4242-4245", "4247-4250"}, portsFor(out, "tcp", "allow"))
				assert.Equal(t, []string{"4246"}, portsFor(out, "tcp", "deny"))
			},
		},
		{
			// deny range + allow single (inside): the single allow is swallowed —
			// ALL of the range is denied, nothing allowed.
			name: "deny range + allow single is fully denied",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242-4250", Action: "deny"},
				{Dst: dst, Proto: "tcp", Port: "4245", Action: "allow"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.Empty(t, portsFor(out, "tcp", "allow"), "allow swallowed by deny range")
				assert.Equal(t, []string{"4242-4250"}, portsFor(out, "tcp", "deny"))
			},
		},
		{
			// deny range + deny single: merge into one deny span.
			name: "deny range + deny single merges",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242-4250", Action: "deny"},
				{Dst: dst, Proto: "tcp", Port: "4245", Action: "deny"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.Equal(t, []string{"4242-4250"}, portsFor(out, "tcp", "deny"))
				assert.Empty(t, portsFor(out, "tcp", "allow"))
			},
		},
		{
			// allow range ∩ deny range (partial overlap): deny wins on the overlap,
			// allow keeps only the non-overlapping prefix.
			name: "allow range partially overlapped by deny range",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242-4250", Action: "allow"},
				{Dst: dst, Proto: "tcp", Port: "4248-4260", Action: "deny"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.Equal(t, []string{"4242-4247"}, portsFor(out, "tcp", "allow"))
				assert.Equal(t, []string{"4248-4260"}, portsFor(out, "tcp", "deny"))
			},
		},
		{
			// inverse direction: deny range overlaps an allow range — deny wins on
			// the overlap, allow keeps only the non-overlapping suffix.
			name: "deny range partially overlapped by allow range",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242-4250", Action: "deny"},
				{Dst: dst, Proto: "tcp", Port: "4245-4260", Action: "allow"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.Equal(t, []string{"4251-4260"}, portsFor(out, "tcp", "allow"))
				assert.Equal(t, []string{"4242-4250"}, portsFor(out, "tcp", "deny"))
			},
		},
		{
			// tcp and ssh share the tcp_<dst>_<port> listener family, so an
			// overlapping port across them is a genuine conflict — NOT merged here;
			// it fails closed in the generator's span-aware collision check.
			name: "cross-proto overlap is not merged",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242", Action: "allow"},
				{Dst: dst, Proto: "ssh", Port: "4242", Action: "allow"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.Equal(t, []string{"4242"}, portsFor(out, "tcp", "allow"))
				assert.Equal(
					t,
					[]string{"4242"},
					portsFor(out, "ssh", "allow"),
					"ssh rule preserved (not merged into tcp)",
				)
			},
		},
		{
			// All-single allow + deny on the SAME port: no range to carve, so this is
			// a contradiction, not a deny-wins carve. The resolver leaves BOTH rules
			// intact (the generator rejects the clash loud).
			name: "all-single allow + deny same port left intact (no silent carve)",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242", Action: "allow"},
				{Dst: dst, Proto: "tcp", Port: "4242", Action: "deny"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.Equal(
					t,
					[]string{"4242"},
					portsFor(out, "tcp", "allow"),
					"allow single NOT carved (no range present)",
				)
				assert.Equal(t, []string{"4242"}, portsFor(out, "tcp", "deny"), "deny single survives")
			},
		},
		{
			// Mixed: an all-single clash (4242) coexists with a legit range carve
			// (deny 5001 ∈ allow 5000-5002). The range carve still happens (a range is
			// present THERE); the all-single 4242 clash is left intact for the loud
			// rejection — gating is per-conflict, not whole-group.
			name: "all-single clash preserved alongside an unrelated range carve",
			in: []config.EgressRule{
				{Dst: dst, Proto: "tcp", Port: "4242", Action: "allow"},
				{Dst: dst, Proto: "tcp", Port: "4242", Action: "deny"},
				{Dst: dst, Proto: "tcp", Port: "5000-5002", Action: "allow"},
				{Dst: dst, Proto: "tcp", Port: "5001", Action: "deny"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.ElementsMatch(t, []string{"4242", "5000", "5002"}, portsFor(out, "tcp", "allow"))
				assert.ElementsMatch(t, []string{"4242", "5001"}, portsFor(out, "tcp", "deny"))
			},
		},
		{
			// udp resolves independently of tcp (separate listener family), and deny
			// still wins within udp.
			name: "udp allow range carved by udp deny single",
			in: []config.EgressRule{
				{Dst: dst, Proto: "udp", Port: "5000-5002", Action: "allow"},
				{Dst: dst, Proto: "udp", Port: "5001", Action: "deny"},
			},
			want: func(t *testing.T, out []config.EgressRule) {
				assert.ElementsMatch(t, []string{"5000", "5002"}, portsFor(out, "udp", "allow"))
				assert.Equal(t, []string{"5001"}, portsFor(out, "udp", "deny"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, _ := firewall.NormalizeAndDedup(tc.in)
			tc.want(t, out)
		})
	}
}

// TestMergeRule_CallerWinsScalars asserts that on a same-RuleKey merge,
// the caller's Action and PathDefault overwrite the existing values.
func TestMergeRule_CallerWinsScalars(t *testing.T) {
	t.Parallel()
	existing := config.EgressRule{
		Dst: "api.example.com", Proto: "https", Port: "443",
		Action:      "allow",
		PathDefault: "allow",
	}
	incoming := config.EgressRule{
		Dst: "api.example.com", Proto: "https", Port: "443",
		Action:      "deny",
		PathDefault: "deny",
	}
	got := firewall.MergeRule(existing, incoming)
	assert.Equal(t, "deny", got.Action)
	assert.Equal(t, "deny", got.PathDefault)
}

// TestMergeRule_PathRulesUnionByPath asserts existing + incoming PathRules
// are unioned by Path, with existing-side order preserved and incoming-only
// entries appended.
func TestMergeRule_PathRulesUnionByPath(t *testing.T) {
	t.Parallel()
	existing := config.EgressRule{
		Dst: "api.example.com", Proto: "https", Port: "443",
		PathRules: []config.PathRule{
			{Path: "/v1", Action: "allow"},
		},
	}
	incoming := config.EgressRule{
		Dst: "api.example.com", Proto: "https", Port: "443",
		PathRules: []config.PathRule{
			{Path: "/v2", Action: "deny"},
		},
	}
	got := firewall.MergeRule(existing, incoming)
	require.Len(t, got.PathRules, 2)
	assert.Equal(t, "/v1", got.PathRules[0].Path)
	assert.Equal(t, "allow", got.PathRules[0].Action)
	assert.Equal(t, "/v2", got.PathRules[1].Path)
	assert.Equal(t, "deny", got.PathRules[1].Action)
}

// TestMergeRule_PathRulesSamePathCallerWins asserts that on a same-Path
// collision inside PathRules, the caller's PathRule overwrites in place
// rather than appending a duplicate.
func TestMergeRule_PathRulesSamePathCallerWins(t *testing.T) {
	t.Parallel()
	existing := config.EgressRule{
		Dst: "api.example.com", Proto: "https", Port: "443",
		PathRules: []config.PathRule{
			{Path: "/v1", Action: "allow"},
			{Path: "/v2", Action: "allow"},
		},
	}
	incoming := config.EgressRule{
		Dst: "api.example.com", Proto: "https", Port: "443",
		PathRules: []config.PathRule{
			{Path: "/v1", Action: "deny"},
		},
	}
	got := firewall.MergeRule(existing, incoming)
	require.Len(t, got.PathRules, 2)
	assert.Equal(t, "/v1", got.PathRules[0].Path)
	assert.Equal(t, "deny", got.PathRules[0].Action, "caller's action wins on path collision")
	assert.Equal(t, "/v2", got.PathRules[1].Path)
	assert.Equal(t, "allow", got.PathRules[1].Action)
}

// TestMergeRule_EmptyIncomingPathRules_PreservesExisting asserts the
// mergePathRules len(incoming)==0 short-circuit: an incoming rule with no
// PathRules must NOT wipe the existing rule's PathRules, but scalars on
// the incoming rule (Action, PathDefault) still win per the caller-wins
// rule.
func TestMergeRule_EmptyIncomingPathRules_PreservesExisting(t *testing.T) {
	t.Parallel()
	existing := config.EgressRule{
		Dst: "api.example.com", Proto: "https", Port: "443",
		Action:      "allow",
		PathDefault: "deny",
		PathRules: []config.PathRule{
			{Path: "/v1", Action: "allow"},
		},
	}
	incoming := config.EgressRule{
		Dst: "api.example.com", Proto: "https", Port: "443",
		Action:      "deny",
		PathDefault: "allow",
		// No PathRules — represents e.g. a bare `clawker firewall add`.
	}
	got := firewall.MergeRule(existing, incoming)
	assert.Equal(t, "deny", got.Action, "caller wins on Action")
	assert.Equal(t, "allow", got.PathDefault, "caller wins on PathDefault")
	require.Len(t, got.PathRules, 1, "existing PathRules preserved when incoming is empty")
	assert.Equal(t, "/v1", got.PathRules[0].Path)
}

// TestMergeRule_EmptyIncomingPathDefault_PreservesExisting locks in the
// invariant that a bare CLI add (no --path, no --path-default) must NOT
// clear a yaml-set PathDefault on the same RuleKey. The string field can't
// distinguish "unset" from "explicitly empty" so empty incoming defers to
// existing.
func TestMergeRule_EmptyIncomingPathDefault_PreservesExisting(t *testing.T) {
	t.Parallel()
	existing := config.EgressRule{
		Dst: "api.example.com", Proto: "https", Port: "443",
		Action:      "allow",
		PathDefault: "deny",
	}
	incoming := config.EgressRule{
		Dst: "api.example.com", Proto: "https", Port: "443",
		Action: "allow",
		// PathDefault unset — bare CLI add has nothing to say.
	}
	got := firewall.MergeRule(existing, incoming)
	assert.Equal(t, "deny", got.PathDefault, "empty incoming PathDefault does not clobber existing")
}

// TestValidateRule_Methods covers HTTP-method-token validation: well-formed
// tokens pass; anything carrying regex metacharacters (which would otherwise be
// embedded into the generated safe_regex alternation) is rejected.
func TestValidateRule_Methods(t *testing.T) {
	t.Parallel()
	mk := func(methods ...string) config.EgressRule {
		return config.EgressRule{
			Dst: "api.example.com", Proto: "https", Port: "443",
			PathRules: []config.PathRule{{Path: "/", Action: "allow", Methods: methods}},
		}
	}
	tests := []struct {
		name    string
		rule    config.EgressRule
		wantErr bool
	}{
		{"valid single", mk("GET"), false},
		{"valid multi", mk("GET", "HEAD", "POST"), false},
		{"valid lowercase token (case normalized later)", mk("get"), false},
		{"valid webdav extension verb", mk("MKCALENDAR"), false},
		{"empty methods ok", mk(), false},
		{"regex metachar rejected", mk("GET|HEAD"), true},
		{"glob metachar rejected", mk("GE.*"), true},
		{"space rejected", mk("GET POST"), true},
		{"empty token rejected", mk(""), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := firewall.ValidateRule(tc.rule)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestValidateRule_PathAnchoring covers path-rule anchoring: a literal path must
// start with "/" (no auto-prepend); a "~"-marked regex path must compile and
// anchor at the path root. ValidateRule is the single seam that guards every FW-
// rule-update path (CLI add, yaml bootstrap, firewall refresh) via handler.go.
func TestValidateRule_PathAnchoring(t *testing.T) {
	t.Parallel()
	mk := func(path string) config.EgressRule {
		return config.EgressRule{
			Dst: "api.example.com", Proto: "https", Port: "443",
			PathRules: []config.PathRule{{Path: path, Action: "allow"}},
		}
	}
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"literal prefix ok", "/repos/clawker/", false},
		{"literal root ok", "/", false},
		{"literal missing leading slash", "repos/clawker", true},
		// Literal sub-delims and other RFC 3986 pchars stay valid — these are
		// legitimate (if unusual) literal paths, not regex.
		{"literal sub-delims ok", "/p/(g)+a,b;c=1:2@x", false},
		{"literal percent ok", "/a%20b", false},
		{"literal tilde mid ok", "/~user/files", false},
		// Characters outside the RFC 3986 path set are rejected — almost always
		// a regex written without the leading "~" marker (the reported footgun).
		{"literal illegal brace (forgot ~)", "/blog{", true},
		{"literal illegal pipe (forgot ~)", "/u/(alice|bob)", true},
		{"literal illegal bracket (forgot ~)", "/api/v[0-9]", true},
		{"literal illegal caret (forgot ~)", "/blog^x", true},
		{"literal illegal backslash", "/foo\\bar", true},
		{"literal illegal space", "/foo bar", true},
		{"regex anchored ok", "~/repos/clawker", false},
		{"regex alternation ok", "~/repos/(clawker|anthropic)/?", false},
		{"regex with caret anchor ok", "~^/repos/clawker", false},
		{"regex subtree ok", "~/repos/clawker(/.*)?", false},
		{"regex repetition brace ok", "~/v[0-9]{1,3}", false},
		{"regex not anchored to slash", "~repos/clawker", true},
		{"regex bare marker", "~", true},
		{"regex will not compile", "~/repos/(clawker", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := firewall.ValidateRule(mk(tc.path))
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestNormalizeRule_Methods confirms a path rule's method set is uppercased,
// deduped, and sorted — and that an all-empty set collapses to nil so no
// :method matcher is emitted for the "all methods" case.
func TestNormalizeRule_Methods(t *testing.T) {
	t.Parallel()
	in := config.EgressRule{
		Dst: "api.example.com", Proto: "https", Port: "443",
		PathRules: []config.PathRule{
			{Path: "/", Action: "allow", Methods: []string{"post", "GET", "get", " head "}},
			{Path: "/x", Action: "deny", Methods: []string{"", "  "}},
		},
	}
	out := firewall.NormalizeRule(in)
	assert.Equal(t, []string{"GET", "HEAD", "POST"}, out.PathRules[0].Methods)
	assert.Nil(t, out.PathRules[1].Methods)
	// Input must not be mutated in place (shared backing array safety).
	assert.Equal(t, []string{"post", "GET", "get", " head "}, in.PathRules[0].Methods)
}

// TestNormalizeAndDedup_MethodsOnOpaqueWarns verifies that path/method rules on
// a non-HTTP proto surface a warning (they are ignored at generation), while the
// same shape on an HTTP-family proto does not.
func TestNormalizeAndDedup_MethodsOnOpaqueWarns(t *testing.T) {
	t.Parallel()
	opaque := []config.EgressRule{{
		Dst: "db.example.com", Proto: "tcp", Port: "5432",
		PathRules: []config.PathRule{{Path: "/", Action: "deny", Methods: []string{"POST"}}},
	}}
	_, warnings := firewall.NormalizeAndDedup(opaque)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "db.example.com")
	assert.Contains(t, warnings[0], "HTTP-family")

	httpFamily := []config.EgressRule{{
		Dst: "api.example.com", Proto: "https", Port: "443",
		PathRules: []config.PathRule{{Path: "/", Action: "allow", Methods: []string{"GET"}}},
	}}
	_, warnings = firewall.NormalizeAndDedup(httpFamily)
	assert.Empty(t, warnings)
}
