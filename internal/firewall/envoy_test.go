package firewall

import (
	"fmt"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	clawkerebpf "github.com/schmitthub/clawker/internal/ebpf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestTCPMappings(t *testing.T) {
	t.Parallel()

	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	tests := []struct {
		name     string
		rules    []config.EgressRule
		expected []TCPMapping
	}{
		{
			name:     "no rules",
			rules:    nil,
			expected: nil,
		},
		{
			name: "TLS rules are excluded",
			rules: []config.EgressRule{
				{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
			},
			expected: nil,
		},
		{
			name: "SSH rule",
			rules: []config.EgressRule{
				{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "github.com", DstPort: 22, EnvoyPort: 10001},
			},
		},
		{
			name: "TCP rule",
			rules: []config.EgressRule{
				{Dst: "db.example.com", Proto: "tcp", Port: 5432, Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "db.example.com", DstPort: 5432, EnvoyPort: 10001},
			},
		},
		{
			name: "multiple TCP rules get sequential ports",
			rules: []config.EgressRule{
				{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
				{Dst: "db.example.com", Proto: "tcp", Port: 5432, Action: "allow"},
				{Dst: "api.example.com", Proto: "tcp", Port: 8080, Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "github.com", DstPort: 22, EnvoyPort: 10001},
				{Dst: "db.example.com", DstPort: 5432, EnvoyPort: 10002},
				{Dst: "api.example.com", DstPort: 8080, EnvoyPort: 10003},
			},
		},
		{
			name: "mixed TLS and TCP — only TCP produces mappings",
			rules: []config.EgressRule{
				{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
				{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
				{Dst: "registry.npmjs.org", Proto: "tls", Port: 443, Action: "allow"},
				{Dst: "db.example.com", Proto: "tcp", Port: 5432, Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "github.com", DstPort: 22, EnvoyPort: 10001},
				{Dst: "db.example.com", DstPort: 5432, EnvoyPort: 10002},
			},
		},
		{
			name: "deny rules are excluded",
			rules: []config.EgressRule{
				{Dst: "evil.com", Proto: "tcp", Port: 8080, Action: "deny"},
				{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "github.com", DstPort: 22, EnvoyPort: 10001},
			},
		},
		{
			name: "IP/CIDR destinations are excluded",
			rules: []config.EgressRule{
				{Dst: "192.168.1.0/24", Proto: "tcp", Port: 8080, Action: "allow"},
				{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "github.com", DstPort: 22, EnvoyPort: 10001},
			},
		},
		{
			name: "SSH with no port defaults to 22",
			rules: []config.EgressRule{
				{Dst: "github.com", Proto: "ssh", Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "github.com", DstPort: 22, EnvoyPort: 10001},
			},
		},
		{
			// Wildcard rules use a leading-dot convention. TCPMapping.Dst
			// must strip it so DomainHash(Dst) matches the CoreDNS zone
			// hash that the dnsbpf plugin writes into dns_cache for
			// resolved subdomains (the Corefile generator already strips
			// the leading dot via normalizeDomain).
			name: "wildcard SSH rule normalized to canonical domain",
			rules: []config.EgressRule{
				{Dst: ".github.com", Proto: "ssh", Port: 22, Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "github.com", DstPort: 22, EnvoyPort: 10001},
			},
		},
		{
			name: "wildcard TCP rule normalized to canonical domain",
			rules: []config.EgressRule{
				{Dst: ".db.example.com", Proto: "tcp", Port: 5432, Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "db.example.com", DstPort: 5432, EnvoyPort: 10001},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := TCPMappings(tt.rules, ports)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestTCPMappings_WildcardDomainHashMatchesDnsbpfZone is a regression test
// for a bug where wildcard TCP/SSH rules were never enforced at runtime
// because the route_map lookup missed.
//
// The writer side (Manager.syncRoutes) hashed TCPMapping.Dst verbatim
// (including the leading dot), while the reader side (dnsbpf plugin) hashed
// the normalized CoreDNS zone name (with the leading dot already stripped
// by normalizeDomain in the Corefile generator). DomainHash(".example.com")
// ≠ DomainHash("example.com"), so a rule like `.example.com` ssh 22 would
// generate an Envoy TCP listener but the BPF route_map entry was keyed on
// a hash that dnsbpf would never produce — silent bypass.
//
// This test asserts the canonical invariant: for any wildcard rule, the
// hash derived from TCPMapping.Dst must equal the hash of the corresponding
// normalized Corefile zone.
func TestTCPMappings_WildcardDomainHashMatchesDnsbpfZone(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: ".example.com", Proto: "ssh", Port: 22, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	mappings := TCPMappings(rules, ports)
	require.Len(t, mappings, 1)

	// What dnsbpf writes into dns_cache for the wildcard zone (the Corefile
	// zone is normalizeDomain(".example.com") = "example.com").
	wantHash := clawkerebpf.DomainHash(normalizeDomain(".example.com"))

	// What Manager.syncRoutes writes into route_map.
	gotHash := clawkerebpf.DomainHash(mappings[0].Dst)

	assert.Equal(t, wantHash, gotHash,
		"route_map hash for wildcard rule must match the dnsbpf dns_cache hash "+
			"for the corresponding CoreDNS zone, otherwise wildcard TCP/SSH rules "+
			"silently fail at runtime")
}

func TestGenerateEnvoyConfig_TCPListeners(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Contains(t, string(yamlBytes), "name: egress")
	assert.Contains(t, string(yamlBytes), "tcp_github_com_22")
}

func TestGenerateEnvoyConfig_HTTPListener(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	out := string(yamlBytes)
	// HTTP rules are served via the egress listener's raw_buffer filter chain.
	assert.Contains(t, out, "name: egress")
	assert.Contains(t, out, "raw_buffer")
	assert.Contains(t, out, "http_egress")
	assert.Contains(t, out, "http_egress_routes")
	assert.Contains(t, out, "example.com")
	assert.Contains(t, out, "deny_all")
	assert.Contains(t, out, "http_example_com_80")
}

func TestGenerateEnvoyConfig_MixedHTTPAndTLS(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	out := string(yamlBytes)
	// Single egress listener with both TLS filter chains and raw_buffer HTTP filter chain.
	assert.Contains(t, out, "name: egress")
	assert.Contains(t, out, "tls_api_anthropic_com_443")
	assert.Contains(t, out, "raw_buffer")
	assert.Contains(t, out, "http_egress")
}

func TestGenerateEnvoyConfig_TLSClusterAutoConfig(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	out := string(yamlBytes)
	// Per-domain LOGICAL_DNS cluster with upstream TLS re-encryption.
	assert.Contains(t, out, "tls_api_anthropic_com_443")
	assert.Contains(t, out, "type: LOGICAL_DNS")
	assert.Contains(t, out, "envoy.extensions.upstreams.http.v3.HttpProtocolOptions")
	assert.Contains(t, out, "auto_sni: true")
	assert.Contains(t, out, "auto_san_validation: true")
	assert.Contains(t, out, "auto_config")
	assert.Contains(t, out, "http2_protocol_options: {}")
	// WebSocket upgrade uses custom filter chain to force HTTP/1.1 upstream ALPN.
	assert.Contains(t, out, "upgrade_type: websocket")
	assert.Contains(t, out, "envoy.network.application_protocols")
	// No DFP filter — LOGICAL_DNS clusters don't need it.
	assert.NotContains(t, out, "envoy.filters.http.dynamic_forward_proxy")
}

func TestGenerateEnvoyConfig_HTTPWithPathRules(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{
			Dst:   "api.example.com",
			Proto: "http",
			Port:  80,
			PathRules: []config.PathRule{
				{Path: "/api/v1", Action: "allow"},
				{Path: "/admin", Action: "deny"},
			},
			PathDefault: "deny",
			Action:      "allow",
		},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	out := string(yamlBytes)
	assert.Contains(t, out, "http_egress")
	assert.Contains(t, out, "api.example.com")
	assert.Contains(t, out, "/api/v1")
	assert.Contains(t, out, "/admin")
	assert.Contains(t, out, "Blocked by clawker firewall")
}

func TestGenerateEnvoyConfig_ZeroPortTLSDefaults443(t *testing.T) {
	t.Parallel()

	// Legacy store files may contain TLS rules with port:0 written before
	// normalizeRule defaulted TLS to 443. GenerateEnvoyConfig must handle this
	// defensively — Envoy rejects port_value:0 with a validation error.
	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 0, Action: "allow"},
		{Dst: "github.com", Proto: "tls", Port: 0, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	out := string(yamlBytes)
	assert.Contains(t, out, "egress")
	assert.Contains(t, out, "api.anthropic.com")
	assert.Contains(t, out, "github.com")
	// port_value:0 must never appear — Envoy requires (0, 65535].
	assert.NotContains(t, out, "port_value: 0")
	// Both rules get TLS filter chains with per-domain certs.
	assert.Contains(t, out, "api.anthropic.com-cert.pem")
	assert.Contains(t, out, "github.com-cert.pem")
}

func TestBuildHTTPAccessLog(t *testing.T) {
	t.Parallel()

	logs := buildHTTPAccessLog("http")
	require.Len(t, logs, 1)

	entry, ok := logs[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "envoy.access_loggers.stdout", entry["name"])

	tc, ok := entry["typed_config"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, tc["@type"], "StdoutAccessLog")

	lf, ok := tc["log_format"].(map[string]any)
	require.True(t, ok)
	jf, ok := lf["json_format"].(map[string]any)
	require.True(t, ok)

	// Common fields.
	assert.Equal(t, "http", jf["proto"])
	assert.Equal(t, "envoy", jf["source"])
	assert.Equal(t, "%REQUESTED_SERVER_NAME%", jf["domain"])
	assert.Equal(t, "%DURATION%", jf["duration_ms"])
	assert.Equal(t, "%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%", jf["client_ip"])

	// HTTP-specific fields present.
	assert.Equal(t, "%RESPONSE_CODE%", jf["response_code"])
	assert.Equal(t, "%REQ(:METHOD)%", jf["method"])
	assert.Equal(t, "%REQ(:PATH)%", jf["path"])
	assert.Equal(t, "%REQ(Host)%", jf["request_host"])
}

func TestBuildTCPAccessLog(t *testing.T) {
	t.Parallel()

	logs := buildTCPAccessLog("tls")
	require.Len(t, logs, 1)

	entry, ok := logs[0].(map[string]any)
	require.True(t, ok)

	tc, ok := entry["typed_config"].(map[string]any)
	require.True(t, ok)
	lf, ok := tc["log_format"].(map[string]any)
	require.True(t, ok)
	jf, ok := lf["json_format"].(map[string]any)
	require.True(t, ok)

	// Common fields present.
	assert.Equal(t, "tls", jf["proto"])
	assert.Equal(t, "%REQUESTED_SERVER_NAME%", jf["domain"])
	assert.Equal(t, "%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%", jf["client_ip"])

	// HTTP-specific fields absent.
	assert.Nil(t, jf["response_code"])
	assert.Nil(t, jf["method"])
	assert.Nil(t, jf["path"])
}

func TestBuildTCPAccessLog_DomainOverride(t *testing.T) {
	t.Parallel()

	logs := buildTCPAccessLog("tcp", "github.com")
	require.Len(t, logs, 1)

	entry, ok := logs[0].(map[string]any)
	require.True(t, ok)

	tc, ok := entry["typed_config"].(map[string]any)
	require.True(t, ok)
	lf, ok := tc["log_format"].(map[string]any)
	require.True(t, ok)
	jf, ok := lf["json_format"].(map[string]any)
	require.True(t, ok)

	// Static domain overrides the SNI placeholder for raw TCP listeners.
	assert.Equal(t, "github.com", jf["domain"])
	assert.Equal(t, "tcp", jf["proto"])
}

func TestGenerateEnvoyConfig_AccessLogPresent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		rules []config.EgressRule
	}{
		{
			name: "TLS has access_log",
			rules: []config.EgressRule{
				{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
			},
		},
		{
			name: "TLS with path rules has access_log",
			rules: []config.EgressRule{
				{
					Dst: "api.example.com", Proto: "tls", Port: 443, Action: "allow",
					PathRules:   []config.PathRule{{Path: "/v1", Action: "allow"}},
					PathDefault: "deny",
				},
			},
		},
		{
			name: "TCP/SSH has access_log",
			rules: []config.EgressRule{
				{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
			},
		},
		{
			name: "HTTP has access_log",
			rules: []config.EgressRule{
				{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
			},
		},
	}

	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			yamlBytes, _, err := GenerateEnvoyConfig(tt.rules, ports)
			require.NoError(t, err)
			out := string(yamlBytes)
			assert.Contains(t, out, "envoy.access_loggers.stdout")
			assert.Contains(t, out, "StdoutAccessLog")
			assert.Contains(t, out, "json_format")
		})
	}
}

func TestNormalizeAndDedup(t *testing.T) {
	t.Parallel()

	// Simulates a legacy store with port:0 and port:443 duplicates.
	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 0, Action: "allow"},
		{Dst: "github.com", Proto: "tls", Port: 0, Action: "allow"},
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "github.com", Proto: "tls", Port: 443, Action: "allow"},
	}

	result, _ := normalizeAndDedup(rules)
	assert.Len(t, result, 2)
	for _, r := range result {
		assert.Equal(t, 443, r.Port, "rule for %s should have port 443", r.Dst)
	}
}

func TestNormalizeAndDedup_WildcardAndExactCoexist(t *testing.T) {
	t.Parallel()

	// Wildcard and exact for the same domain — both kept as separate rules.
	rules := []config.EgressRule{
		{Dst: "claude.ai", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: ".claude.ai", Proto: "tls", Port: 443, Action: "allow"},
	}

	result, _ := normalizeAndDedup(rules)
	assert.Len(t, result, 2, "wildcard and exact are distinct rules")

	dsts := []string{result[0].Dst, result[1].Dst}
	assert.Contains(t, dsts, "claude.ai")
	assert.Contains(t, dsts, ".claude.ai")
}

func TestNormalizeAndDedup_ExactDuplicatesStillDeduped(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "claude.ai", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "claude.ai", Proto: "tls", Port: 443, Action: "allow"},
	}

	result, _ := normalizeAndDedup(rules)
	assert.Len(t, result, 1)
	assert.Equal(t, "claude.ai", result[0].Dst)
}

func TestIsWildcardDomain(t *testing.T) {
	t.Parallel()

	assert.True(t, isWildcardDomain(".datadoghq.com"))
	assert.True(t, isWildcardDomain(".example.com"))
	assert.False(t, isWildcardDomain("api.anthropic.com"))
	assert.False(t, isWildcardDomain("datadoghq.com"))
	assert.False(t, isWildcardDomain(""))
}

func TestNormalizeDomain_LeadingDot(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "datadoghq.com", normalizeDomain(".datadoghq.com"))
	assert.Equal(t, "example.com", normalizeDomain(".example.com."))
	assert.Equal(t, "api.anthropic.com", normalizeDomain("api.anthropic.com"))
	assert.Equal(t, "api.anthropic.com", normalizeDomain("api.anthropic.com."))
}

