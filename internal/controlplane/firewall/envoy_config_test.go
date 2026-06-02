package firewall

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The ONLY firewall Envoy-generation tests. Three STRICT rules (see
// .claude/rules/envoy.md → Testing):
//  1. input is real egress-rules YAML parsed via storage.NewFromString — the
//     exact production read path, never structs/mocks/internals;
//  2. every case compares the COMPLETE generated config against a committed
//     control (the testdata/envoy/<case>.envoy.golden file), never field-level
//     structural assertions — a whole-config golden catches every chain, vhost,
//     cluster, filter, listener, and access-log change as a diff;
//  3. all cases are rows in this one table.
//
// Re-bless goldens with GOLDEN_UPDATE=1, then read the diff against
// ENVOY_TARGET.md before committing.

func testPorts() EnvoyPorts {
	return EnvoyPorts{EgressPort: 10000, TCPPortBase: 15000, UDPPortBase: 16000, HealthPort: 10001}
}

func TestGenerateEnvoyConfig(t *testing.T) {
	cases := []struct {
		name  string    // golden: testdata/envoy/<name>.envoy.golden
		rules string    // real egress-rules YAML, parsed via storage.NewFromString
		als   ALSConfig // generation-side access-log config (not part of the rules sample)
		// wantErrContains, when set, asserts GenerateEnvoyConfig FAILS with an error
		// containing this substring (the "control" for a fail-closed case) and skips
		// the golden compare — no config is produced.
		wantErrContains string
	}{
		{
			name: "http", // exact + wildcard, multi-port, path rules, plaintext DFP
			rules: `
rules:
  - dst: example.com
    proto: http
    port: 80
  - dst: .example.com
    proto: http
    port: 8080
  - dst: api.site.com
    proto: http
    port: 80
    path_default: deny
    path_rules:
      - path: /v1
        action: allow
      - path: /v1/internal
        action: deny
  - dst: .other.com
    proto: http
`,
		},
		{
			name: "http_exact_only", // exact-only → no DFP filter/cluster
			rules: `
rules:
  - dst: example.com
    proto: http
    port: 80
  - dst: some.com
    proto: http
`,
		},
		{
			name: "https", // exact + wildcard (+ apex dedup) + h3/QUIC + reencrypt DFP
			rules: `
rules:
  - dst: api.anthropic.com
    proto: https
  - dst: mintlify.com
    proto: https
  - dst: .mintlify.com
    proto: https
  - dst: .docs.example.com
    proto: https
`,
		},
		{
			name: "https_exact_only", // exact-only https + non-default port
			rules: `
rules:
  - dst: api.anthropic.com
    proto: https
  - dst: example.org
    proto: https
    port: 8443
`,
		},
		{
			name: "ip_dst", // IP literals as dst (not FQDNs) across http + https
			rules: `
rules:
  - dst: 192.168.1.1
    proto: http
    port: 80
  - dst: 192.168.1.1
    proto: http
    port: 8080
  - dst: 192.168.1.2
    proto: http
    port: 80
    path_default: deny
    path_rules:
      - path: /v1
        action: allow
      - path: /v1/internal
        action: deny
  - dst: 192.168.1.3
    proto: https
`,
		},
		{
			// Axis 4 — insecure_skip_tls_verify, orthogonal to dst type: a self-signed
			// FQDN dev host AND a self-signed IP dev host. Both upstream reencrypt
			// contexts carry trust_chain_verification: ACCEPT_UNTRUSTED; SAN binding
			// (auto_san_validation) still holds. The IP also exercises axes 1-3
			// (prefix_ranges gate, iPAddress-keyed cert path, STATIC cluster).
			name: "insecure_skip_verify",
			rules: `
rules:
  - dst: local.dev
    proto: https
    insecure_skip_tls_verify: true
  - dst: 10.0.0.5
    proto: https
    port: 8443
    insecure_skip_tls_verify: true
`,
		},
		{
			name: "raw_tcp", // opaque raw TCP: dedicated listener → tcp_proxy → pinned cluster
			rules: `
rules:
  - dst: db.example.com
    proto: tcp
    port: 5432
`,
		},
		{
			name: "ssh", // opaque SSH over TCP: same shape as raw tcp, proto token = ssh
			rules: `
rules:
  - dst: github.com
    proto: ssh
    port: 22
`,
		},
		{
			name: "raw_udp", // opaque raw UDP: dedicated UDP listener → udp_proxy → pinned cluster
			rules: `
rules:
  - dst: relay.example.com
    proto: udp
    port: 3478
`,
		},
		{
			name: "ws", // websocket over http: standalone ws rule promotes to an http stack + per-route upgrade_configs + HCM allow_connect
			rules: `
rules:
  - dst: realtime.io
    proto: ws
    port: 80
`,
		},
		{
			name: "wss", // websocket over https: standalone wss promotes to the https+quic stack, enriched (upgrade route, allow_connect/allow_extended_connect, upstream pinned http/1.1, MITM cert)
			rules: `
rules:
  - dst: stream.anthropic.com
    proto: wss
`,
		},
		{
			name: "ws_wildcard", // wildcard ws: plaintext http_dfp + websocket enrichment (no h1.1 pin — plaintext upstream)
			rules: `
rules:
  - dst: .chat.example.com
    proto: ws
`,
		},
		{
			name: "wss_wildcard", // wildcard wss: distinct wss_dfp cluster (h1.1) sharing the https dns_cache + websocket enrichment
			rules: `
rules:
  - dst: .stream.example.com
    proto: wss
`,
		},
		{
			// Additive UX: ws/wss is its own rule that ENRICHES the base http/https
			// stack for the same origin. https+wss → ONE enriched https stack (wss
			// absorbed, no proto-collision); http+ws → ONE enriched http stack. The
			// base rule owns structure; the ws/wss rule only flips the upgrade on.
			name: "ws_wss_absorb",
			rules: `
rules:
  - dst: secure.example.com
    proto: https
  - dst: secure.example.com
    proto: wss
  - dst: plain.example.com
    proto: http
    port: 80
  - dst: plain.example.com
    proto: ws
    port: 80
`,
		},
		{
			// Opaque TCP/SSH to an IP or CIDR rides the SHARED egress listener as a
			// prefix_ranges raw_buffer chain (the dst is known at gen time — no
			// dedicated listener, no SNI/Host discriminator needed). A bare IP pins
			// STATIC (the address IS the resolution); a CIDR forwards to ORIGINAL_DST
			// scoped by the chain's prefix_ranges (range = the grant). use_original_dst
			// recovers the real dst so prefix_ranges matches it.
			name: "opaque_ip_cidr",
			rules: `
rules:
  - dst: 192.168.1.5
    proto: tcp
    port: 5432
  - dst: 10.0.0.0/24
    proto: tcp
    port: 5432
  - dst: 203.0.113.7
    proto: ssh
    port: 22
`,
		},
		{
			// Raw UDP to a single IP → dedicated UDP listener → udp_proxy → STATIC
			// pin (UDP has no filter chains, so even a known IP gets its own listener;
			// the pin is the gate). Peer of opaque_ip_cidr's tcp single-IP case.
			name: "udp_ip",
			rules: `
rules:
  - dst: 192.168.1.9
    proto: udp
    port: 3478
`,
		},
		{
			// Raw UDP to a CIDR fails closed: udp_proxy has no original-destination
			// forwarding (only use_original_src_ip, which rewrites the source) and UDP
			// has no filter chains to pin per in-range host. No golden.
			name: "udp_cidr_unsupported",
			rules: `
rules:
  - dst: 10.0.0.0/24
    proto: udp
    port: 3478
`,
			wantErrContains: "raw udp to a CIDR range",
		},
		{
			name: "tcp_port_range", // opaque tcp port_range → one dedicated listener + pinned cluster per in-range port (mapping A, never ORIGINAL_DST)
			rules: `
rules:
  - dst: cluster.example.com
    proto: tcp
    port_range: "9000-9002"
`,
		},
		{
			// A port_range wide enough to push the tcp/ssh band past the udp base
			// must FAIL closed: a tcp + udp listener sharing one Envoy bind port is a
			// runtime bind failure, and the eBPF route_map can't disambiguate on
			// EnvoyPort. testPorts: TCP base 15000, UDP base 16000 → a 1002-wide tcp
			// range tops at 16001 and overlaps the udp listener. No golden.
			name: "port_range_band_overlap",
			rules: `
rules:
  - dst: cluster.example.com
    proto: tcp
    port_range: "9000-10001"
  - dst: relay.example.com
    proto: udp
    port: 3478
`,
			wantErrContains: "overlaps the raw-udp band",
		},
		{
			// A port_range whose fan-out tops past 65535 must FAIL closed: otherwise
			// rules_store.RoutesFromRules' uint16(EnvoyPort) cast would silently WRAP
			// and write a bogus eBPF route. testPorts TCP base 15000 + 60000 ports →
			// 74999 > 65535. No golden.
			name: "port_range_overflow_65535",
			rules: `
rules:
  - dst: huge.example.com
    proto: tcp
    port_range: "1-60000"
`,
			wantErrContains: "overflow past port 65535",
		},
		{
			name: "otel_mtls", // OTel access-log sink wired
			rules: `
rules:
  - dst: example.com
    proto: http
`,
			als: ALSConfig{Port: 4319, MTLS: true},
		},
		{
			name: "unsupported_proto", // ftp → not yet a supported token; skipped (no listener in output)
			rules: `
rules:
  - dst: host.com
    proto: ftp
    port: 21
`,
		},
		{
			// Two different protos on the same host:port must FAIL generation — a
			// host:port maps to exactly one network stack; the eBPF route_map is
			// keyed (host, port) with no proto, so allowing both would silently
			// race (last proto wins, the other's rules stranded). No golden.
			name: "proto_collision_same_port",
			rules: `
rules:
  - dst: collide.example.com
    proto: http
    port: 8443
  - dst: collide.example.com
    proto: https
    port: 8443
`,
			wantErrContains: "claimed by multiple protos",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := storage.NewFromString[EgressRulesFile](tc.rules)
			require.NoError(t, err, "parse rules sample via the storage engine")
			rules, _ := NormalizeAndDedup(store.Read().Rules)

			out, _, err := GenerateEnvoyConfig(rules, testPorts(), tc.als)
			if tc.wantErrContains != "" {
				require.Error(t, err, "expected generation to fail closed")
				assert.Contains(t, err.Error(), tc.wantErrContains)
				return // fail-closed case produces no config to compare
			}
			require.NoError(t, err)

			goldenPath := filepath.Join("testdata", "envoy", tc.name+".envoy.golden")
			if os.Getenv("GOLDEN_UPDATE") == "1" {
				require.NoError(t, os.WriteFile(goldenPath, out, 0o644))
			}
			want, err := os.ReadFile(goldenPath)
			require.NoError(t, err, "read golden (GOLDEN_UPDATE=1 to create)")
			assert.Equal(t, string(want), string(out), "generated config drifted from %s", goldenPath)
		})
	}
}
