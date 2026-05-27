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
	// Upstream auto_config offers both HTTP/1.1 and HTTP/2 via ALPN.
	assert.Contains(t, out, "http2_protocol_options: {}")
	// WebSocket upgrade carries a per-request ALPN override forcing upstream
	// to h1.1 (RFC 6455 Upgrade); h2-WS support is downstream-only and
	// covered by _DownstreamHCMAllowsH2WebSocket + _TLSWebSocketUpgrade.
	assert.Contains(t, out, "upgrade_type: websocket")
	assert.Contains(t, out, "envoy.network.application_protocols")
	// Exact-rule chains do not run DFP.
	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))
	c := findTLSFilterChain(t, cfg, "api.anthropic.com")
	require.NotNil(t, c)
	for _, f := range chainHCM(t, c)["http_filters"].([]any) {
		assert.NotEqual(t, "envoy.filters.http.dynamic_forward_proxy", f.(map[string]any)["name"], "exact-rule TLS chain must not include DFP filter")
	}
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

	// Walk the route table structurally and assert the inferred-default
	// invariant: explicit deny path rules deny, the catch-all "/" allows.
	// A regression where the catch-all also denied would silently 403 the
	// whole domain — the original denylist bug.
	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	var catchAllAction, adminAction string
	catchAllSeen, adminSeen := false, false
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
					vhost := vh.(map[string]any)
					// Only inspect the docs.example.com virtual host —
					// other virtual hosts (e.g. deny_all SNI catch-all)
					// carry their own denied catch-all by design.
					domains, _ := vhost["domains"].([]any)
					isTarget := false
					for _, d := range domains {
						if s, ok := d.(string); ok && s == "docs.example.com" {
							isTarget = true
							break
						}
					}
					if !isTarget {
						continue
					}
					for _, r := range vhost["routes"].([]any) {
						route := r.(map[string]any)
						match, _ := route["match"].(map[string]any)
						prefix, _ := match["prefix"].(string)
						md := route["metadata"].(map[string]any)
						fm := md["filter_metadata"].(map[string]any)
						clw := fm["clawker"].(map[string]any)
						action := clw["action"].(string)
						switch prefix {
						case "/":
							catchAllAction = action
							catchAllSeen = true
						case "/admin":
							adminAction = action
							adminSeen = true
						}
					}
				}
			}
		}
	}
	require.True(t, adminSeen, "/admin path-rule route not found in docs.example.com virtual host")
	require.True(t, catchAllSeen, `catch-all "/" route not found in docs.example.com virtual host`)
	assert.Equal(t, "denied", adminAction, "/admin path rule must carry denied verdict")
	assert.Equal(t, "allowed", catchAllAction, `catch-all "/" must carry allowed verdict — inferred PathDefault for denylist-only rule`)
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
	// Deny chain catches both TLS handshakes and plaintext flows that no
	// allow chain claimed — Envoy resets before observing which.
	// tls.established MUST be omitted (not "false") so forensics is not
	// misled into reading a denied TLS handshake as a plaintext attempt.
	_, hasTLSEst := jf["tls.established"]
	assert.False(t, hasTLSEst, "deny chain must OMIT tls.established (TLS-vs-plaintext is unobservable on a reset)")

	otelEntry := findOtelEntry(t, logs)
	otc, ok := otelEntry["typed_config"].(map[string]any)
	require.True(t, ok)
	attrs, ok := otc["attributes"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "github.com", otelAttrValue(t, attrs, "server.address"))
	assert.Equal(t, "denied", otelAttrValue(t, attrs, "action"))
	// Same omission must hold on the OTel attributes sink.
	for _, v := range attrs["values"].([]any) {
		entry := v.(map[string]any)
		assert.NotEqual(t, "tls.established", entry["key"], "deny chain OTel attrs must OMIT tls.established")
	}
}