func TestServerNames_ExactDomain(t *testing.T) {
	t.Parallel()

	names := serverNames("api.anthropic.com", nil)
	assert.Equal(t, []string{"api.anthropic.com"}, names)
}

func TestServerNames_WildcardDomain(t *testing.T) {
	t.Parallel()

	names := serverNames(".datadoghq.com", nil)
	assert.Equal(t, []string{".datadoghq.com", "datadoghq.com"}, names)
}

func TestServerNames_WildcardWithExactSibling(t *testing.T) {
	t.Parallel()

	// When a separate exact rule exists, wildcard omits the apex.
	exact := map[string]bool{"claude.ai": true}
	names := serverNames(".claude.ai", exact)
	assert.Equal(t, []string{".claude.ai"}, names, "apex should be omitted when exact rule handles it")
}

func TestHTTPDomains_ExactDomain(t *testing.T) {
	t.Parallel()

	domains := httpDomains("api.anthropic.com", nil)
	assert.Equal(t, []string{"api.anthropic.com", "api.anthropic.com:*"}, domains)
}

func TestHTTPDomains_WildcardDomain(t *testing.T) {
	t.Parallel()

	domains := httpDomains(".datadoghq.com", nil)
	assert.Equal(t, []string{"*.datadoghq.com", "*.datadoghq.com:*", "datadoghq.com", "datadoghq.com:*"}, domains)
}

