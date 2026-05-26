package firewall

import (
	"fmt"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	clawkerebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
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
				{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
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
				{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
				{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
				{Dst: "registry.npmjs.org", Proto: "https", Port: 443, Action: "allow"},
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
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Contains(t, string(yamlBytes), "name: egress")
	assert.Contains(t, string(yamlBytes), "tcp_github_com_22")
}

func TestGenerateEnvoyConfig_TLSClusterAutoConfig(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
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

// TestGenerateEnvoyConfig_DenylistPathRules_InferAllowDefault locks in the
// fix for the inverse path-rule bug: a rule with only deny path_rules and
// no explicit PathDefault must catch unmatched paths through to upstream
// (allow), not 403. Without this, `firewall add foo.com --path /admin
// --action deny` silently denied the whole domain.
func TestGenerateEnvoyConfig_DenylistPathRules_InferAllowDefault(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{
			Dst:    "docs.example.com",
			Proto:  "https",
			Port:   443,
			Action: "allow",
			PathRules: []config.PathRule{
				{Path: "/admin", Action: "deny"},
			},
			// PathDefault deliberately unset — inference must produce
			// "allow" so the catch-all routes to upstream.
		},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
	require.NoError(t, err)
	assert.Empty(t, warnings)

	out := string(yamlBytes)
	assert.Contains(t, out, "docs.example.com")
	assert.Contains(t, out, "/admin")
	// The denied path emits a direct_response 403 in the generated config,
	// but the catch-all "/" must route to the upstream cluster — not also
	// 403. Count route-level `action: denied` clawker metadata entries:
	// the per-rule deny route and the deny_all virtual-host route are the
	// two structurally expected denies in this minimal config. A third
	// would mean the catch-all "/" is also denying (the original bug).
	denyMetadataCount := strings.Count(out, "action: denied")
	assert.Equal(t, 2, denyMetadataCount, "two deny routes expected (/admin path rule + deny_all virtual host); catch-all must route to upstream")
}

func TestGenerateEnvoyConfig_ZeroPortTLSDefaults443(t *testing.T) {
	t.Parallel()

	// Legacy store files may contain TLS rules with port:0 written before
	// normalizeRule defaulted TLS to 443. GenerateEnvoyConfig must handle this
	// defensively — Envoy rejects port_value:0 with a validation error.
	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "https", Port: 0, Action: "allow"},
		{Dst: "github.com", Proto: "https", Port: 0, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
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

// findStdoutEntry returns the stdout access log entry from the dual-sink
// access_log list returned by buildHTTPAccessLog / buildTCPAccessLog.
func findStdoutEntry(t *testing.T, logs []any) map[string]any {
	t.Helper()
	for _, e := range logs {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if entry["name"] == "envoy.access_loggers.stdout" {
			return entry
		}
	}
	t.Fatalf("no stdout access log entry found in %v", logs)
	return nil
}

// findOtelEntry returns the OpenTelemetry ALS access log entry from the
// dual-sink access_log list.
func findOtelEntry(t *testing.T, logs []any) map[string]any {
	t.Helper()
	for _, e := range logs {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if entry["name"] == "envoy.access_loggers.open_telemetry" {
			return entry
		}
	}
	t.Fatalf("no otel access log entry found in %v", logs)
	return nil
}

// otelAttrValue returns the string value associated with a given attribute
// key in an OpenTelemetryAccessLogConfig.attributes KeyValueList.
func otelAttrValue(t *testing.T, attrs map[string]any, key string) string {
	t.Helper()
	values, ok := attrs["values"].([]any)
	require.True(t, ok, "attributes.values not present or wrong type")
	for _, kv := range values {
		entry, ok := kv.(map[string]any)
		if !ok {
			continue
		}
		if entry["key"] == key {
			v, ok := entry["value"].(map[string]any)
			require.True(t, ok, "attribute value not a map")
			s, ok := v["string_value"].(string)
			require.True(t, ok, "attribute value.string_value not a string")
			return s
		}
	}
	t.Fatalf("attribute %q not found", key)
	return ""
}

func TestBuildHTTPAccessLog_DegradedModeStdoutOnly(t *testing.T) {
	t.Parallel()

	// Trust-lane invariant: when mTLS material is unavailable, infra
	// services must NOT emit OTLP across the untrusted lane. Only the
	// stdout sink ships from this helper; the OTel entry must be absent.
	logs := buildHTTPAccessLog(true, "allowed", ALSConfig{})
	require.Len(t, logs, 1, "degraded mode must emit stdout sink only")
	findStdoutEntry(t, logs)
}

func TestBuildHTTPAccessLog(t *testing.T) {
	t.Parallel()

	logs := buildHTTPAccessLog(true, "%METADATA(ROUTE:clawker:action)%", ALSConfig{MTLS: true, Port: 4319})
	require.Len(t, logs, 2, "expected dual-sink: stdout + otel")

	// Stdout sink: legacy JSON access log surfacing in `docker logs`.
	stdoutEntry := findStdoutEntry(t, logs)
	tc, ok := stdoutEntry["typed_config"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, tc["@type"], "StdoutAccessLog")
	lf, ok := tc["log_format"].(map[string]any)
	require.True(t, ok)
	jf, ok := lf["json_format"].(map[string]any)
	require.True(t, ok)

	// OTel network/tls semconv fields replace the legacy `proto` overload.
	assert.Equal(t, "tcp", jf["network.transport"])
	assert.Equal(t, "http", jf["network.protocol.name"])
	assert.Equal(t, "%PROTOCOL%", jf["network.protocol.version"])
	assert.Equal(t, "true", jf["tls.established"], "HCM with tlsTerminated=true must stamp tls.established=true")
	assert.Equal(t, "%DOWNSTREAM_TLS_VERSION%", jf["tls.protocol.version"])
	assert.Equal(t, "%DOWNSTREAM_TLS_CIPHER%", jf["tls.cipher"])

	// Common fields. Note: `source: "envoy"` and `timestamp` have been
	// pruned — `resource.service.name=envoy` (OTel ALS resource attribute)
	// and the OTel envelope `@timestamp` cover them respectively.
	assert.Nil(t, jf["source"], "redundant with resource.service.name=envoy")
	assert.Nil(t, jf["timestamp"], "redundant with envelope @timestamp")
	// OTel semconv: server.address = "host the client was trying to reach"
	// (stable, replaces deprecated tls.server.name). TLS-MITM HCM sources
	// from SNI; the plaintext HCM variant tested below overrides to
	// %REQ(Host)% since SNI is unavailable on plaintext.
	assert.Equal(t, "%REQUESTED_SERVER_NAME%", jf["server.address"])
	assert.Equal(t, "%DURATION%", jf["duration_ms"])
	assert.Equal(t, "%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%", jf["client.address"])
	assert.Equal(t, "%UPSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%", jf["network.peer.address"])
	assert.Equal(t, "%UPSTREAM_REMOTE_PORT%", jf["network.peer.port"])

	// HTTP-specific fields present.
	assert.Equal(t, "%RESPONSE_CODE%", jf["response_code"])
	assert.Equal(t, "%REQ(:METHOD)%", jf["method"])
	assert.Equal(t, "%REQ(:PATH)%", jf["path"])
	assert.Equal(t, "%UPSTREAM_TRANSPORT_FAILURE_REASON%", jf["upstream_transport_failure_reason"])

	// Legacy + redundant field names dropped. `request_host` consolidated
	// into `server.address` (Host header source for plaintext); the
	// pre-rename colloquial fields (`domain`, `client_ip`, `upstream_ip`,
	// `upstream_port`) replaced by OTel semconv equivalents.
	_, hasLegacyProto := jf["proto"]
	assert.False(t, hasLegacyProto, "legacy `proto` field must be gone (replaced by network.transport / network.protocol.name)")
	_, hasLegacyDsTLS := jf["downstream_tls_version"]
	assert.False(t, hasLegacyDsTLS, "legacy `downstream_tls_version` replaced by tls.protocol.version")
	_, hasRequestHost := jf["request_host"]
	assert.False(t, hasRequestHost, "request_host consolidated into server.address")

	// OTel ALS sink: structured fields on attributes, service.name on resource.
	otelEntry := findOtelEntry(t, logs)
	otc, ok := otelEntry["typed_config"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, otc["@type"], "OpenTelemetryAccessLogConfig")
	grpcSvc, ok := otc["grpc_service"].(map[string]any)
	require.True(t, ok)
	envoyGrpc, ok := grpcSvc["envoy_grpc"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, otelCollectorALSClusterName, envoyGrpc["cluster_name"])

	resAttrs, ok := otc["resource_attributes"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "envoy", otelAttrValue(t, resAttrs, "service.name"))

	attrs, ok := otc["attributes"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "tcp", otelAttrValue(t, attrs, "network.transport"))
	assert.Equal(t, "http", otelAttrValue(t, attrs, "network.protocol.name"))
	assert.Equal(t, "%PROTOCOL%", otelAttrValue(t, attrs, "network.protocol.version"))
	assert.Equal(t, "true", otelAttrValue(t, attrs, "tls.established"))
	assert.Equal(t, "%REQ(:METHOD)%", otelAttrValue(t, attrs, "method"))
	assert.Equal(t, "%REQ(:PATH)%", otelAttrValue(t, attrs, "path"))
	assert.Equal(t, "%REQUESTED_SERVER_NAME%", otelAttrValue(t, attrs, "server.address"))
	assert.Equal(t, "%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%", otelAttrValue(t, attrs, "client.address"))
	assert.Equal(t, "%UPSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%", otelAttrValue(t, attrs, "network.peer.address"))
}

func TestBuildHTTPAccessLog_PlaintextHCMStampsTLSEstablishedFalse(t *testing.T) {
	t.Parallel()

	// When the plaintext HCM filter chain (no upstream TLS termination) calls
	// the builder, tls.established must stamp "false" so consumers can
	// distinguish HTTPS-MITM records from plaintext HTTP records purely from
	// the indexed attribute. server.address falls back to the Host header
	// because SNI is unavailable on plaintext — without the override, the
	// destination would be empty for every plaintext record.
	logs := buildHTTPAccessLog(false, "allowed", ALSConfig{})
	stdoutEntry := findStdoutEntry(t, logs)
	jf := stdoutEntry["typed_config"].(map[string]any)["log_format"].(map[string]any)["json_format"].(map[string]any)
	assert.Equal(t, "false", jf["tls.established"])
	assert.Equal(t, "%REQ(Host)%", jf["server.address"], "plaintext HCM must source server.address from Host header (SNI unavailable)")
}

func TestBuildTCPAccessLog_DegradedModeStdoutOnly(t *testing.T) {
	t.Parallel()

	// Mirror of the HTTP-side degraded-mode guard at the TCP/SSH/deny
	// access-log builder.
	logs := buildTCPAccessLog("ssh", "allowed", ALSConfig{})
	require.Len(t, logs, 1, "degraded mode must emit stdout sink only")
	findStdoutEntry(t, logs)
}

func TestBuildTCPAccessLog(t *testing.T) {
	t.Parallel()

	logs := buildTCPAccessLog("ssh", "allowed", ALSConfig{MTLS: true, Port: 4319})
	require.Len(t, logs, 2, "expected dual-sink: stdout + otel")

	stdoutEntry := findStdoutEntry(t, logs)
	tc, ok := stdoutEntry["typed_config"].(map[string]any)
	require.True(t, ok)
	lf, ok := tc["log_format"].(map[string]any)
	require.True(t, ok)
	jf, ok := lf["json_format"].(map[string]any)
	require.True(t, ok)

	// OTel network/tls semconv fields.
	assert.Equal(t, "tcp", jf["network.transport"])
	assert.Equal(t, "ssh", jf["network.protocol.name"])
	assert.Equal(t, "false", jf["tls.established"], "opaque TCP listener does not terminate TLS")

	// network.protocol.version is HTTP-only and must be absent on TCP records.
	_, hasVersion := jf["network.protocol.version"]
	assert.False(t, hasVersion, "network.protocol.version is HTTP-only — absent on TCP path")

	// Common fields present.
	assert.Equal(t, "%REQUESTED_SERVER_NAME%", jf["server.address"])
	assert.Equal(t, "%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%", jf["client.address"])

	// HTTP-specific fields absent on TCP variant.
	assert.Nil(t, jf["response_code"])
	assert.Nil(t, jf["method"])
	assert.Nil(t, jf["path"])
	assert.Nil(t, jf["upstream_transport_failure_reason"], "upstream_transport_failure_reason is HTTP-only — absent on TCP path")

	// Legacy field names gone.
	_, hasLegacyProto := jf["proto"]
	assert.False(t, hasLegacyProto, "legacy `proto` field must be gone")

	// OTel sink mirrors the same shape and carries service.name=envoy.
	otelEntry := findOtelEntry(t, logs)
	otc, ok := otelEntry["typed_config"].(map[string]any)
	require.True(t, ok)
	resAttrs, ok := otc["resource_attributes"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "envoy", otelAttrValue(t, resAttrs, "service.name"))

	attrs, ok := otc["attributes"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "tcp", otelAttrValue(t, attrs, "network.transport"))
	assert.Equal(t, "ssh", otelAttrValue(t, attrs, "network.protocol.name"))
	assert.Equal(t, "false", otelAttrValue(t, attrs, "tls.established"))
}

func TestBuildTCPAccessLog_DomainOverride(t *testing.T) {
	t.Parallel()

	logs := buildTCPAccessLog("", "denied", ALSConfig{MTLS: true, Port: 4319}, "github.com")
	require.Len(t, logs, 2)

	// Static domain overrides %REQUESTED_SERVER_NAME% in both sinks.
	stdoutEntry := findStdoutEntry(t, logs)
	tc, ok := stdoutEntry["typed_config"].(map[string]any)
	require.True(t, ok)
	lf, ok := tc["log_format"].(map[string]any)
	require.True(t, ok)
	jf, ok := lf["json_format"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "github.com", jf["server.address"])
	assert.Equal(t, "tcp", jf["network.transport"])
	assert.Equal(t, "", jf["network.protocol.name"], "deny chain has no negotiated L7 protocol")
	assert.Equal(t, "denied", jf["action"])

	otelEntry := findOtelEntry(t, logs)
	otc, ok := otelEntry["typed_config"].(map[string]any)
	require.True(t, ok)
	attrs, ok := otc["attributes"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "github.com", otelAttrValue(t, attrs, "server.address"))
	assert.Equal(t, "denied", otelAttrValue(t, attrs, "action"))
}

func TestGenerateEnvoyConfig_DegradedModeOmitsOtelSink(t *testing.T) {
	t.Parallel()

	// Trust-lane invariant: when mTLS material is unavailable, Envoy must
	// emit access logs to stdout only — never push OTLP across the
	// untrusted otel-collector:4317 lane (reserved for agent containers).
	// The otel_collector_als cluster and OpenTelemetryAccessLogConfig
	// entries must both be absent in this mode.
	//
	// Fixture exercises all four sender paths (TLS filter chain, HTTP
	// filter chain, deny chain implicit on the egress listener, TCP/SSH
	// listener) so a regression on any single helper is caught.
	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "example.com", Proto: "https", Port: 80, Action: "allow"},
		{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
	}
	yamlBytes, _, err := GenerateEnvoyConfig(rules, EnvoyPorts{
		EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901,
	}, ALSConfig{})
	require.NoError(t, err)
	out := string(yamlBytes)

	assert.NotContains(t, out, otelCollectorALSClusterName, "OTel ALS cluster must not be emitted in degraded mode")
	assert.NotContains(t, out, "envoy.access_loggers.open_telemetry", "OTel access-log sink must not be emitted in degraded mode")
	assert.NotContains(t, out, "OpenTelemetryAccessLogConfig", "OTel access-log config must not be emitted in degraded mode")
	// Stdout sink for `docker logs` triage stays on the TLS filter chain.
	assert.Contains(t, out, "envoy.access_loggers.stdout")
}

func TestGenerateEnvoyConfig_OtelALSCluster_MTLS(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
	}
	yamlBytes, _, err := GenerateEnvoyConfig(rules, EnvoyPorts{
		EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901,
	}, ALSConfig{Port: 4319, MTLS: true})
	require.NoError(t, err)
	out := string(yamlBytes)

	// OTel ALS cluster + sink emitted; targets the mTLS-gated receiver
	// port, not the untrusted 4317 lane.
	assert.Contains(t, out, otelCollectorALSClusterName)
	assert.Contains(t, out, "envoy.access_loggers.open_telemetry")
	assert.Contains(t, out, "port_value: 4319")
	assert.NotContains(t, out, "port_value: 4317", "infra services must never dial the untrusted lane")
	// Upstream TLS context with the bind-mount paths Stack writes.
	assert.Contains(t, out, "transport_socket")
	assert.Contains(t, out, "UpstreamTlsContext")
	assert.Contains(t, out, "/etc/envoy/otel-tls/client.pem")
	assert.Contains(t, out, "/etc/envoy/otel-tls/client.key")
	assert.Contains(t, out, "/etc/envoy/otel-tls/ca.pem")
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
				{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
			},
		},
		{
			name: "TLS with path rules has access_log",
			rules: []config.EgressRule{
				{
					Dst: "api.example.com", Proto: "https", Port: 443, Action: "allow",
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
				{Dst: "example.com", Proto: "https", Port: 80, Action: "allow"},
			},
		},
	}

	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}
	// Exercise the mTLS-on path: both stdout + OTel sinks must be emitted
	// on every listener type. (The degraded path is covered by
	// TestGenerateEnvoyConfig_DegradedModeOmitsOtelSink.)
	als := ALSConfig{Port: 4319, MTLS: true}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			yamlBytes, _, err := GenerateEnvoyConfig(tt.rules, ports, als)
			require.NoError(t, err)
			out := string(yamlBytes)
			// Stdout sink (operator triage).
			assert.Contains(t, out, "envoy.access_loggers.stdout")
			assert.Contains(t, out, "StdoutAccessLog")
			assert.Contains(t, out, "json_format")
			// OTel ALS sink (collector ingest).
			assert.Contains(t, out, "envoy.access_loggers.open_telemetry")
			assert.Contains(t, out, "OpenTelemetryAccessLogConfig")
			assert.Contains(t, out, otelCollectorALSClusterName)
		})
	}
}

