package firewall

import (
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestTCPMappings(t *testing.T) {
	t.Parallel()

	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001}

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := TCPMappings(tt.rules, ports)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateEnvoyConfig_TCPListeners(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
	}
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Contains(t, string(yamlBytes), "tls_egress")
	assert.Contains(t, string(yamlBytes), "tcp_github_com_22")
}

func TestHTTPMappings(t *testing.T) {
	t.Parallel()

	httpPort := 10080

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
			name: "HTTP rule on port 80",
			rules: []config.EgressRule{
				{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "http", DstPort: 80, EnvoyPort: 10080},
			},
		},
		{
			name: "HTTP rule on non-standard port",
			rules: []config.EgressRule{
				{Dst: "api.example.com", Proto: "http", Port: 8080, Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "http", DstPort: 8080, EnvoyPort: 10080},
			},
		},
		{
			name: "multiple HTTP domains on same port — deduplicated",
			rules: []config.EgressRule{
				{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
				{Dst: "httpbin.org", Proto: "http", Port: 80, Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "http", DstPort: 80, EnvoyPort: 10080},
			},
		},
		{
			name: "HTTP rules on different ports — separate entries",
			rules: []config.EgressRule{
				{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
				{Dst: "api.internal", Proto: "http", Port: 8080, Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "http", DstPort: 80, EnvoyPort: 10080},
				{Dst: "http", DstPort: 8080, EnvoyPort: 10080},
			},
		},
		{
			name: "deny HTTP rules are excluded",
			rules: []config.EgressRule{
				{Dst: "evil.com", Proto: "http", Port: 80, Action: "deny"},
			},
			expected: nil,
		},
		{
			name: "HTTP rule without port is skipped",
			rules: []config.EgressRule{
				{Dst: "example.com", Proto: "http", Port: 0, Action: "allow"},
			},
			expected: nil,
		},
		{
			name: "mixed TLS, TCP, and HTTP — only HTTP produces HTTP mappings",
			rules: []config.EgressRule{
				{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
				{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
				{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
			},
			expected: []TCPMapping{
				{Dst: "http", DstPort: 80, EnvoyPort: 10080},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := HTTPMappings(tt.rules, httpPort)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateEnvoyConfig_HTTPListener(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	out := string(yamlBytes)
	assert.Contains(t, out, "http_egress")
	assert.Contains(t, out, "http_egress_routes")
	assert.Contains(t, out, "example.com")
	assert.Contains(t, out, "deny_all")
	assert.Contains(t, out, "dynamic_forward_proxy_cluster")
	// Should NOT have a TLS listener when only HTTP rules exist.
	assert.NotContains(t, out, "tls_egress")
}

func TestGenerateEnvoyConfig_MixedHTTPAndTLS(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	out := string(yamlBytes)
	// Both listeners should be present.
	assert.Contains(t, out, "tls_egress")
	assert.Contains(t, out, "http_egress")
}

func TestGenerateEnvoyConfig_TLSClusterAutoConfig(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	out := string(yamlBytes)
	// Per-domain TLS cluster with isolated connection pool.
	assert.Contains(t, out, "dfp_tls_api_anthropic_com")
	assert.Contains(t, out, "dfp_dns_api_anthropic_com")
	assert.Contains(t, out, "envoy.extensions.upstreams.http.v3.HttpProtocolOptions")
	assert.Contains(t, out, "auto_sni: true")
	assert.Contains(t, out, "auto_san_validation: true")
	assert.Contains(t, out, "auto_config")
	assert.Contains(t, out, "http2_protocol_options: {}")
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
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

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
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	out := string(yamlBytes)
	assert.Contains(t, out, "tls_egress")
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

	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}
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

	result := normalizeAndDedup(rules)
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

	result := normalizeAndDedup(rules)
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

	result := normalizeAndDedup(rules)
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
	assert.Equal(t, []string{"api.anthropic.com"}, domains)
}

func TestHTTPDomains_WildcardDomain(t *testing.T) {
	t.Parallel()

	domains := httpDomains(".datadoghq.com", nil)
	assert.Equal(t, []string{"*.datadoghq.com", "datadoghq.com"}, domains)
}

func TestHTTPDomains_WildcardWithExactSibling(t *testing.T) {
	t.Parallel()

	exact := map[string]bool{"claude.ai": true}
	domains := httpDomains(".claude.ai", exact)
	assert.Equal(t, []string{"*.claude.ai"}, domains, "apex should be omitted when exact rule handles it")
}

func TestGenerateEnvoyConfig_WildcardDomain(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: ".datadoghq.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

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
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	out := string(yamlBytes)

	// Per-domain TLS cluster must exist with upstream TLS context for re-encryption.
	assert.Contains(t, out, "dfp_tls_api_anthropic_com",
		"per-domain TLS cluster must be present for upstream re-encryption after MITM termination")
	assert.Contains(t, out, "UpstreamTlsContext",
		"TLS cluster must have UpstreamTlsContext for upstream re-encryption")
	assert.Contains(t, out, "ca-certificates.crt",
		"TLS cluster must validate upstream certificates against system CA bundle")

	// The plaintext cluster must also exist (for HTTP routes).
	assert.Contains(t, out, "dynamic_forward_proxy_cluster")

	// Per-domain TLS cluster must have its own DNS cache for connection isolation.
	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	clusters := sr["clusters"].([]any)

	var foundTLSCluster bool
	for _, c := range clusters {
		cl := c.(map[string]any)
		if cl["name"] == "dfp_tls_api_anthropic_com" {
			foundTLSCluster = true
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
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

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
		if lis["name"] != "tls_egress" {
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
							assert.Truef(t, strings.HasPrefix(clusterName, "dfp_tls_"),
								"TLS filter chain routes must use a per-domain TLS cluster, got %q", clusterName)
						}
					}
				}
			}
		}
	}
	require.True(t, foundListener, "tls_egress listener must be present in generated config")
	require.True(t, checkedRoutes, "at least one TLS route must be inspected")
}

// TestGenerateEnvoyConfig_PerDomainClusterIsolation verifies that domains sharing
// the same IP get separate DFP clusters with isolated DNS caches, preventing HTTP/2
// connection pool reuse that causes SAN validation failures.
func TestGenerateEnvoyConfig_PerDomainClusterIsolation(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "mcp-proxy.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	clusters := sr["clusters"].([]any)

	// Each domain must have its own TLS cluster with its own DNS cache.
	clusterNames := make(map[string]string) // cluster name → dns cache name
	for _, c := range clusters {
		cl := c.(map[string]any)
		name := cl["name"].(string)
		if !strings.HasPrefix(name, "dfp_tls_") {
			continue
		}
		ct := cl["cluster_type"].(map[string]any)
		tc := ct["typed_config"].(map[string]any)
		dc := tc["dns_cache_config"].(map[string]any)
		clusterNames[name] = dc["name"].(string)
	}

	assert.Contains(t, clusterNames, "dfp_tls_api_anthropic_com")
	assert.Contains(t, clusterNames, "dfp_tls_mcp_proxy_anthropic_com")
	assert.Equal(t, "dfp_dns_api_anthropic_com", clusterNames["dfp_tls_api_anthropic_com"])
	assert.Equal(t, "dfp_dns_mcp_proxy_anthropic_com", clusterNames["dfp_tls_mcp_proxy_anthropic_com"])

	// Each filter chain must route to its own domain-specific cluster.
	listeners := sr["listeners"].([]any)
	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "tls_egress" {
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

			expectedCluster := "dfp_tls_" + sanitizeName(domain)
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
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	listeners := sr["listeners"].([]any)

	foundListener := false
	checkedRoutes := false
	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "http_egress" {
			continue
		}
		foundListener = true
		chains := lis["filter_chains"].([]any)
		for _, fc := range chains {
			chain := fc.(map[string]any)
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
							assert.Equal(t, "dynamic_forward_proxy_cluster", routeTarget["cluster"],
								"HTTP listener routes must use the plaintext cluster (no upstream TLS)")
						}
					}
				}
			}
		}
	}
	require.True(t, foundListener, "http_egress listener must be present in generated config")
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
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	listeners := sr["listeners"].([]any)

	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "http_egress" {
			continue
		}
		chains := lis["filter_chains"].([]any)
		for _, fc := range chains {
			chain := fc.(map[string]any)
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

func TestGenerateEnvoyConfig_TLSPortEnforcement(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "custom.example.com", Proto: "tls", Port: 8443, Action: "allow"},
	}
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	listeners := sr["listeners"].([]any)

	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "tls_egress" {
			continue
		}
		chains := lis["filter_chains"].([]any)
		for _, fc := range chains {
			chain := fc.(map[string]any)
			if chain["transport_socket"] == nil {
				continue
			}
			filters := chain["filters"].([]any)
			for _, f := range filters {
				filter := f.(map[string]any)
				tc := filter["typed_config"].(map[string]any)

				httpFilters := tc["http_filters"].([]any)
				require.GreaterOrEqual(t, len(httpFilters), 3,
					"TLS filter chain must have set_filter_state + DFP + router")

				sfs := httpFilters[0].(map[string]any)
				assert.Equal(t, "envoy.filters.http.set_filter_state", sfs["name"],
					"first HTTP filter must be set_filter_state for port enforcement")

				dfp := httpFilters[1].(map[string]any)
				assert.Equal(t, "envoy.filters.http.dynamic_forward_proxy", dfp["name"])
				dfpTC := dfp["typed_config"].(map[string]any)
				assert.Equal(t, true, dfpTC["allow_dynamic_host_from_filter_state"],
					"DFP must enable filter state so it reads the pinned port")
			}
		}
	}
}