func TestHTTPDomains_WildcardWithExactSibling(t *testing.T) {
	t.Parallel()

	exact := map[string]bool{"claude.ai": true}
	domains := httpDomains(".claude.ai", exact)
	assert.Equal(t, []string{"*.claude.ai", "*.claude.ai:*"}, domains, "apex should be omitted when exact rule handles it")
}

func TestGenerateEnvoyConfig_WildcardDomain(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: ".datadoghq.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	out := string(yamlBytes)
	// Wildcard domain should have suffix match form in SNI.
	assert.Contains(t, out, ".datadoghq.com")
	assert.Contains(t, out, "datadoghq.com")
	// Exact domain should appear as-is.
	assert.Contains(t, out, "api.anthropic.com")
}

func TestGenerateEnvoyConfig_UpstreamTLSReEncryption(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	out := string(yamlBytes)

	// Per-domain LOGICAL_DNS cluster must exist with upstream TLS context for re-encryption.
	assert.Contains(t, out, "tls_api_anthropic_com_443",
		"per-domain TLS cluster must be present for upstream re-encryption after MITM termination")
	assert.Contains(t, out, "UpstreamTlsContext",
		"TLS cluster must have UpstreamTlsContext for upstream re-encryption")
	assert.Contains(t, out, "ca-certificates.crt",
		"TLS cluster must validate upstream certificates against system CA bundle")

	// Per-domain TLS cluster uses LOGICAL_DNS — upstream host is the domain itself,
	// not derived from the Host header (prevents confused deputy attacks).
	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	clusters := sr["clusters"].([]any)

	var foundTLSCluster bool
	for _, c := range clusters {
		cl := c.(map[string]any)
		if cl["name"] == "tls_api_anthropic_com_443" {
			foundTLSCluster = true
			assert.Equal(t, "LOGICAL_DNS", cl["type"],
				"TLS cluster must use LOGICAL_DNS for domain-pinned routing")
			ts := cl["transport_socket"].(map[string]any)
			assert.Equal(t, "envoy.transport_sockets.tls", ts["name"])
			tc := ts["typed_config"].(map[string]any)
			assert.Contains(t, tc["@type"], "UpstreamTlsContext")
		}
	}
	assert.True(t, foundTLSCluster, "per-domain TLS cluster must be defined")
}

