package firewall

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/storage"
)

// The ONLY firewall Envoy-generation tests. Three STRICT rules (see
// .claude/rules/envoy.md → Testing):
//  1. input is real egress-rules YAML parsed via storage.New — the
//     exact production read path, never structs/mocks/internals;
//  2. every case compares the COMPLETE generated config against a committed
//     control (the testdata/envoy/<case>.envoy.golden file), never field-level
//     structural assertions — a whole-config golden catches every chain, vhost,
//     cluster, filter, listener, and access-log change as a diff;
//  3. all cases are rows in this one table.
//
// ADDING COVERAGE? Extend the `comprehensiveRules` const below and re-bless with
// GOLDEN_UPDATE=1 — do NOT add a new table row + new `*.envoy.golden`. A per-
// feature golden re-tests in isolation what the mega-config already covers, loses
// the cross-rule interaction diff, and rots. A new case is justified ONLY for one
// of the two reasons listed below; if neither applies, it goes in comprehensiveRules.
// (Full rationale: .claude/rules/envoy.md → Testing §.)
//
// Strategy: `comprehensive` (+ `comprehensive_mtls`) is the all-encompassing
// interaction golden — every co-existable feature in ONE config so cross-rule
// regressions surface as a single diff. The only other golden cases are the ones
// a mega-config STRUCTURALLY cannot express, because a mega-config forces every
// generation-wide fact ON and so can never observe a fact being absent:
//   - http_exact_only / https_exact_only — assert the DFP filter+cluster is
//     ABSENT (any wildcard rule in the mega turns DFP on globally);
//   - ssh — an opaque-only config has NO shared egress listener and NO deny
//     floor (any http/https rule creates the egress listener).
// Plus the fail-closed cases (no config produced), which cannot coexist with a
// valid config by definition. Everything else folds into the comprehensive pair.
//
// Re-bless goldens with GOLDEN_UPDATE=1, then read the diff carefully
// before committing — a re-bless that quietly drops a chain is a regression.

func testPorts() EnvoyPorts {
	return EnvoyPorts{EgressPort: 10000, TCPPortBase: 15000, UDPPortBase: 16000, HealthPort: 10001}
}

