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