func TestGenerateEnvoyConfig_TLSRoutesToTLSCluster(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	listeners := sr["listeners"].([]any)

	// Find the TLS listener and verify routes use the TLS cluster.
	foundListener := false
	checkedRoutes := false
	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "egress" {
			continue
		}
		foundListener = true
		chains := lis["filter_chains"].([]any)
		for _, fc := range chains {
			chain := fc.(map[string]any)
			// Skip the deny chain (no transport_socket).
			if chain["transport_socket"] == nil {
				continue
			}
			filters := chain["filters"].([]any)
			for _, f := range filters {
				filter := f.(map[string]any)
				tc := filter["typed_config"].(map[string]any)
				rc := tc["route_config"].(map[string]any)
				vhosts := rc["virtual_hosts"].([]any)
				for _, vh := range vhosts {
					vhost := vh.(map[string]any)
					routes := vhost["routes"].([]any)
					for _, r := range routes {
						route := r.(map[string]any)
						if routeTarget, ok := route["route"].(map[string]any); ok {
							checkedRoutes = true
							clusterName := routeTarget["cluster"].(string)
							assert.Truef(t, strings.HasPrefix(clusterName, "tls_"),
								"TLS filter chain routes must use a per-domain TLS cluster, got %q", clusterName)
						}
					}
				}
			}
		}
	}
	require.True(t, foundListener, "egress listener must be present in generated config")
	require.True(t, checkedRoutes, "at least one TLS route must be inspected")
}