// comprehensiveRules is the all-encompassing egress-rules sample shared by the
// `comprehensive` (als off) and `comprehensive_mtls` (als on) cases — the same
// rule set generated under both access-log modes is a full-matrix on/off
// differential for the OTel sink across every listener type.
//
// It maxes out the SHARED egress listener — TLS SNI chains (per-SNI cert + deny
// default), the plaintext http catch-all (+ http_dfp), prefix_ranges raw_buffer
// chains for opaque CIDR AND L7/TLS-to-CIDR, all under one use_original_dst
// (single-IP opaque tcp/ssh no longer rides this — it gets its OWN dedicated
// STATIC listener, since eBPF connect4 NAT defeats prefix_ranges recovery) —
// alongside the dedicated tcp/ssh/udp listeners, the port_range fan-out, the
// QUIC sibling listener, and the deny floor. It deliberately interleaves: FQDN
// https (gets a QUIC sibling) next to IP and CIDR https (TCP-only, no QUIC — the
// carve-out pinned in one place); the same CIDR on two ports under two protos
// (10.0.0.0/24 http:8080 + tcp:5432); the same IP on two ports under two protos
// (10.0.0.5 https:8443 on the shared TLS prefix_ranges chain + tcp:8080 on its
// own dedicated STATIC listener); plaintext http to a bare IP literal
// (192.168.1.1/.2 → STATIC pin + IP-keyed Host vhost, multi-port + path rules);
// apex dedup (mintlify.com + .mintlify.com);
// ws/wss absorbed into a base rule AND standalone ws/wss promotion AND wildcard
// ws/wss; path routing on both plaintext and TLS chains; and an unsupported
// proto (ftp) skipped amid the rest.
const comprehensiveRules = `
rules:
  # --- http: exact, wildcard (httpDFPActive), path rules ---
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
      # method gate, multi-method allow (safe_regex): only GET/HEAD on /v1
      - path: /v1
        action: allow
        methods: [GET, HEAD]
      # method gate, single-method deny (exact): block DELETE on /v1/internal
      - path: /v1/internal
        action: deny
        methods: [DELETE]
  # --- https: exact FQDN (QUIC sibling), apex dedup, wildcard (httpsDFPActive), path rules ---
  - dst: api.anthropic.com
    proto: https
  - dst: mintlify.com
    proto: https
  - dst: .mintlify.com
    proto: https
  - dst: api.github.com
    proto: https
    port: 443
    path_default: allow
    path_rules:
      - path: /repos/schmitthub/clawker/
        action: allow
      # method gate, single-method allow (exact): only GET on this prefix
      - path: /repos/envoyproxy/
        action: allow
        methods: [GET]
      # method gate, multi-method deny (safe_regex): block writes on /repos/,
      # reads fall through to path_default: allow
      - path: /repos/
        action: deny
        methods: [POST, PUT, PATCH, DELETE]
      # regex path rule (opt-in ~): exact alternation, end-anchored by the
      # full-string safe_regex — /users/clawker[/] and /users/anthropic[/]
      # exactly, never /users/clawker-evil (the open-prefix bypass this guards)
      - path: ~/users/(clawker|anthropic)/?
        action: allow
      # regex deny: block the settings subtree exactly via a safe_regex deny route
      - path: ~/settings(/.*)?
        action: deny
  - dst: raw.githubusercontent.com
    proto: https
    port: 443
    path_default: deny
    path_rules:
      - path: /anchore/syft/main/
        action: allow
      - path: /schmitthub/clawker/
        action: allow
      - path: /envoyproxy/
        action: allow
  # --- https self-signed: FQDN (QUIC) + IP (no QUIC), insecure_skip_tls_verify ---
  - dst: local.dev
    proto: https
    insecure_skip_tls_verify: true
  - dst: 10.0.0.5
    proto: https
    port: 8443
    insecure_skip_tls_verify: true
  # --- L7/TLS to CIDR (TCP-only, no QUIC): http + https ---
  - dst: 172.16.0.0/16
    proto: https
  - dst: 10.0.0.0/24
    proto: http
    port: 8080
    path_default: deny
    path_rules:
      - path: /v1
        action: allow
      - path: /v1/internal
        action: deny
  # --- http to bare IP literal: STATIC pin + IP-keyed Host vhost, multi-port, path rules on an IP vhost ---
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
  # --- websocket: standalone promote, absorb into base, wildcard, CIDR ---
  - dst: realtime.io
    proto: ws
    port: 80
  - dst: stream.anthropic.com
    proto: wss
  - dst: .chat.example.com
    proto: ws
  - dst: .stream.example.com
    proto: wss
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
  - dst: 10.20.0.0/24
    proto: ws
    port: 80
  - dst: 10.10.0.0/16
    proto: wss
    insecure_skip_tls_verify: true
  # --- opaque tcp/ssh: dedicated FQDN listeners + port_range fan-out ---
  - dst: db.example.com
    proto: tcp
    port: 5432
  - dst: github.com
    proto: ssh
    port: 22
  - dst: gitlab.com
    proto: ssh
    port: 22
  - dst: cluster.example.com
    proto: tcp
    port: "9000-9002"
  # Overlap-coalesce guard: a single port inside the range above for the same
  # dst:proto. Each opaque rule fans into one dedicated listener PER in-range
  # port, so without rule-layer coalescing this would emit tcp_cluster.example.com_9001
  # twice and fail generation. NormalizeAndDedup must merge it back into 9000-9002
  # BEFORE generation — so this config still produces exactly the 9000-9002 fan-out
  # (the golden is unchanged by this line; its job is to prove no duplicate listener).
  - dst: cluster.example.com
    proto: tcp
    port: "9001"
  # --- first-class opaque deny: explicit per-port deny chain → deny_cluster blackhole ---
  # allow range with a deny single carved out of the MIDDLE: the deny port gets its
  # own dedicated deny listener (tcp_carve.example.com_7001 → deny_cluster, access
  # log action=denied), and the allow range splits around it into 7000 + 7002.
  - dst: carve.example.com
    proto: tcp
    port: "7000-7002"
  - dst: carve.example.com
    proto: tcp
    port: "7001"
    action: deny
  # standalone opaque ssh deny (no overlapping allow) — still an EXPLICIT deny chain
  # on its own dedicated listener, never a fall-through to the egress floor.
  - dst: blocked.example.com
    proto: ssh
    port: 2222
    action: deny
  # standalone raw-udp deny → dedicated udp_proxy listener routing to deny_cluster.
  - dst: blocked-udp.example.com
    proto: udp
    port: 9999
    action: deny
  # opaque tcp deny to a CIDR → explicit prefix_ranges deny chain on the shared
  # egress listener (raw_buffer + prefix_ranges + destination_port → deny_cluster),
  # distinct from the unmatched deny floor.
  - dst: 172.31.0.0/24
    proto: tcp
    port: 9000
    action: deny
  # --- opaque tcp/ssh: single-IP → dedicated STATIC listener; CIDR → shared egress prefix_ranges (ORIGINAL_DST) ---
  - dst: 10.0.0.5
    proto: tcp
    port: 8080
  - dst: 10.0.0.0/24
    proto: tcp
    port: 5432
  - dst: 203.0.113.7
    proto: ssh
    port: 22
  # single-IP opaque tcp + port_range: fans into one dedicated STATIC listener per
  # in-range port (mirrors the FQDN port_range above), each with a seeded eBPF route.
  - dst: 198.51.100.9
    proto: tcp
    port: "9100-9101"
  # --- raw udp: dedicated FQDN listener + dedicated single-IP listener ---
  - dst: relay.example.com
    proto: udp
    port: 3478
  - dst: 192.168.1.9
    proto: udp
    port: 3478
  # --- unsupported proto: skipped with a warning, rest still generates ---
  - dst: legacy.example.com
    proto: ftp
    port: 21
`