func TestNormalizeAndDedup(t *testing.T) {
	t.Parallel()

	// Simulates a legacy store with port:0 and port:443 duplicates.
	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "https", Port: 0, Action: "allow"},
		{Dst: "github.com", Proto: "https", Port: 0, Action: "allow"},
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "github.com", Proto: "https", Port: 443, Action: "allow"},
	}

	result, _ := NormalizeAndDedup(rules)
	assert.Len(t, result, 2)
	for _, r := range result {
		assert.Equal(t, 443, r.Port, "rule for %s should have port 443", r.Dst)
	}
}

func TestNormalizeAndDedup_WildcardAndExactCoexist(t *testing.T) {
	t.Parallel()

	// Wildcard and exact for the same domain — both kept as separate rules.
	rules := []config.EgressRule{
		{Dst: "claude.ai", Proto: "https", Port: 443, Action: "allow"},
		{Dst: ".claude.ai", Proto: "https", Port: 443, Action: "allow"},
	}

	result, _ := NormalizeAndDedup(rules)
	assert.Len(t, result, 2, "wildcard and exact are distinct rules")

	dsts := []string{result[0].Dst, result[1].Dst}
	assert.Contains(t, dsts, "claude.ai")
	assert.Contains(t, dsts, ".claude.ai")
}