// TestGenerateEnvoyConfig_PerDomainClusterIsolation verifies that domains sharing
// the same IP get separate LOGICAL_DNS clusters, preventing HTTP/2 connection pool
// reuse that causes SAN validation failures.
func TestGenerateEnvoyConfig_PerDomainClusterIsolation(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "mcp-proxy.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	clusters := sr["clusters"].([]any)

	// Each domain must have its own LOGICAL_DNS cluster.
	clusterNames := make(map[string]bool)
	for _, c := range clusters {
		cl := c.(map[string]any)
		name := cl["name"].(string)
		if !strings.HasPrefix(name, "tls_") {
			continue
		}
		assert.Equal(t, "LOGICAL_DNS", cl["type"],
			"TLS cluster %s must be LOGICAL_DNS", name)
		clusterNames[name] = true
	}

	assert.Contains(t, clusterNames, "tls_api_anthropic_com_443")
	assert.Contains(t, clusterNames, "tls_mcp_proxy_anthropic_com_443")

	// Each filter chain must route to its own domain-specific cluster.
	listeners := sr["listeners"].([]any)
	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "egress" {
			continue
		}
		chains := lis["filter_chains"].([]any)
		for _, fc := range chains {
			chain := fc.(map[string]any)
			fcm, ok := chain["filter_chain_match"].(map[string]any)
			if !ok {
				continue
			}
			sn, ok := fcm["server_names"].([]any)
			if !ok || len(sn) == 0 {
				continue
			}
			domain := sn[0].(string)
			filters := chain["filters"].([]any)
			hcm := filters[0].(map[string]any)
			tc := hcm["typed_config"].(map[string]any)
			rc := tc["route_config"].(map[string]any)
			vhosts := rc["virtual_hosts"].([]any)
			routes := vhosts[0].(map[string]any)["routes"].([]any)
			route := routes[0].(map[string]any)
			routeAction := route["route"].(map[string]any)
			cluster := routeAction["cluster"].(string)

			expectedCluster := fmt.Sprintf("tls_%s_443", sanitizeName(domain))
			assert.Equalf(t, expectedCluster, cluster,
				"filter chain for %s must route to its own per-domain cluster", domain)
		}
	}
}

func TestGenerateEnvoyConfig_HTTPRoutesToPlaintextCluster(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	listeners := sr["listeners"].([]any)

	// HTTP rules live as a raw_buffer filter chain inside the egress listener.
	foundEgress := false
	foundHTTPChain := false
	checkedRoutes := false
	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "egress" {
			continue
		}
		foundEgress = true
		chains := lis["filter_chains"].([]any)
		for _, fc := range chains {
			chain := fc.(map[string]any)
			match, _ := chain["filter_chain_match"].(map[string]any)
			if match["transport_protocol"] != "raw_buffer" {
				continue
			}
			foundHTTPChain = true
			filters := chain["filters"].([]any)
			for _, f := range filters {
				filter := f.(map[string]any)
				tc := filter["typed_config"].(map[string]any)
				rc := tc["route_config"].(map[string]any)
				vhosts := rc["virtual_hosts"].([]any)
				for _, vh := range vhosts {
					vhost := vh.(map[string]any)
					routes := vhost["routes"].([]any)
					for _, r := range routes {
						route := r.(map[string]any)
						if routeTarget, ok := route["route"].(map[string]any); ok {
							checkedRoutes = true
							clusterName := routeTarget["cluster"].(string)
							assert.Truef(t, strings.HasPrefix(clusterName, "http_"),
								"HTTP filter chain routes must use a per-domain HTTP cluster, got %q", clusterName)
						}
					}
				}
			}
		}
	}
	require.True(t, foundEgress, "egress listener must be present in generated config")
	require.True(t, foundHTTPChain, "raw_buffer filter chain must be present for HTTP rules")
	require.True(t, checkedRoutes, "at least one HTTP route must be inspected")
}