func TestGenerateEnvoyConfig(t *testing.T) {
	cases := []struct {
		name  string    // golden: testdata/envoy/<name>.envoy.golden
		rules string    // real egress-rules YAML, parsed via storage.New
		als   ALSConfig // generation-side access-log config (not part of the rules sample)
		// wantErrContains, when set, asserts GenerateEnvoyConfig FAILS with an error
		// containing this substring (the "control" for a fail-closed case) and skips
		// the golden compare — no config is produced.
		wantErrContains string
	}{
		{
			// The all-encompassing interaction golden (als off → stdout access log
			// only, no OTel cluster/sink). See comprehensiveRules.
			name:  "comprehensive",
			rules: comprehensiveRules,
		},
		{
			// Same rules as `comprehensive` with als.MTLS on: the OTel cluster
			// (otel_collector_als) + open_telemetry access-log sink appear on EVERY
			// listener type (http HCM, https HCM, tcp/ssh/udp tcp_proxy). The diff vs
			// `comprehensive` is exactly the OTel additions — a full-matrix on/off
			// differential for the access-log gate across the whole feature set.
			name:  "comprehensive_mtls",
			rules: comprehensiveRules,
			als:   ALSConfig{Port: 4319, MTLS: true},
		},
		{
			name: "http_exact_only", // exact-only → no DFP filter/cluster (the httpDFPActive=false shape)
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
			name: "https_exact_only", // exact-only https + non-default port (the httpsDFPActive=false shape)
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
			name: "ssh", // opaque-only: dedicated listener → tcp_proxy → pinned cluster, NO shared egress listener, NO deny floor
			rules: `
rules:
  - dst: github.com
    proto: ssh
    port: 22
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
    port: "9000-10001"
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
    port: "1-60000"
`,
			wantErrContains: "overflow past port 65535",
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
		{
			// Span-aware collision: a tcp port_range that OVERLAPS an ssh single port
			// on the same host. tcp and ssh share the tcp_<dst>_<port> listener family
			// (and the eBPF route_map keys host:port with no proto), so 4500 ∈
			// [4242,5000] is a genuine collision — must fail closed, not slip past a
			// SinglePort() collapse to the proto default. No golden.
			name: "proto_collision_range_overlaps_single",
			rules: `
rules:
  - dst: collide.example.com
    proto: tcp
    port: "4242-5000"
  - dst: collide.example.com
    proto: ssh
    port: 4500
`,
			wantErrContains: "claimed by multiple protos",
		},
		{
			// An all-single allow/deny clash on one opaque (dst, proto) port: no range
			// to carve, so it is a contradictory config — fail closed rather than
			// silently collapse to deny or trip the order-dependent addChain backstop.
			// (A range-involved clash like allow 4242-4243 + deny 4242 is NOT here — it
			// carves cleanly, deny wins, and generation succeeds.) No golden.
			name: "opaque_single_port_allow_deny_conflict",
			rules: `
rules:
  - dst: collide.example.com
    proto: tcp
    port: 4242
  - dst: collide.example.com
    proto: tcp
    port: 4242
    action: deny
`,
			wantErrContains: "no range to carve",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := storage.New[EgressRulesFile](tc.rules)
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
