package firewall

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Contains(t, out, "port_value: 443")
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
			name: "TLS passthrough has access_log",
			rules: []config.EgressRule{
				{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
			},
		},
		{
			name: "TLS MITM has access_log",
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

func TestNormalizeAndDedup_WildcardSubsumesExact(t *testing.T) {
	t.Parallel()

	// Wildcard and exact for the same domain — wildcard should win.
	rules := []config.EgressRule{
		{Dst: "claude.ai", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: ".claude.ai", Proto: "tls", Port: 443, Action: "allow"},
	}

	result := normalizeAndDedup(rules)
	assert.Len(t, result, 1)
	assert.Equal(t, ".claude.ai", result[0].Dst, "wildcard should supersede exact")
}

func TestNormalizeAndDedup_WildcardFirstKept(t *testing.T) {
	t.Parallel()

	// Wildcard comes first — exact duplicate should be dropped.
	rules := []config.EgressRule{
		{Dst: ".claude.ai", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "claude.ai", Proto: "tls", Port: 443, Action: "allow"},
	}

	result := normalizeAndDedup(rules)
	assert.Len(t, result, 1)
	assert.Equal(t, ".claude.ai", result[0].Dst, "wildcard should be preserved")
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

	names := serverNames("api.anthropic.com")
	assert.Equal(t, []string{"api.anthropic.com"}, names)
}

func TestServerNames_WildcardDomain(t *testing.T) {
	t.Parallel()

	names := serverNames(".datadoghq.com")
	assert.Equal(t, []string{".datadoghq.com", "datadoghq.com"}, names)
}

func TestHTTPDomains_ExactDomain(t *testing.T) {
	t.Parallel()

	domains := httpDomains("api.anthropic.com")
	assert.Equal(t, []string{"api.anthropic.com"}, domains)
}

func TestHTTPDomains_WildcardDomain(t *testing.T) {
	t.Parallel()

	domains := httpDomains(".datadoghq.com")
	assert.Equal(t, []string{"*.datadoghq.com", "datadoghq.com"}, domains)
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