func TestVirtualHostName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		dst      string
		expected string
	}{
		{"api.anthropic.com", "api.anthropic.com"},
		{".datadoghq.com", "wildcard_datadoghq.com"},
		{".example.com", "wildcard_example.com"},
		{"example.com", "example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.dst, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, virtualHostName(tt.dst))
		})
	}
}

func TestGenerateEnvoyConfig_WildcardAndExactHTTPNoDuplicateVirtualHostNames(t *testing.T) {
	t.Parallel()

	// Both wildcard and exact rules for the same domain — virtual host names must be unique.
	rules := []config.EgressRule{
		{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
		{Dst: ".example.com", Proto: "http", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	listeners := sr["listeners"].([]any)

	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "egress" {
			continue
		}
		chains := lis["filter_chains"].([]any)
		for _, fc := range chains {
			chain := fc.(map[string]any)
			// Find the raw_buffer (HTTP) filter chain.
			fcm, _ := chain["filter_chain_match"].(map[string]any)
			if fcm["transport_protocol"] != "raw_buffer" {
				continue
			}
			filters := chain["filters"].([]any)
			for _, f := range filters {
				filter := f.(map[string]any)
				tc := filter["typed_config"].(map[string]any)
				rc := tc["route_config"].(map[string]any)
				vhosts := rc["virtual_hosts"].([]any)

				names := make(map[string]bool, len(vhosts))
				for _, vh := range vhosts {
					vhost := vh.(map[string]any)
					name := vhost["name"].(string)
					assert.False(t, names[name],
						"duplicate virtual host name %q in HTTP route_config", name)
					names[name] = true
				}
				assert.True(t, names["example.com"], "exact virtual host should be present")
				assert.True(t, names["wildcard_example.com"], "wildcard virtual host should be present")
			}
		}
	}
}

// TestGenerateEnvoyConfig_TLSClusterPortPinning verifies that each per-domain TLS
// cluster uses the port from the rule config as its endpoint port_value — not derived
// from the Host header. This is the security fix: LOGICAL_DNS endpoints have fixed
// ports, eliminating the confused deputy attack via Host header port manipulation.
func TestGenerateEnvoyConfig_TLSClusterPortPinning(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "custom.example.com", Proto: "tls", Port: 8443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	clusters := sr["clusters"].([]any)

	portByCluster := make(map[string]int)
	for _, c := range clusters {
		cl := c.(map[string]any)
		name := cl["name"].(string)
		if !strings.HasPrefix(name, "tls_") {
			continue
		}
		la := cl["load_assignment"].(map[string]any)
		eps := la["endpoints"].([]any)
		lbEps := eps[0].(map[string]any)["lb_endpoints"].([]any)
		ep := lbEps[0].(map[string]any)["endpoint"].(map[string]any)
		addr := ep["address"].(map[string]any)["socket_address"].(map[string]any)
		portByCluster[name] = addr["port_value"].(int)
	}

	assert.Equal(t, 443, portByCluster["tls_api_anthropic_com_443"],
		"cluster for api.anthropic.com must use port 443")
	assert.Equal(t, 8443, portByCluster["tls_custom_example_com_8443"],
		"cluster for custom.example.com must use port 8443")

	// No DFP port enforcement filter needed — ports are hardcoded in cluster endpoints.
	out := string(yamlBytes)
	assert.NotContains(t, out, "envoy.upstream.dynamic_port",
		"LOGICAL_DNS clusters don't need dynamic port filter state")
}

// TestGenerateEnvoyConfig_SimplifiedFilterChains verifies the new architecture
// has minimal http_filters: just the router. No DFP filter, no port enforcement.
func TestGenerateEnvoyConfig_SimplifiedFilterChains(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	listeners := sr["listeners"].([]any)

	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "egress" {
			continue
		}
		chains := lis["filter_chains"].([]any)
		for _, fc := range chains {
			chain := fc.(map[string]any)
			// Skip deny chain (uses tcp_proxy, no http_filters).
			fcm, _ := chain["filter_chain_match"].(map[string]any)
			if len(fcm) == 0 {
				continue
			}
			filters := chain["filters"].([]any)
			hcm := filters[0].(map[string]any)
			tc := hcm["typed_config"].(map[string]any)
			httpFilters := tc["http_filters"].([]any)

			// Both TLS and HTTP filter chains should have router only.
			assert.Len(t, httpFilters, 1,
				"filter chain should have router only (no DFP, no port enforcement)")
			router := httpFilters[0].(map[string]any)
			assert.Equal(t, "envoy.filters.http.router", router["name"])
		}
	}
}

func TestNormalizeAndDedup_MalformedDomains(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: ".", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "..", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "valid.com", Proto: "tls", Port: 443, Action: "allow"},
	}

	result, warnings := normalizeAndDedup(rules)
	assert.Len(t, result, 1, "malformed domains should be filtered out")
	assert.Equal(t, "valid.com", result[0].Dst)
	assert.Len(t, warnings, 2, "should warn about each dropped malformed domain")
}