func TestNormalizeAndDedup_ExactDuplicatesStillDeduped(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "claude.ai", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "claude.ai", Proto: "https", Port: 443, Action: "allow"},
	}

	result, _ := NormalizeAndDedup(rules)
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
		{Dst: ".datadoghq.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, warnings, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
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
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
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
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
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
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "mcp-proxy.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
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

// TestGenerateEnvoyConfig_TLSClusterPortPinning verifies that each per-domain TLS
// cluster uses the port from the rule config as its endpoint port_value — not derived
// from the Host header. This is the security fix: LOGICAL_DNS endpoints have fixed
// ports, eliminating the confused deputy attack via Host header port manipulation.
func TestGenerateEnvoyConfig_TLSClusterPortPinning(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "custom.example.com", Proto: "https", Port: 8443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
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
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "example.com", Proto: "https", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
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
		{Dst: ".", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "..", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "valid.com", Proto: "https", Port: 443, Action: "allow"},
	}

	result, warnings := NormalizeAndDedup(rules)
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

// ──────────────────────────────────────────────────────────────────────────────
// Gap coverage: HTTP port 0 defaults to 80 (#10)
// ──────────────────────────────────────────────────────────────────────────────

// ──────────────────────────────────────────────────────────────────────────────
// Gap coverage: WebSocket ALPN override structural test (#11)
// ──────────────────────────────────────────────────────────────────────────────

func TestGenerateEnvoyConfig_TLSWebSocketALPNOverride(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
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
		{Dst: "example.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "example.com", Proto: "https", Port: 8443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
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

// ──────────────────────────────────────────────────────────────────────────────
// Gap coverage: tls_inspector listener filter presence (#13)
// ──────────────────────────────────────────────────────────────────────────────

func TestGenerateEnvoyConfig_TLSInspectorPresent(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "example.com", Proto: "https", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
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
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "example.com", Proto: "https", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
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

// TestBuildDenyFilterChain_ProtoNotOverloaded locks in the un-overloading
// of the legacy `proto` access log field that conflated protocol with the
// firewall verdict. The deny chain now stamps `network.transport: "tcp"`,
// `network.protocol.name: ""` (no L7 negotiated for a denied connection),
// and `action: "denied"` (the dedicated verdict field). Regression here
// would reintroduce the field overload that motivated the action
// standardization.
func TestBuildDenyFilterChain_ProtoNotOverloaded(t *testing.T) {
	t.Parallel()

	logs := buildTCPAccessLog("", "denied", ALSConfig{MTLS: true, Port: 4319})
	stdoutEntry := findStdoutEntry(t, logs)
	tc := stdoutEntry["typed_config"].(map[string]any)
	lf := tc["log_format"].(map[string]any)
	jf := lf["json_format"].(map[string]any)
	assert.Equal(t, "tcp", jf["network.transport"])
	assert.Equal(t, "", jf["network.protocol.name"], "deny chain has no negotiated L7")
	assert.Equal(t, "denied", jf["action"], "deny_cluster action must carry the firewall verdict")
	_, hasLegacyProto := jf["proto"]
	assert.False(t, hasLegacyProto, "legacy `proto` field must be gone")
}

// TestBuildDenyFilterChain_IdleTimeoutRemoved locks in the DoS-surface fix
// at envoy_config.go's buildDenyFilterChain. The previous explicit
// `"idle_timeout": "0s"` disabled the timeout per Envoy tcp_proxy.proto
// warning ("Disabling this timeout is likely to yield connection leaks").
// Regression here would re-introduce a path where a hostile agent can pin
// Envoy resources by opening N never-closed connections to a blocked SNI.
func TestBuildDenyFilterChain_IdleTimeoutRemoved(t *testing.T) {
	t.Parallel()

	chain := buildDenyFilterChain(ALSConfig{})
	filters := chain["filters"].([]any)
	tcpProxy := filters[0].(map[string]any)
	tc := tcpProxy["typed_config"].(map[string]any)
	_, present := tc["idle_timeout"]
	assert.False(t, present, "deny_cluster must NOT set idle_timeout (Envoy default of 1h applies; explicit '0s' disables and leaks)")
}

// TestGenerateEnvoyConfig_RouteMetadataActionStamped locks in the
// concrete-at-emit-time action stamping. EVERY route literal in the
// generated config carries metadata.filter_metadata.clawker.action so
// the HTTP access log's %METADATA(ROUTE:clawker:action)% substitution
// reads a value baked at config generation. A missing metadata block on
// any route would cause Envoy to emit `action: -` for requests matching
// that route — breaking the dashboard's per-record verdict signal.
func TestGenerateEnvoyConfig_RouteMetadataActionStamped(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.example.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "raw.example.com", Proto: "https", Port: 443, Action: "allow",
			PathDefault: "deny",
			PathRules: []config.PathRule{
				{Path: "/allowed/", Action: "allow"},
			},
		},
		{Dst: "plain.example.com", Proto: "https", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
	require.NoError(t, err)

	// Parse the generated YAML and walk every virtual_host.routes entry,
	// asserting each route has clawker.action metadata. Walking the
	// structure (vs string-counting) avoids false matches on
	// filter_chain_match and other unrelated map keys.
	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	checkedRoutes := 0
	listeners := cfg["static_resources"].(map[string]any)["listeners"].([]any)
	for _, l := range listeners {
		lis := l.(map[string]any)
		for _, c := range lis["filter_chains"].([]any) {
			ch := c.(map[string]any)
			for _, f := range ch["filters"].([]any) {
				flt := f.(map[string]any)
				if flt["name"] != "envoy.filters.network.http_connection_manager" {
					continue
				}
				tc := flt["typed_config"].(map[string]any)
				rc, _ := tc["route_config"].(map[string]any)
				if rc == nil {
					continue
				}
				for _, vh := range rc["virtual_hosts"].([]any) {
					for _, r := range vh.(map[string]any)["routes"].([]any) {
						route := r.(map[string]any)
						md, ok := route["metadata"].(map[string]any)
						require.True(t, ok, "route %v missing metadata block", route["match"])
						fm, ok := md["filter_metadata"].(map[string]any)
						require.True(t, ok, "route %v missing filter_metadata", route["match"])
						clw, ok := fm["clawker"].(map[string]any)
						require.True(t, ok, "route %v missing clawker filter namespace", route["match"])
						action, ok := clw["action"].(string)
						require.True(t, ok, "route %v missing clawker.action", route["match"])
						assert.Contains(t, []string{"allowed", "denied"}, action, "action must be allowed|denied")
						checkedRoutes++
					}
				}
			}
		}
	}
	assert.GreaterOrEqual(t, checkedRoutes, 4, "expected ≥4 routes across the test config (tls allow, tls path-rule allow+deny, http allow, deny_all)")
}

// TestGenerateEnvoyConfig_HCMHardening locks in the path-smuggling-vector
// fix. Every HTTP connection manager MUST carry the edge-hardening field
// set so URL-encoded traversal cannot bypass the route-prefix matcher.
// Missing any one of these fields reintroduces the verified
// CVE-class smuggling vector documented in plan
// compressed-floating-matsumoto.md §4.
func TestGenerateEnvoyConfig_HCMHardening(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "tls.example.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "plain.example.com", Proto: "http", Port: 80, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	// Walk every HCM typed_config in every listener filter chain and
	// assert the hardening field set is complete. Both the TLS chain HCM
	// and the plaintext HTTP chain HCM must carry the full set.
	required := []string{
		"normalize_path",
		"merge_slashes",
		"path_with_escaped_slashes_action",
		"common_http_protocol_options",
		"http2_protocol_options",
	}

	hcmCount := 0
	egressListenerSeen := false
	listeners := cfg["static_resources"].(map[string]any)["listeners"].([]any)
	for _, l := range listeners {
		lis := l.(map[string]any)
		if lis["name"] != "egress" {
			continue
		}
		egressListenerSeen = true
		for _, c := range lis["filter_chains"].([]any) {
			ch := c.(map[string]any)
			for _, f := range ch["filters"].([]any) {
				flt := f.(map[string]any)
				if flt["name"] != "envoy.filters.network.http_connection_manager" {
					continue
				}
				tc := flt["typed_config"].(map[string]any)
				hcmCount++
				for _, field := range required {
					_, present := tc[field]
					assert.True(t, present, "HCM stat_prefix=%v missing hardening field %q — reintroduces path-smuggling vector", tc["stat_prefix"], field)
				}
				assert.Equal(t, "UNESCAPE_AND_REDIRECT", tc["path_with_escaped_slashes_action"], "URL-encoded traversal must be unescaped before route match")
				assert.Equal(t, true, tc["normalize_path"], "normalize_path MUST be true to defeat traversal smuggling")
				assert.Equal(t, true, tc["merge_slashes"], "merge_slashes MUST be true to defeat path-segment smuggling")
			}
		}
	}
	assert.True(t, egressListenerSeen, "egress listener not found in generated config")
	assert.GreaterOrEqual(t, hcmCount, 2, "expected at least 2 HCMs (TLS chain + plaintext HTTP chain)")
}

// TestGenerateEnvoyConfig_DenyResponseBodyNotFingerprinted locks in the
// non-fingerprinting 403 body. The previous "Blocked by clawker firewall"
// literal disclosed the enforcement product to any agent — useful intel
// for an injected-prompt adversary tuning a bypass. Generic
// firewallBlockedBody decouples the response body from product identity;
// the firewall verdict travels via the action access log field.
func TestGenerateEnvoyConfig_DenyResponseBodyNotFingerprinted(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.example.com", Proto: "https", Port: 443, Action: "allow",
			PathDefault: "deny",
			PathRules:   []config.PathRule{{Path: "/allowed/", Action: "allow"}},
		},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
	require.NoError(t, err)

	out := string(yamlBytes)
	assert.NotContains(t, out, "Blocked by clawker firewall", "deny body must not name the firewall product (fingerprint disclosure)")
	assert.NotContains(t, out, "clawker firewall", "no inline string may reveal the clawker name in response bodies")
	assert.Contains(t, out, strings.TrimSpace(firewallBlockedBody), "centralized firewallBlockedBody must be present")
}