// TestBuildDenyFilterChain_OmitsTLSEstablished walks the generated deny
// filter chain end-to-end and asserts tls.established is absent from
// both access-log sinks. The deny chain catches both TLS and plaintext
// flows; stamping a bool on a denied TLS handshake misleads forensics.
func TestBuildDenyFilterChain_OmitsTLSEstablished(t *testing.T) {
	t.Parallel()

	chain := buildDenyFilterChain(ALSConfig{MTLS: true, Port: 4319})
	filters := chain["filters"].([]any)
	tcpProxy := filters[0].(map[string]any)
	tc := tcpProxy["typed_config"].(map[string]any)
	accessLog := tc["access_log"].([]any)

	for _, entry := range accessLog {
		entryMap := entry.(map[string]any)
		switch entryMap["name"] {
		case "envoy.access_loggers.stdout":
			etc := entryMap["typed_config"].(map[string]any)
			lf := etc["log_format"].(map[string]any)
			jf := lf["json_format"].(map[string]any)
			_, has := jf["tls.established"]
			assert.False(t, has, "stdout deny-chain access log must OMIT tls.established")
		case "envoy.access_loggers.open_telemetry":
			etc := entryMap["typed_config"].(map[string]any)
			attrs := etc["attributes"].(map[string]any)
			for _, v := range attrs["values"].([]any) {
				e := v.(map[string]any)
				assert.NotEqual(t, "tls.established", e["key"], "otel deny-chain access log must OMIT tls.established")
			}
		}
	}
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

// TestGenerateEnvoyConfig_SimplifiedFilterChains verifies the http_filters
// shape on TLS and plaintext-HTTP chains. TLS chains carry [sni-lock, router]
// — sni-lock pre-populates upstream_server_name + upstream_subject_alt_names
// from %REQUESTED_SERVER_NAME% so the router can't fall back to deriving
// either from the :authority (Host) header under auto_sni / auto_san_validation.
// Plaintext HTTP chains carry [router] only — no upstream TLS handshake so no
// SNI to lock.
func TestGenerateEnvoyConfig_SimplifiedFilterChains(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "example.com", Proto: "http", Port: 80, Action: "allow"},
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
			fcm, _ := chain["filter_chain_match"].(map[string]any)
			// Skip deny chain (catch-all, tcp_proxy filter).
			if len(fcm) == 0 {
				continue
			}
			filters := chain["filters"].([]any)
			hcm := filters[0].(map[string]any)
			tc := hcm["typed_config"].(map[string]any)
			httpFilters := tc["http_filters"].([]any)

			names := []string{}
			for _, hf := range httpFilters {
				names = append(names, hf.(map[string]any)["name"].(string))
			}

			// Plaintext chain is matched by transport_protocol=raw_buffer;
			// TLS chains by SNI.
			if fcm["transport_protocol"] == "raw_buffer" {
				assert.Equal(t,
					[]string{"envoy.filters.http.router"},
					names,
					"plaintext HTTP chain has no upstream TLS — no SNI to lock, router only",
				)
				continue
			}
			assert.Equal(t,
				[]string{
					"envoy.filters.http.set_filter_state",
					"envoy.filters.http.router",
				},
				names,
				"TLS chain must carry sni-lock + router (sni-lock pins upstream SNI/SAN to downstream SNI, defeating Host-header confused-deputy at the upstream TLS handshake)",
			)
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
		{Dst: ".wild.example.com", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	// Walk every HCM typed_config in every listener filter chain and
	// assert the hardening field set is complete. TLS exact, TLS wildcard,
	// and plaintext HTTP chain HCMs must all carry the full set.
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
	assert.GreaterOrEqual(t, hcmCount, 3, "expected at least 3 HCMs (TLS exact + TLS wildcard + plaintext HTTP)")
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
}

// findCluster returns the cluster map with the given name from a parsed envoy
// config, or nil if not found.
func findCluster(t *testing.T, cfg map[string]any, name string) map[string]any {
	t.Helper()
	sr := cfg["static_resources"].(map[string]any)
	for _, c := range sr["clusters"].([]any) {
		cl := c.(map[string]any)
		if cl["name"] == name {
			return cl
		}
	}
	return nil
}

// findTLSFilterChain returns the egress listener's filter chain whose
// server_names list contains the given SNI entry, or nil.
func findTLSFilterChain(t *testing.T, cfg map[string]any, sni string) map[string]any {
	t.Helper()
	sr := cfg["static_resources"].(map[string]any)
	for _, l := range sr["listeners"].([]any) {
		lis := l.(map[string]any)
		if lis["name"] != "egress" {
			continue
		}
		for _, fc := range lis["filter_chains"].([]any) {
			chain := fc.(map[string]any)
			fcm, ok := chain["filter_chain_match"].(map[string]any)
			if !ok {
				continue
			}
			sn, ok := fcm["server_names"].([]any)
			if !ok {
				continue
			}
			for _, s := range sn {
				if s.(string) == sni {
					return chain
				}
			}
		}
	}
	return nil
}

// chainHCM returns the HCM typed_config from a TLS filter chain.
func chainHCM(t *testing.T, chain map[string]any) map[string]any {
	t.Helper()
	filters := chain["filters"].([]any)
	require.NotEmpty(t, filters)
	return filters[0].(map[string]any)["typed_config"].(map[string]any)
}

// TestGenerateEnvoyConfig_WildcardClusterIsDFP — a wildcard rule must emit a
// dynamic_forward_proxy cluster with sub_clusters_config, not LOGICAL_DNS.
// LOGICAL_DNS pins the upstream endpoint to the apex hostname, which sends
// requests for any subdomain to whatever IP the apex resolves to. That fails
// when an apex and its subdomains live on different infrastructure (e.g.
// mintlify.com → Vercel, www.mintlify.com → Cloudflare) — Envoy connects to
// the apex IP, sends SNI for the subdomain, and the upstream cert verification
// fails on the wrong backend.
func TestGenerateEnvoyConfig_WildcardClusterIsDFP(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: ".mintlify.com", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	cl := findCluster(t, cfg, "tls_wildcard_mintlify_com_443")
	require.NotNil(t, cl, "wildcard rule must produce a tls_wildcard_<apex>_<port> cluster")

	ct, ok := cl["cluster_type"].(map[string]any)
	require.True(t, ok, "wildcard cluster must use cluster_type (dynamic_forward_proxy), not the LOGICAL_DNS type field")
	assert.Equal(t, "envoy.clusters.dynamic_forward_proxy", ct["name"])

	tc := ct["typed_config"].(map[string]any)
	sub, ok := tc["sub_clusters_config"].(map[string]any)
	require.True(t, ok, "wildcard DFP cluster must enable sub_clusters_config for per-host:port pool isolation")
	assert.NotZero(t, sub["max_sub_clusters"], "max_sub_clusters must be set explicitly")
	assert.NotEmpty(t, sub["sub_cluster_ttl"], "sub_cluster_ttl must be set explicitly")

	// allow_coalesced_connections is explicitly false in YAML, not relying on
	// the proto default. With sub_clusters each host:port has its own pool;
	// coalescing across pools would re-open the same-SAN h2 pool reuse race
	// where the first IP to win a pool serves every subsequent same-SAN
	// hostname. Pinning the flag at the YAML layer keeps the boundary stable
	// against any future upstream default change.
	v, present := tc["allow_coalesced_connections"]
	require.True(t, present, "allow_coalesced_connections must be explicit in YAML, not relying on proto default")
	assert.Equal(t, false, v, "allow_coalesced_connections must be false to keep pools isolated across SAN-shared hostnames")

	// Upstream TLS context — must keep auto_sni + auto_san_validation and the
	// system trusted_ca so the upstream cert is validated against the SNI
	// derived from the request.
	ts, ok := cl["transport_socket"].(map[string]any)
	require.True(t, ok)
	utc := ts["typed_config"].(map[string]any)
	assert.Contains(t, utc["@type"], "UpstreamTlsContext")
	common := utc["common_tls_context"].(map[string]any)
	vc := common["validation_context"].(map[string]any)
	assert.Equal(t, "/etc/ssl/certs/ca-certificates.crt", vc["trusted_ca"].(map[string]any)["filename"])

	hpo := cl["typed_extension_protocol_options"].(map[string]any)["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"].(map[string]any)
	uhp := hpo["upstream_http_protocol_options"].(map[string]any)
	assert.Equal(t, true, uhp["auto_sni"])
	assert.Equal(t, true, uhp["auto_san_validation"])
	auto, ok := hpo["auto_config"].(map[string]any)
	require.True(t, ok, "wildcard cluster must keep auto_config (h2 + h1.1 ALPN) parity with exact clusters")
	_, hasH1 := auto["http_protocol_options"]
	_, hasH2 := auto["http2_protocol_options"]
	assert.True(t, hasH1 && hasH2, "auto_config must offer both HTTP/1.1 and HTTP/2 so h2 wins via ALPN when supported")
}

// TestGenerateEnvoyConfig_WildcardClusterIsV4Only — clawker-net is IPv4-only,
// matching the explicit V4_ONLY pin on the exact-rule LOGICAL_DNS cluster.
// Without this on the wildcard DFP cluster, dynamically-spawned sub-clusters
// pick up Envoy's `AUTO` default and try AAAA first; any wildcard host with
// IPv6 records (e.g. claude.ai → 2607:6bc0::10) hits
// `immediate_connect_error: Network is unreachable` at the Envoy upstream.
func TestGenerateEnvoyConfig_WildcardClusterIsV4Only(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: ".claude.ai", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	cl := findCluster(t, cfg, "tls_wildcard_claude_ai_443")
	require.NotNil(t, cl)
	assert.Equal(t, "V4_ONLY", cl["dns_lookup_family"], "wildcard DFP cluster must pin V4_ONLY — clawker-net has no IPv6 path and the Envoy default (AUTO) prefers AAAA, which yields Network-is-unreachable on any v6-enabled upstream")
}

// TestGenerateEnvoyConfig_WildcardAndExactCoexistTwoClusters — when a
// wildcard rule and an exact rule for the same apex are present together,
// the generator must produce two distinct clusters (one DFP, one LOGICAL_DNS).
// Collapsing them into one is the bug this fix addresses; the DFP cluster's
// per-host:port pool isolation also keeps the exact-apex pool from sharing
// upstream connections with subdomain pools.
func TestGenerateEnvoyConfig_WildcardAndExactCoexistTwoClusters(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: ".mintlify.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "mintlify.com", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	exact := findCluster(t, cfg, "tls_mintlify_com_443")
	require.NotNil(t, exact, "exact apex rule must produce tls_<apex>_<port> cluster")
	assert.Equal(t, "LOGICAL_DNS", exact["type"], "exact rule must remain LOGICAL_DNS")

	wild := findCluster(t, cfg, "tls_wildcard_mintlify_com_443")
	require.NotNil(t, wild, "wildcard rule must produce a distinct tls_wildcard_<apex>_<port> cluster")
	ct := wild["cluster_type"].(map[string]any)
	assert.Equal(t, "envoy.clusters.dynamic_forward_proxy", ct["name"])
}

// TestGenerateEnvoyConfig_TLSChainSNILock — every TLS filter chain (exact
// AND wildcard) must prepend a set_filter_state that locks the upstream
// SNI / SAN validation target to the downstream SNI. Without this lock,
// Router::Filter::decodeHeaders derives both from the :authority header,
// letting an attacker who controls Host validate the upstream cert against
// a different name than the one that selected the chain — exfiltration to
// unallowed siblings via shared-edge cert SANs.
//
// Wildcard chains additionally carry the dynamic_host writer + DFP HTTP
// filter so cluster resolution sees the per-subdomain SNI.
func TestGenerateEnvoyConfig_TLSChainSNILock(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: ".mintlify.com", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	collectSNIKeys := func(t *testing.T, filters []any) []string {
		t.Helper()
		keys := []string{}
		for _, f := range filters {
			flt := f.(map[string]any)
			if flt["name"] != "envoy.filters.http.set_filter_state" {
				continue
			}
			tc := flt["typed_config"].(map[string]any)
			for _, e := range tc["on_request_headers"].([]any) {
				entry := e.(map[string]any)
				keys = append(keys, entry["object_key"].(string))
				fmtStr := entry["format_string"].(map[string]any)["text_format_source"].(map[string]any)["inline_string"].(string)
				assert.Equalf(t, "%REQUESTED_SERVER_NAME%", fmtStr, "filter-state %q must be sourced from SNI; any other source reopens the SNI/Host confused-deputy vector", entry["object_key"])
			}
		}
		return keys
	}

	// Exact chain — sni-lock + router. upstream_server_name and
	// upstream_subject_alt_names locked to SNI; no DFP (LOGICAL_DNS).
	exact := findTLSFilterChain(t, cfg, "api.anthropic.com")
	require.NotNil(t, exact)
	exactFilters := chainHCM(t, exact)["http_filters"].([]any)
	require.Len(t, exactFilters, 2, "exact TLS chain must have [sni-lock, router]")
	assert.Equal(t, "envoy.filters.http.set_filter_state", exactFilters[0].(map[string]any)["name"])
	assert.Equal(t, "envoy.filters.http.router", exactFilters[1].(map[string]any)["name"])
	assert.ElementsMatch(t,
		[]string{
			"envoy.network.upstream_server_name",
			"envoy.network.upstream_subject_alt_names",
		},
		collectSNIKeys(t, exactFilters),
		"exact chain SNI-lock must pin upstream_server_name + upstream_subject_alt_names",
	)

	// Wildcard chain — sni-lock + dynamic_host + DFP + router. All three
	// filter-state keys must be locked to SNI: dynamic_host for cluster
	// resolution, upstream_server_name + upstream_subject_alt_names so the
	// router can't fall back to Host header.
	wild := findTLSFilterChain(t, cfg, ".mintlify.com")
	require.NotNil(t, wild, "wildcard rule must produce a filter chain matched on the suffix SNI")
	wildFilters := chainHCM(t, wild)["http_filters"].([]any)
	require.Len(t, wildFilters, 4, "wildcard TLS chain must have [sni-lock, dynamic_host, dynamic_forward_proxy, router]")
	wildNames := []string{}
	for _, f := range wildFilters {
		wildNames = append(wildNames, f.(map[string]any)["name"].(string))
	}
	assert.Equal(t,
		[]string{
			"envoy.filters.http.set_filter_state",
			"envoy.filters.http.set_filter_state",
			"envoy.filters.http.dynamic_forward_proxy",
			"envoy.filters.http.router",
		},
		wildNames,
		"wildcard chain filter order: sni-lock → dynamic_host writer → DFP → router (sni-lock and dynamic_host writers must both precede DFP since dfp reads the filter state they write)",
	)
	assert.ElementsMatch(t,
		[]string{
			"envoy.upstream.dynamic_host",
			"envoy.network.upstream_server_name",
			"envoy.network.upstream_subject_alt_names",
		},
		collectSNIKeys(t, wildFilters),
		"wildcard chain must lock all three upstream-targeting filter-state keys to SNI",
	)

	dfpTC := wildFilters[2].(map[string]any)["typed_config"].(map[string]any)
	assert.Equal(t, true, dfpTC["allow_dynamic_host_from_filter_state"], "DFP filter must honor the dynamic_host filter state instead of :authority")
	require.NotNil(t, dfpTC["sub_cluster_config"], "DFP filter must reference sub_cluster_config to engage the per-host:port sub-clusters")
}

// TestGenerateEnvoyConfig_WildcardRoutesToWildcardCluster — the wildcard
// filter chain's routes must point at the wildcard DFP cluster, not the
// exact LOGICAL_DNS cluster. Mis-routing would defeat the whole fix: the
// route would still send subdomain requests through the apex-pinned cluster.
func TestGenerateEnvoyConfig_WildcardRoutesToWildcardCluster(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: ".mintlify.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: "mintlify.com", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	// Wildcard chain → wildcard cluster.
	wcChain := findTLSFilterChain(t, cfg, ".mintlify.com")
	require.NotNil(t, wcChain)
	wcRoutes := chainHCM(t, wcChain)["route_config"].(map[string]any)["virtual_hosts"].([]any)[0].(map[string]any)["routes"].([]any)
	wcRoute := wcRoutes[0].(map[string]any)["route"].(map[string]any)
	assert.Equal(t, "tls_wildcard_mintlify_com_443", wcRoute["cluster"])

	// Exact chain → exact cluster, unaffected by the wildcard sibling.
	exChain := findTLSFilterChain(t, cfg, "mintlify.com")
	require.NotNil(t, exChain, "exact rule must produce its own filter chain when no exact-sibling suppression applies")
	exRoutes := chainHCM(t, exChain)["route_config"].(map[string]any)["virtual_hosts"].([]any)[0].(map[string]any)["routes"].([]any)
	exRoute := exRoutes[0].(map[string]any)["route"].(map[string]any)
	assert.Equal(t, "tls_mintlify_com_443", exRoute["cluster"])
}

// TestGenerateEnvoyConfig_DownstreamHCMAllowsH2WebSocket — every TLS HCM
// must set http2_protocol_options.allow_connect=true so HTTP/2 downstream
// clients can run WebSocket via RFC 8441 extended CONNECT (instead of being
// forced down to HTTP/1.1 ALPN). Envoy translates the downstream extended
// CONNECT to upstream HTTP/1.1 RFC 6455 Upgrade automatically — the per-
// request ALPN override on the upgrade chain (verified by
// _TLSWebSocketUpgrade_AlpnForceH1) keeps upstream on h1.1 regardless of
// what upstream auto_config would negotiate.
func TestGenerateEnvoyConfig_DownstreamHCMAllowsH2WebSocket(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: ".mintlify.com", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	for _, sni := range []string{"api.anthropic.com", ".mintlify.com"} {
		chain := findTLSFilterChain(t, cfg, sni)
		require.NotNil(t, chain)
		hcm := chainHCM(t, chain)
		h2 := hcm["http2_protocol_options"].(map[string]any)
		assert.Equal(t, true, h2["allow_connect"], "%q HCM must set http2_protocol_options.allow_connect=true to enable h2 WebSocket (RFC 8441 extended CONNECT) for downstream clients", sni)
	}
}

// TestGenerateEnvoyConfig_TLSWebSocketUpgrade — WS upgrade_configs on TLS
// chains must replay every regular http_filter the trust boundary depends
// on, AND force upstream ALPN to h1.1 per-request. Upstream auto_config
// would otherwise negotiate h2 with capable upstreams, and Envoy would try
// h2 extended CONNECT — which fails on upstreams that don't implement
// RFC 8441 (ngrok edge in our test setup, plenty of origin servers). The
// ALPN override is the documented Envoy pattern for forcing per-request
// upstream HTTP version. It's still the recommended approach in 1.37+.
func TestGenerateEnvoyConfig_TLSWebSocketUpgrade(t *testing.T) {
	t.Parallel()

	rules := []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
		{Dst: ".mintlify.com", Proto: "https", Port: 443, Action: "allow"},
	}
	ports := EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}

	yamlBytes, _, err := GenerateEnvoyConfig(rules, ports, ALSConfig{})
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(yamlBytes, &cfg))

	// Filter-state writes contributed by each set_filter_state filter,
	// indexed by object_key → inline_string value. Built across all
	// on_request_headers entries of every set_filter_state filter.
	collect := func(t *testing.T, filters []any) (names []string, keyValues map[string]string) {
		t.Helper()
		keyValues = map[string]string{}
		for _, f := range filters {
			flt := f.(map[string]any)
			names = append(names, flt["name"].(string))
			if flt["name"] != "envoy.filters.http.set_filter_state" {
				continue
			}
			for _, e := range flt["typed_config"].(map[string]any)["on_request_headers"].([]any) {
				entry := e.(map[string]any)
				key := entry["object_key"].(string)
				val := entry["format_string"].(map[string]any)["text_format_source"].(map[string]any)["inline_string"].(string)
				keyValues[key] = val
			}
		}
		return names, keyValues
	}

	t.Run("exact rule", func(t *testing.T) {
		chain := findTLSFilterChain(t, cfg, "api.anthropic.com")
		require.NotNil(t, chain)
		ws := chainHCM(t, chain)["upgrade_configs"].([]any)[0].(map[string]any)
		assert.Equal(t, "websocket", ws["upgrade_type"])
		names, kv := collect(t, ws["filters"].([]any))
		assert.Equal(t,
			[]string{
				"envoy.filters.http.set_filter_state",
				"envoy.filters.http.set_filter_state",
				"envoy.filters.http.router",
			},
			names,
			"exact-rule WS upgrade chain: sni-lock + alpn-override + router (no DFP — exact uses LOGICAL_DNS)",
		)
		assert.Equal(t,
			map[string]string{
				"envoy.network.upstream_server_name":       "%REQUESTED_SERVER_NAME%",
				"envoy.network.upstream_subject_alt_names": "%REQUESTED_SERVER_NAME%",
				"envoy.network.application_protocols":      "http/1.1",
			},
			kv,
			"exact WS upgrade must SNI-lock upstream_server_name + upstream_subject_alt_names AND force upstream ALPN to http/1.1",
		)
	})

	t.Run("wildcard rule", func(t *testing.T) {
		chain := findTLSFilterChain(t, cfg, ".mintlify.com")
		require.NotNil(t, chain)
		ws := chainHCM(t, chain)["upgrade_configs"].([]any)[0].(map[string]any)
		assert.Equal(t, "websocket", ws["upgrade_type"])
		names, kv := collect(t, ws["filters"].([]any))
		assert.Equal(t,
			[]string{
				"envoy.filters.http.set_filter_state",
				"envoy.filters.http.set_filter_state",
				"envoy.filters.http.set_filter_state",
				"envoy.filters.http.dynamic_forward_proxy",
				"envoy.filters.http.router",
			},
			names,
			"wildcard WS upgrade chain: sni-lock + dynamic_host writer + alpn-override + DFP + router",
		)
		assert.Equal(t,
			map[string]string{
				"envoy.upstream.dynamic_host":              "%REQUESTED_SERVER_NAME%",
				"envoy.network.upstream_server_name":       "%REQUESTED_SERVER_NAME%",
				"envoy.network.upstream_subject_alt_names": "%REQUESTED_SERVER_NAME%",
				"envoy.network.application_protocols":      "http/1.1",
			},
			kv,
			"wildcard WS upgrade must lock dynamic_host + upstream_server_name + upstream_subject_alt_names to SNI AND force upstream ALPN to http/1.1",
		)
	})
}