func TestEnvoyPorts_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ports   EnvoyPorts
		wantErr bool
	}{
		{
			name:  "valid ports",
			ports: EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901},
		},
		{
			name:    "zero EgressPort",
			ports:   EnvoyPorts{EgressPort: 0, TCPPortBase: 10001, HealthPort: 18901},
			wantErr: true,
		},
		{
			name:    "port too large",
			ports:   EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 70000},
			wantErr: true,
		},
		{
			name:    "collision Egress and Health",
			ports:   EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 10000},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.ports.Validate()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Gap coverage: HTTP cluster structure (#9)
// ──────────────────────────────────────────────────────────────────────────────

func TestGenerateEnvoyConfig_HTTPClusterStructure(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "example.com", Proto: "http", Port: 8080, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	clusters := sr["clusters"].([]any)

	var found bool
	for _, c := range clusters {
		cl := c.(map[string]any)
		if cl["name"] != "http_example_com_8080" {
			continue
		}
		found = true
		assert.Equal(t, "LOGICAL_DNS", cl["type"])
		assert.Equal(t, "V4_ONLY", cl["dns_lookup_family"])
		// No transport_socket — plaintext upstream.
		assert.Nil(t, cl["transport_socket"],
			"HTTP cluster must NOT have transport_socket (plaintext upstream)")

		// Endpoint address and port.
		la := cl["load_assignment"].(map[string]any)
		eps := la["endpoints"].([]any)
		lbEps := eps[0].(map[string]any)["lb_endpoints"].([]any)
		ep := lbEps[0].(map[string]any)["endpoint"].(map[string]any)
		addr := ep["address"].(map[string]any)["socket_address"].(map[string]any)
		assert.Equal(t, "example.com", addr["address"])
		assert.Equal(t, 8080, addr["port_value"])
	}
	assert.True(t, found, "http_example_com_8080 cluster must exist")
}

// ──────────────────────────────────────────────────────────────────────────────
// Gap coverage: HTTP port 0 defaults to 80 (#10)
// ──────────────────────────────────────────────────────────────────────────────

func TestGenerateEnvoyConfig_ZeroPortHTTPDefaults80(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "example.com", Proto: "http", Port: 0, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	out := string(yamlBytes)
	// port_value:0 must never appear — Envoy requires (0, 65535].
	assert.NotContains(t, out, "port_value: 0")
	assert.Contains(t, out, "http_example_com_80")

	// Verify cluster endpoint uses port 80.
	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))
	sr := cfg["static_resources"].(map[string]any)
	clusters := sr["clusters"].([]any)

	for _, c := range clusters {
		cl := c.(map[string]any)
		if cl["name"] != "http_example_com_80" {
			continue
		}
		la := cl["load_assignment"].(map[string]any)
		eps := la["endpoints"].([]any)
		lbEps := eps[0].(map[string]any)["lb_endpoints"].([]any)
		ep := lbEps[0].(map[string]any)["endpoint"].(map[string]any)
		addr := ep["address"].(map[string]any)["socket_address"].(map[string]any)
		assert.Equal(t, 80, addr["port_value"])
		return
	}
	t.Fatal("http_example_com_80 cluster not found")
}

// ──────────────────────────────────────────────────────────────────────────────
// Gap coverage: WebSocket ALPN override structural test (#11)
// ──────────────────────────────────────────────────────────────────────────────