func TestGenerateEnvoyConfig_TLSPortEnforcementValues(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "custom.example.com", Proto: "tls", Port: 8443, Action: "allow"},
	}
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	out := string(yamlBytes)
	assert.Contains(t, out, "inline_string: \"443\"")
	assert.Contains(t, out, "inline_string: \"8443\"")
	assert.Contains(t, out, "envoy.upstream.dynamic_port")
	assert.Contains(t, out, "envoy.filters.http.set_filter_state")
}

func TestGenerateEnvoyConfig_HTTPListenerNoDFPFilterState(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{TLSPort: 10000, TCPPortBase: 10001, HTTPPort: 10080}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	sr := cfg["static_resources"].(map[string]any)
	listeners := sr["listeners"].([]any)

	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "http_egress" {
			continue
		}
		chains := lis["filter_chains"].([]any)
		for _, fc := range chains {
			chain := fc.(map[string]any)
			filters := chain["filters"].([]any)
			for _, f := range filters {
				filter := f.(map[string]any)
				tc := filter["typed_config"].(map[string]any)
				httpFilters := tc["http_filters"].([]any)

				assert.Len(t, httpFilters, 2, "HTTP listener should have DFP + router only")
				dfp := httpFilters[0].(map[string]any)
				dfpTC := dfp["typed_config"].(map[string]any)
				assert.Nil(t, dfpTC["allow_dynamic_host_from_filter_state"],
					"HTTP listener DFP should not enable filter state override")
			}
		}
	}
}