func TestGenerateEnvoyConfig_TLSWebSocketALPNOverride(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	listeners := sr["listeners"].([]any)

	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "egress" {
			continue
		}
		chains := lis["filter_chains"].([]any)
		for _, fc := range chains {
			chain := fc.(map[string]any)
			// Find TLS filter chains (have transport_socket).
			if chain["transport_socket"] == nil {
				continue
			}
			filters := chain["filters"].([]any)
			hcm := filters[0].(map[string]any)
			tc := hcm["typed_config"].(map[string]any)
			upgrades := tc["upgrade_configs"].([]any)

			require.Len(t, upgrades, 1, "TLS chain must have exactly one upgrade_configs entry")
			uc := upgrades[0].(map[string]any)
			assert.Equal(t, "websocket", uc["upgrade_type"])

			// Custom filter chain with ALPN override.
			ucFilters := uc["filters"].([]any)
			require.Len(t, ucFilters, 2, "WebSocket upgrade must have set_filter_state + router")

			// First filter: set_filter_state to override ALPN.
			sf := ucFilters[0].(map[string]any)
			assert.Equal(t, "envoy.filters.http.set_filter_state", sf["name"])
			sfTC := sf["typed_config"].(map[string]any)
			headers := sfTC["on_request_headers"].([]any)
			header := headers[0].(map[string]any)
			assert.Equal(t, "envoy.network.application_protocols", header["object_key"])
			fs := header["format_string"].(map[string]any)
			tfs := fs["text_format_source"].(map[string]any)
			assert.Equal(t, "http/1.1", tfs["inline_string"])

			// Second filter: router.
			router := ucFilters[1].(map[string]any)
			assert.Equal(t, "envoy.filters.http.router", router["name"])
			return
		}
	}
	t.Fatal("TLS filter chain with WebSocket upgrade not found")
}

// ──────────────────────────────────────────────────────────────────────────────
// Gap coverage: duplicate domain different ports (#12)
// ──────────────────────────────────────────────────────────────────────────────

func TestGenerateEnvoyConfig_SameDomainDifferentPorts(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "example.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "example.com", Proto: "tls", Port: 8443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))
	sr := cfg["static_resources"].(map[string]any)
	clusters := sr["clusters"].([]any)

	// Both port-specific clusters must exist.
	clusterNames := make(map[string]bool)
	portByCluster := make(map[string]int)
	for _, c := range clusters {
		cl := c.(map[string]any)
		name := cl["name"].(string)
		clusterNames[name] = true
		if !strings.HasPrefix(name, "tls_") {
			continue
		}
		la := cl["load_assignment"].(map[string]any)
		eps := la["endpoints"].([]any)
		lbEps := eps[0].(map[string]any)["lb_endpoints"].([]any)
		ep := lbEps[0].(map[string]any)["endpoint"].(map[string]any)
		addr := ep["address"].(map[string]any)["socket_address"].(map[string]any)
		portByCluster[name] = addr["port_value"].(int)
	}

	assert.True(t, clusterNames["tls_example_com_443"], "cluster for port 443 must exist")
	assert.True(t, clusterNames["tls_example_com_8443"], "cluster for port 8443 must exist")
	assert.Equal(t, 443, portByCluster["tls_example_com_443"])
	assert.Equal(t, 8443, portByCluster["tls_example_com_8443"])
}

func TestGenerateEnvoyConfig_SameDomainDifferentPortsHTTP(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
		{Dst: "example.com", Proto: "http", Port: 8080, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))
	sr := cfg["static_resources"].(map[string]any)
	clusters := sr["clusters"].([]any)

	clusterNames := make(map[string]bool)
	for _, c := range clusters {
		cl := c.(map[string]any)
		clusterNames[cl["name"].(string)] = true
	}

	assert.True(t, clusterNames["http_example_com_80"], "cluster for port 80 must exist")
	assert.True(t, clusterNames["http_example_com_8080"], "cluster for port 8080 must exist")
}

// ──────────────────────────────────────────────────────────────────────────────
// Gap coverage: tls_inspector listener filter presence (#13)
// ──────────────────────────────────────────────────────────────────────────────

func TestGenerateEnvoyConfig_TLSInspectorPresent(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	listeners := sr["listeners"].([]any)

	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "egress" {
			continue
		}
		lf := lis["listener_filters"].([]any)
		require.Len(t, lf, 1)
		filter := lf[0].(map[string]any)
		assert.Equal(t, "envoy.filters.listener.tls_inspector", filter["name"])
		return
	}
	t.Fatal("egress listener not found")
}

// ──────────────────────────────────────────────────────────────────────────────
// Gap coverage: deny chain must be last (#8)
// ──────────────────────────────────────────────────────────────────────────────

func TestGenerateEnvoyConfig_DenyChainIsLast(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	listeners := sr["listeners"].([]any)

	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "egress" {
			continue
		}
		chains := lis["filter_chains"].([]any)
		require.Greater(t, len(chains), 1, "egress listener must have multiple filter chains")

		// Last chain must be the deny chain: empty filter_chain_match + tcp_proxy → deny_cluster.
		last := chains[len(chains)-1].(map[string]any)
		fcm, _ := last["filter_chain_match"].(map[string]any)
		assert.Empty(t, fcm, "deny chain must have empty filter_chain_match (catch-all)")

		filters := last["filters"].([]any)
		tcpProxy := filters[0].(map[string]any)
		assert.Equal(t, "envoy.filters.network.tcp_proxy", tcpProxy["name"])
		tc := tcpProxy["typed_config"].(map[string]any)
		assert.Equal(t, "deny_cluster", tc["cluster"])
		return
	}
	t.Fatal("egress listener not found")
}
