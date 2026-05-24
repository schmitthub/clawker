package firewall

import (
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"gopkg.in/yaml.v3"
)

// OpenTelemetry Access Log Service (ALS) wiring.
//
// Envoy ships access logs to the otel-collector via the native
// `envoy.access_loggers.open_telemetry` sink, which speaks OTLP
// LogsService over gRPC. Records arrive at the collector's standard
// OTLP/gRPC receiver tagged with `service.name=envoy` so the
// routing/logs_by_service connector can dispatch them to a dedicated
// OpenSearch index without colliding with claude-code or clawker-cp
// log shapes.
const otelCollectorALSClusterName = "otel_collector_als"

// ALSConfig configures the Envoy access logger's upstream cluster.
//
// When MTLS is true the cluster targets the otel-collector's
// mTLS-gated receiver on Port with an upstream TLS transport_socket:
// leaf+intermediate chain from /etc/envoy/otel-tls/client.pem, key
// from client.key, and the CLI root CA at ca.pem as the validation
// context. The receiver trusts only certs chained to the CLI root.
//
// When MTLS is false the OTel access-log sink and otel_collector_als
// cluster are omitted entirely. Envoy keeps the stdout JSON sink for
// `docker logs clawker-envoy` triage, but emits no OTLP — infra
// services must never cross into the untrusted otel-collector:4317
// lane reserved for agent containers.
type ALSConfig struct {
	Port int
	MTLS bool
}

// EnvoyPorts holds the port configuration for the Envoy proxy, sourced from config.Config.
type EnvoyPorts struct {
	EgressPort  int // Main egress listener — handles TLS (per-domain filter chains) and HTTP (raw_buffer filter chain).
	TCPPortBase int // Starting port for TCP/SSH listeners.
	HealthPort  int // Dedicated health check listener port for external probes.
}

// Validate checks that all ports are in valid range and no two ports collide.
func (p EnvoyPorts) Validate() error {
	named := []struct {
		name string
		port int
	}{
		{"EgressPort", p.EgressPort},
		{"TCPPortBase", p.TCPPortBase},
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

// TCPMapping describes a per-destination eBPF DNAT entry for non-TLS traffic.
// Each TCP/SSH rule gets a dedicated Envoy listener port.
type TCPMapping struct {
	Dst       string // Destination domain or IP.
	DstPort   int    // Original destination port (e.g. 22, 8080).
	EnvoyPort int    // Envoy listener port (TCPPortBase + index).
}

// TCPMappings computes TCP port mappings from egress rules.
// The result is deterministic for a given rule set — same rules produce same mappings.
// Used by both GenerateEnvoyConfig (to build listeners) and Enable (to build eBPF args).
func TCPMappings(rules []config.EgressRule, ports EnvoyPorts) []TCPMapping {
	var mappings []TCPMapping
	idx := 0
	for _, r := range rules {
		action := strings.ToLower(r.Action)
		if action != "allow" && action != "" {
			continue
		}
		if isIPOrCIDR(r.Dst) {
			continue
		}
		proto := strings.ToLower(r.Proto)
		if proto != "ssh" && proto != "tcp" {
			continue
		}
		// Dst is normalized to the canonical domain (no leading/trailing
		// dots) so that DomainHash(Dst) matches what the dnsbpf CoreDNS
		// plugin writes into dns_cache — the Corefile zones are already
		// normalized via normalizeDomain, so any leading-dot wildcard
		// marker on the raw rule Dst must be stripped here too or the
		// route_map lookup will miss for wildcard TCP/SSH rules.
		mappings = append(mappings, TCPMapping{
			Dst:       normalizeDomain(r.Dst),
			DstPort:   tcpDefaultPort(r),
			EnvoyPort: ports.TCPPortBase + idx,
		})
		idx++
	}
	return mappings
}

// Cert path formats inside the Envoy container (mounted volume).
const (
	envoyCertFileFmt = "/etc/envoy/certs/%s-cert.pem"
	envoyKeyFileFmt  = "/etc/envoy/certs/%s-key.pem"
)

// buildHTTPAccessLog returns Envoy access loggers for http_connection_manager
// contexts. Stdout (kept for `docker logs clawker-envoy` triage) is always
// emitted; the OpenTelemetry ALS sink is added only when als.MTLS is true.
// Without mTLS material we cannot reach the trusted otlp/infra receiver,
// and the untrusted otel-collector:4317 lane is reserved for agent
// containers — infra services must never cross into it.
//
// HTTP-specific fields (method, path, response_code, request_host) are only
// available when Envoy terminates HTTP. request_host captures the
// Host/:authority header, which is the only domain source for plaintext
// HTTP (where SNI/%REQUESTED_SERVER_NAME% is empty).
func buildHTTPAccessLog(tlsTerminated bool, action string, als ALSConfig) []any {
	extra := map[string]string{
		"method":                            "%REQ(:METHOD)%",
		"path":                              "%REQ(:PATH)%",
		"response_code":                     "%RESPONSE_CODE%",
		"response_code_details":             "%RESPONSE_CODE_DETAILS%",
		"request_host":                      "%REQ(Host)%",
		"user_agent":                        "%REQ(USER-AGENT)%",
		"req_duration_ms":                   "%REQUEST_DURATION%",
		"resp_duration_ms":                  "%RESPONSE_DURATION%",
		"resp_tx_duration_ms":               "%RESPONSE_TX_DURATION%",
		"upstream_transport_failure_reason": "%UPSTREAM_TRANSPORT_FAILURE_REASON%",
		"network.protocol.version":          "%PROTOCOL%",
	}
	tlsEst := "false"
	if tlsTerminated {
		tlsEst = "true"
	}
	sinks := []any{stdoutAccessLogEntry("tcp", "http", tlsEst, action, extra)}
	if als.MTLS {
		sinks = append(sinks, otelAccessLogEntry("tcp", "http", tlsEst, action, extra))
	}
	return sinks
}

// buildTCPAccessLog returns Envoy access loggers for tcp_proxy contexts.
// Stdout always; OpenTelemetry ALS only when als.MTLS is true (same
// trust-lane rationale as buildHTTPAccessLog). Omits HTTP fields (method,
// path, response_code) that are unavailable in TCP proxy — used by deny
// and TCP/SSH listeners. Optional domain overrides %REQUESTED_SERVER_NAME%
// for raw TCP where SNI is unavailable.
//
// `action` is a clawker-internal literal stamped at config generation:
// uniform-verdict TCP filter chains hardcode it ("denied" for deny_cluster,
// "allowed" for per-rule TCP/SSH listeners). It carries the firewall
// decision in the dedicated `action` field, never overloaded into `proto`.
func buildTCPAccessLog(l7Proto, action string, als ALSConfig, domain ...string) []any {
	var extra map[string]string
	if len(domain) > 0 && domain[0] != "" {
		extra = map[string]string{"domain": domain[0]}
	}
	sinks := []any{stdoutAccessLogEntry("tcp", l7Proto, "false", action, extra)}
	if als.MTLS {
		sinks = append(sinks, otelAccessLogEntry("tcp", l7Proto, "false", action, extra))
	}
	return sinks
}

// firewallBlockedBody is the response body returned for every clawker-firewall
// direct_response 403 route (SNI-block deny_all virtual host, per-path-rule
// deny, default unmatched path deny). Generic and non-fingerprinting — an
// injected-prompt adversary should not be able to trivially distinguish a
// clawker firewall block from a generic upstream "Forbidden". The firewall
// verdict travels via the `action` access log field (route metadata +
// %METADATA(ROUTE:clawker:action)% substitution), never via the response
// body. Centralized here so all three direct_response sites stay in sync.
const firewallBlockedBody = "Forbidden\n"

// httpConnectionManagerHardening returns the edge-hardening field map shared
// by every clawker HTTP connection manager (TLS and plaintext HTTP filter
// chains). Critical for path-rule security: without `normalize_path` +
// `merge_slashes` + `path_with_escaped_slashes_action`, URL-encoded
// traversal (e.g. `/anchore/syft/main/%2e%2e/%2e%2e/torvalds/linux/...`)
// bypasses the route's literal-prefix matcher and forwards upstream — the
// verified path-smuggling exploit documented in plan
// compressed-floating-matsumoto.md §4.
//
// Field-by-field rationale (each cited against Envoy best_practices/edge
// + the http_connection_manager.proto + protocol.proto API references):
//   - normalize_path / merge_slashes / path_with_escaped_slashes_action:
//     RFC 3986 normalization BEFORE route matching closes the smuggling
//     vector. UNESCAPE_AND_REDIRECT issues a 307 with the canonical path
//     instead of silently rewriting, so the matcher sees what the agent
//     actually sent.
//   - request_timeout + stream_idle_timeout + idle_timeout: slow-loris
//     mitigation. Without these one slow connection per agent can pin
//     Envoy worker resources.
//   - headers_with_underscores_action: REJECT_REQUEST: defends against
//     RFC 9110 §5.4.5 header-name aliasing (`X_AUTH` vs `X-AUTH`).
//   - http2_protocol_options.max_concurrent_streams: h2 amplification
//     cap; default is conservative for forward-proxy threat model.
//   - per_connection_buffer_limit_bytes: DoS resistance for slowloris-
//     style attacks that grow the per-connection buffer.
//
// Applied via maps.Copy at HCM construction sites — keeps each HCM literal
// readable while ensuring no site forgets a hardening field.
func httpConnectionManagerHardening() map[string]any {
	return map[string]any{
		"normalize_path":                   true,
		"merge_slashes":                    true,
		"path_with_escaped_slashes_action": "UNESCAPE_AND_REDIRECT",
		"request_timeout":                  "30s",
		"stream_idle_timeout":              "300s",
		"common_http_protocol_options": map[string]any{
			"idle_timeout":                    "300s",
			"headers_with_underscores_action": "REJECT_REQUEST",
		},
		"http2_protocol_options": map[string]any{
			"max_concurrent_streams": 100,
		},
	}
}

// clawkerActionMetadata returns a route-level metadata block whose
// filter_metadata.clawker.action is read by the HTTP access log via
// %METADATA(ROUTE:clawker:action)% substitution. Every route literal in
// the route table MUST carry this so Envoy stamps the correct action
// per-record at emit time. `action` is a Go literal ("allowed" / "denied")
// — never a runtime computation — exactly the same generation-time model
// as `proto`/`action` hardcoded at TCP filter chain access loggers.
func clawkerActionMetadata(action string) map[string]any {
	return map[string]any{
		"filter_metadata": map[string]any{
			"clawker": map[string]any{"action": action},
		},
	}
}

// accessLogFields returns the canonical field map shared between the
// stdout JSON sink and the OpenTelemetry ALS attributes. Keeping a single
// source of truth means renaming a field updates both sinks at once.
//
// `action` carries the clawker firewall decision (`allowed` / `denied`),
// stamped at config generation time. For uniform-verdict filter chains
// (deny_cluster, TCP/SSH listeners) the call site passes a literal; for
// mixed-verdict HTTP filter chains (one HCM serves both allow + deny
// routes) the call site passes the literal substitution token
// `%METADATA(ROUTE:clawker:action)%` so Envoy copies the per-route
// metadata value into the log line at emit time. The field is NEVER
// inferred from response_code, response_flags, or any downstream-of-
// routing signal — see plan compressed-floating-matsumoto.md for the
// concrete-at-emit-time rationale.
//
// `proto` reflects the actual L4 protocol the filter chain processes
// (`tls`/`http`/`tcp`/`ssh`). It does NOT carry the verdict — the
// pre-rename `proto: "deny"` overload that conflated proto with action
// is gone.
//
// `extra` carries context-specific overrides (HTTP method/path/code, or
// the static domain for raw TCP). Nil-safe.
func accessLogFields(transport, l7Proto, tlsEstablished, action string, extra map[string]string) map[string]string {
	f := map[string]string{
		"domain":                  "%REQUESTED_SERVER_NAME%",
		"client_ip":               "%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%",
		"listener_ip":             "%DOWNSTREAM_LOCAL_ADDRESS_WITHOUT_PORT%",
		"upstream_ip":             "%UPSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%",
		"upstream_port":           "%UPSTREAM_REMOTE_PORT%",
		"response_flags":          "%RESPONSE_FLAGS%",
		"bytes_sent":              "%BYTES_SENT%",
		"bytes_received":          "%BYTES_RECEIVED%",
		"upstream_bytes_sent":     "%UPSTREAM_WIRE_BYTES_SENT%",
		"upstream_bytes_received": "%UPSTREAM_WIRE_BYTES_RECEIVED%",
		"duration_ms":             "%DURATION%",
		"tls.protocol.version":    "%DOWNSTREAM_TLS_VERSION%",
		"tls.cipher":              "%DOWNSTREAM_TLS_CIPHER%",
		"upstream_tls_version":    "%UPSTREAM_TLS_VERSION%",
		"upstream_tls_cipher":     "%UPSTREAM_TLS_CIPHER%",
		"network.transport":       transport,
		"network.protocol.name":   l7Proto,
		"tls.established":         tlsEstablished,
		"action":                  action,
	}
	maps.Copy(f, extra)
	return f
}

// stdoutAccessLogEntry builds the legacy JSON-formatted stdout access log
// entry. Surfaces in `docker logs clawker-envoy` for triage when the otel
// pipeline is misconfigured or the monitoring stack is down.
func stdoutAccessLogEntry(transport, l7Proto, tlsEstablished, action string, extra map[string]string) map[string]any {
	fields := accessLogFields(transport, l7Proto, tlsEstablished, action, extra)
	jf := make(map[string]any, len(fields))
	for k, v := range fields {
		jf[k] = v
	}
	return map[string]any{
		"name": "envoy.access_loggers.stdout",
		"typed_config": map[string]any{
			"@type":      "type.googleapis.com/envoy.extensions.access_loggers.stream.v3.StdoutAccessLog",
			"log_format": map[string]any{"json_format": jf},
		},
	}
}

// otelAccessLogEntry builds the OpenTelemetry ALS access log entry that
// streams records to the otel-collector as OTLP/gRPC log records. Resource
// attribute `service.name=envoy` is stamped on every record so the
// collector's routing/logs_by_service connector can dispatch envoy logs to
// a dedicated OpenSearch index without colliding with claude-code or
// clawker-cp shapes.
//
// Body holds a short human-readable description; structured fields land in
// `attributes` so downstream filters in OpenSearch Dashboards can pivot on
// them without parsing the body string.
func otelAccessLogEntry(transport, l7Proto, tlsEstablished, action string, extra map[string]string) map[string]any {
	fields := accessLogFields(transport, l7Proto, tlsEstablished, action, extra)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	values := make([]any, 0, len(keys))
	for _, k := range keys {
		values = append(values, map[string]any{
			"key":   k,
			"value": map[string]any{"string_value": fields[k]},
		})
	}
	return map[string]any{
		"name": "envoy.access_loggers.open_telemetry",
		"typed_config": map[string]any{
			"@type": "type.googleapis.com/envoy.extensions.access_loggers.open_telemetry.v3.OpenTelemetryAccessLogConfig",
			"grpc_service": map[string]any{
				"envoy_grpc": map[string]any{
					"cluster_name": otelCollectorALSClusterName,
				},
			},
			"resource_attributes": map[string]any{
				"values": []any{
					map[string]any{
						"key":   "service.name",
						"value": map[string]any{"string_value": "envoy"},
					},
				},
			},
			"body": map[string]any{
				"string_value": "envoy access_log",
			},
			"attributes": map[string]any{"values": values},
		},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Cluster naming
// ──────────────────────────────────────────────────────────────────────────────

// tlsClusterName returns the per-domain, per-port cluster name for TLS upstream.
// Each (domain, port) pair gets its own LOGICAL_DNS cluster with upstream TLS re-encryption.
// Port is included in the name so that rules for the same domain on different ports
// (e.g., example.com:443 and example.com:8443) get separate clusters.
func tlsClusterName(domain string, port int) string {
	return fmt.Sprintf("tls_%s_%d", sanitizeName(domain), port)
}

// httpClusterName returns the per-domain, per-port cluster name for plaintext HTTP upstream.
func httpClusterName(domain string, port int) string {
	return fmt.Sprintf("http_%s_%d", sanitizeName(domain), port)
}

// ──────────────────────────────────────────────────────────────────────────────
// Top-level config generator
// ──────────────────────────────────────────────────────────────────────────────

// GenerateEnvoyConfig produces an Envoy static bootstrap YAML from egress rules.
// Returns the YAML bytes and a list of warnings (non-fatal issues).
func GenerateEnvoyConfig(rules []config.EgressRule, ports EnvoyPorts, als ALSConfig) ([]byte, []string, error) {
	if err := ports.Validate(); err != nil {
		return nil, nil, err
	}

	var warnings []string

	// Classify rules.
	var (
		tlsRules  []config.EgressRule
		tcpRules  []config.EgressRule
		httpRules []config.EgressRule
	)
	for _, r := range rules {
		action := strings.ToLower(r.Action)
		if action != "allow" && action != "" {
			continue // Only allow rules generate proxy config; deny/unknown handled by default deny chain.
		}
		if isIPOrCIDR(r.Dst) {
			warnings = append(warnings, fmt.Sprintf("skipping IP/CIDR rule %q (not supported in Envoy proxy)", r.Dst))
			continue
		}
		proto := strings.ToLower(r.Proto)
		if proto == "ssh" || proto == "tcp" {
			tcpRules = append(tcpRules, r)
			continue
		}
		// proto: "http" (default after NormalizeRule, ex-"tls") and any other
		// unknown L7 name route to the TLS-MITM HCM filter chain. Envoy
		// terminates TLS with a per-domain certificate, inspects HTTP (paths
		// visible in access logs), then re-encrypts upstream. Rules with
		// PathRules get per-path routing; rules without get allow-all.
		if r.Port == 0 {
			r.Port = 443
		}
		tlsRules = append(tlsRules, r)
	}

	// Build per-listener exact-domain sets so wildcard filter chains only
	// omit the apex when a same-listener exact rule handles it. Without
	// per-listener separation, an exact HTTP rule for "example.com" would
	// wrongly suppress the apex from a TLS wildcard ".example.com".
	tlsExactDomains := make(map[string]bool)
	for _, r := range tlsRules {
		if !isWildcardDomain(r.Dst) {
			tlsExactDomains[normalizeDomain(r.Dst)] = true
		}
	}
	httpExactDomains := make(map[string]bool)
	for _, r := range httpRules {
		if !isWildcardDomain(r.Dst) {
			httpExactDomains[normalizeDomain(r.Dst)] = true
		}
	}

	// Compute TCP port mappings (same function used by manager.Enable for eBPF args).
	tcpMappings := TCPMappings(rules, ports)

	// Validate derived TCP listener ports: range check + collision detection.
	if len(tcpMappings) > 0 {
		reservedPorts := map[int]string{
			ports.EgressPort: "EgressPort",
			ports.HealthPort: "HealthPort",
			9901:             "EnvoyAdmin",
		}
		for _, m := range tcpMappings {
			if m.EnvoyPort > 65535 {
				return nil, nil, fmt.Errorf(
					"TCP rule %s:%d would use port %d which exceeds 65535 (TCPPortBase=%d, %d TCP rules)",
					m.Dst, m.DstPort, m.EnvoyPort, ports.TCPPortBase, len(tcpMappings))
			}
			if conflict, ok := reservedPorts[m.EnvoyPort]; ok {
				return nil, nil, fmt.Errorf(
					"TCP rule %s:%d would use port %d which collides with %s (TCPPortBase=%d)",
					m.Dst, m.DstPort, m.EnvoyPort, conflict, ports.TCPPortBase)
			}
			reservedPorts[m.EnvoyPort] = fmt.Sprintf("TCP:%s:%d", m.Dst, m.DstPort)
		}
	}

	cfg := map[string]any{
		"admin": map[string]any{
			"address": map[string]any{
				"socket_address": map[string]any{
					"address":    "127.0.0.1",
					"port_value": 9901,
				},
			},
		},
		"static_resources": map[string]any{
			"listeners": buildListeners(tlsRules, tcpRules, httpRules, tcpMappings, ports, tlsExactDomains, httpExactDomains, als),
			"clusters":  buildClusters(tlsRules, tcpRules, httpRules, als),
		},
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal envoy config: %w", err)
	}
	return out, warnings, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Listeners
// ──────────────────────────────────────────────────────────────────────────────

// buildListeners constructs all Envoy listeners.
func buildListeners(tls, tcp, http []config.EgressRule, tcpMappings []TCPMapping, ports EnvoyPorts, tlsExactDomains, httpExactDomains map[string]bool, als ALSConfig) []any {
	var listeners []any

	// Main egress listener — handles TLS (per-domain filter chains with SNI matching)
	// and plaintext HTTP (raw_buffer filter chain with Host header routing).
	// tls_inspector differentiates TLS from plaintext at the listener level.
	if len(tls) > 0 || len(http) > 0 {
		listeners = append(listeners, buildEgressListener(tls, http, ports.EgressPort, tlsExactDomains, httpExactDomains, als))
	}

	// Per-rule TCP/SSH listeners from the port mappings.
	for i, r := range tcp {
		if i < len(tcpMappings) {
			listeners = append(listeners, buildTCPListener(r, tcpMappings[i].EnvoyPort, als))
		}
	}

	// Dedicated health check listener — published for host-side probes.
	// Kept on a separate port so the TLS listener (10000) is never published,
	// avoiding Docker's port-publish NAT rules that can masquerade source IPs.
	if ports.HealthPort > 0 {
		listeners = append(listeners, buildHealthListener(ports.HealthPort))
	}

	return listeners
}

// buildHealthListener creates a lightweight HTTP listener that returns 200 OK
// for health probes. This is the only port published to the host — keeping
// traffic ports unpublished preserves source IPs for per-agent attribution.
func buildHealthListener(port int) map[string]any {
	return map[string]any{
		"name": "health_check",
		"address": map[string]any{
			"socket_address": map[string]any{
				"address":    "0.0.0.0",
				"port_value": port,
			},
		},
		"filter_chains": []any{
			map[string]any{
				"filters": []any{
					map[string]any{
						"name": "envoy.filters.network.http_connection_manager",
						"typed_config": map[string]any{
							"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
							"stat_prefix": "health_check",
							"route_config": map[string]any{
								"virtual_hosts": []any{
									map[string]any{
										"name":    "health",
										"domains": []any{"*"},
										"routes": []any{
											map[string]any{
												"match":    map[string]any{"prefix": "/"},
												"metadata": clawkerActionMetadata("allowed"),
												"direct_response": map[string]any{
													"status": 200,
													"body":   map[string]any{"inline_string": "ok"},
												},
											},
										},
									},
								},
							},
							"http_filters": []any{routerFilter()},
						},
					},
				},
			},
		},
	}
}

// buildEgressListener creates the main egress listener that handles both TLS and
// plaintext HTTP traffic on a single port. The tls_inspector listener filter
// differentiates protocols: TLS connections match per-domain filter chains (SNI),
// plaintext HTTP matches a raw_buffer filter chain (Host header routing).
// Unmatched traffic hits the deny chain (connection reset).
func buildEgressListener(tlsRules, httpRules []config.EgressRule, port int, tlsExactDomains, httpExactDomains map[string]bool, als ALSConfig) map[string]any {
	var filterChains []any

	// Per-domain TLS filter chains (matched by SNI via tls_inspector).
	for _, r := range tlsRules {
		filterChains = append(filterChains, buildTLSFilterChain(r, tlsExactDomains, als))
	}

	// Plaintext HTTP filter chain (matched by transport_protocol: "raw_buffer").
	// Handles all proto: http rules via Host header routing on this same port.
	if len(httpRules) > 0 {
		filterChains = append(filterChains, buildHTTPFilterChain(httpRules, httpExactDomains, als))
	}

	// Default deny chain (catch-all → connection reset).
	filterChains = append(filterChains, buildDenyFilterChain(als))

	return map[string]any{
		"name": "egress",
		"address": map[string]any{
			"socket_address": map[string]any{
				"address":    "0.0.0.0",
				"port_value": port,
			},
		},
		// per_connection_buffer_limit_bytes is a Listener field (not HCM —
		// envoy.config.listener.v3.Listener). Caps the per-connection read
		// buffer so a slowloris-style agent can't grow Envoy's memory by
		// dribbling bytes on N parked connections.
		"per_connection_buffer_limit_bytes": 32768,
		"listener_filters": []any{
			map[string]any{
				"name": "envoy.filters.listener.tls_inspector",
				"typed_config": map[string]any{
					"@type": "type.googleapis.com/envoy.extensions.filters.listener.tls_inspector.v3.TlsInspector",
				},
			},
		},
		"filter_chains": filterChains,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// HTTP filter chain (plaintext)
// ──────────────────────────────────────────────────────────────────────────────

// buildHTTPFilterChain creates a filter chain for plaintext HTTP traffic on the
// egress listener. Matched by transport_protocol: "raw_buffer" (the tls_inspector
// sets this for non-TLS connections). Uses Host header for domain authorization
// and routes to per-domain LOGICAL_DNS clusters.
func buildHTTPFilterChain(rules []config.EgressRule, exactDomains map[string]bool, als ALSConfig) map[string]any {
	var virtualHosts []any

	for _, r := range rules {
		domain := normalizeDomain(r.Dst)
		cluster := httpClusterName(domain, r.Port)

		var routes []any
		if len(r.PathRules) > 0 {
			routes = buildHTTPRoutes(r, cluster)
		} else {
			routes = []any{
				map[string]any{
					"match":    map[string]any{"prefix": "/"},
					"metadata": clawkerActionMetadata("allowed"),
					"route":    map[string]any{"cluster": cluster, "timeout": "0s", "upgrade_configs": []any{map[string]any{"upgrade_type": "websocket"}}},
				},
			}
		}

		virtualHosts = append(virtualHosts, map[string]any{
			"name":    virtualHostName(r.Dst),
			"domains": httpDomains(r.Dst, exactDomains),
			"routes":  routes,
		})
	}

	// Default deny for unknown domains.
	virtualHosts = append(virtualHosts, map[string]any{
		"name":    "deny_all",
		"domains": []string{"*"},
		"routes": []any{
			map[string]any{
				"match":    map[string]any{"prefix": "/"},
				"metadata": clawkerActionMetadata("denied"),
				"direct_response": map[string]any{
					"status": 403,
					"body":   map[string]any{"inline_string": firewallBlockedBody},
				},
			},
		},
	})

	return map[string]any{
		"filter_chain_match": map[string]any{
			"transport_protocol": "raw_buffer",
		},
		"filters": []any{
			map[string]any{
				"name":         "envoy.filters.network.http_connection_manager",
				"typed_config": plaintextHCMTypedConfig(virtualHosts, als),
			},
		},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// TLS filter chain (per-domain MITM)
// ──────────────────────────────────────────────────────────────────────────────

// buildTLSFilterChain creates a filter chain that terminates TLS with a per-domain
// certificate, inspects HTTP traffic, then forwards to a per-domain LOGICAL_DNS
// cluster that re-encrypts upstream. The upstream destination is determined by the
// cluster endpoint (domain:port), NOT by the HTTP Host header — this prevents
// confused deputy attacks where a malicious client manipulates Host to redirect traffic.
func buildTLSFilterChain(r config.EgressRule, exactDomains map[string]bool, als ALSConfig) map[string]any {
	domain := normalizeDomain(r.Dst)
	certFile := fmt.Sprintf(envoyCertFileFmt, domain)
	keyFile := fmt.Sprintf(envoyKeyFileFmt, domain)
	cluster := tlsClusterName(domain, r.Port)

	// Build routes: path rules when configured, otherwise allow-all.
	// Each domain routes to its own per-domain LOGICAL_DNS cluster.
	var routes []any
	if len(r.PathRules) > 0 {
		routes = buildHTTPRoutes(r, cluster)
	} else {
		routes = []any{
			map[string]any{
				"match":    map[string]any{"prefix": "/"},
				"metadata": clawkerActionMetadata("allowed"),
				"route":    map[string]any{"cluster": cluster, "timeout": "0s", "upgrade_configs": []any{map[string]any{"upgrade_type": "websocket"}}},
			},
		}
	}

	return map[string]any{
		"filter_chain_match": map[string]any{
			"server_names": serverNames(r.Dst, exactDomains),
		},
		"transport_socket": map[string]any{
			"name": "envoy.transport_sockets.tls",
			"typed_config": map[string]any{
				"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext",
				"common_tls_context": map[string]any{
					"alpn_protocols": []string{"h2", "http/1.1"},
					"tls_certificates": []any{
						map[string]any{
							"certificate_chain": map[string]any{"filename": certFile},
							"private_key":       map[string]any{"filename": keyFile},
						},
					},
				},
			},
		},
		"filters": []any{
			map[string]any{
				"name":         "envoy.filters.network.http_connection_manager",
				"typed_config": tlsHCMTypedConfig(r, domain, exactDomains, routes, als),
			},
		},
	}
}

// plaintextHCMTypedConfig + tlsHCMTypedConfig build the HCM `typed_config`
// map for the plaintext-HTTP and TLS-terminating filter chains. Both end by
// merging in httpConnectionManagerHardening() so no HCM construction site
// can forget the edge-hardening defaults. Split into helpers (vs inlining)
// purely so the maps.Copy(hardening) call is shared — every clawker HCM
// MUST carry the hardening set.
func plaintextHCMTypedConfig(virtualHosts []any, als ALSConfig) map[string]any {
	tc := map[string]any{
		"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
		"stat_prefix": "http_egress",
		"codec_type":  "AUTO",
		"access_log":  buildHTTPAccessLog(false, "%METADATA(ROUTE:clawker:action)%", als),
		// Plaintext HTTP — no upstream TLS, no ALPN override needed.
		// Standard HCM-level upgrade is sufficient per envoy-ws.yaml example.
		"upgrade_configs": []any{
			map[string]any{"upgrade_type": "websocket"},
		},
		"route_config": map[string]any{
			"name":          "http_egress_routes",
			"virtual_hosts": virtualHosts,
		},
		"http_filters": []any{routerFilter()},
	}
	maps.Copy(tc, httpConnectionManagerHardening())
	return tc
}

func tlsHCMTypedConfig(r config.EgressRule, domain string, exactDomains map[string]bool, routes []any, als ALSConfig) map[string]any {
	tc := map[string]any{
		"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
		"stat_prefix": fmt.Sprintf("tls_%s", sanitizeName(domain)),
		"codec_type":  "AUTO",
		"access_log":  buildHTTPAccessLog(true, "%METADATA(ROUTE:clawker:action)%", als),
		// TLS upstream uses auto_config (H2+H1.1 ALPN). WebSocket Upgrade
		// doesn't exist in HTTP/2, so the upgrade filter chain overrides
		// ALPN to force HTTP/1.1 on the upstream TLS handshake.
		"upgrade_configs": buildTLSWebSocketUpgrade(),
		"route_config": map[string]any{
			"name": fmt.Sprintf("tls_route_%s", sanitizeName(domain)),
			"virtual_hosts": []any{
				map[string]any{
					"name":    virtualHostName(r.Dst),
					"domains": httpDomains(r.Dst, exactDomains),
					"routes":  routes,
				},
			},
		},
		"http_filters": []any{routerFilter()},
	}
	maps.Copy(tc, httpConnectionManagerHardening())
	return tc
}

// ──────────────────────────────────────────────────────────────────────────────
// Shared route and filter builders
// ──────────────────────────────────────────────────────────────────────────────

// routerFilter returns the standard Envoy router HTTP filter.
func routerFilter() map[string]any {
	return map[string]any{
		"name": "envoy.filters.http.router",
		"typed_config": map[string]any{
			"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router",
		},
	}
}

// buildHTTPRoutes converts path rules into Envoy route entries.
// clusterName determines the upstream cluster — per-domain LOGICAL_DNS for both
// TLS (re-encrypts upstream) and HTTP (plaintext upstream) filter chains.
func buildHTTPRoutes(r config.EgressRule, clusterName string) []any {
	var routes []any

	wsUpgrade := []any{map[string]any{"upgrade_type": "websocket"}}

	// Explicit path rules first. Each route literal carries
	// metadata.filter_metadata.clawker.action — the access log
	// (%METADATA(ROUTE:clawker:action)%) reads this at emit time so the
	// firewall verdict is concrete per record (never inferred from
	// response_code). See clawkerActionMetadata + plan
	// compressed-floating-matsumoto.md.
	for _, pr := range r.PathRules {
		if strings.EqualFold(pr.Action, "allow") {
			routes = append(routes, map[string]any{
				"match":    map[string]any{"prefix": pr.Path},
				"metadata": clawkerActionMetadata("allowed"),
				"route":    map[string]any{"cluster": clusterName, "timeout": "0s", "upgrade_configs": wsUpgrade},
			})
		} else {
			routes = append(routes, map[string]any{
				"match":    map[string]any{"prefix": pr.Path},
				"metadata": clawkerActionMetadata("denied"),
				"direct_response": map[string]any{
					"status": 403,
					"body":   map[string]any{"inline_string": firewallBlockedBody},
				},
			})
		}
	}

	// Default action for unmatched paths. EffectivePathDefault honors an
	// explicit r.PathDefault override and otherwise infers from the path_rules
	// composition — see rules_store.go for the inference rule.
	pathDefault := strings.ToLower(EffectivePathDefault(r))
	if pathDefault == "deny" {
		routes = append(routes, map[string]any{
			"match":    map[string]any{"prefix": "/"},
			"metadata": clawkerActionMetadata("denied"),
			"direct_response": map[string]any{
				"status": 403,
				"body":   map[string]any{"inline_string": firewallBlockedBody},
			},
		})
	} else {
		routes = append(routes, map[string]any{
			"match":    map[string]any{"prefix": "/"},
			"metadata": clawkerActionMetadata("allowed"),
			"route":    map[string]any{"cluster": clusterName, "timeout": "0s", "upgrade_configs": wsUpgrade},
		})
	}

	return routes
}

// buildTLSWebSocketUpgrade returns the HCM upgrade_configs entry for WebSocket
// on TLS filter chains. The custom filter chain overrides ALPN to HTTP/1.1 for
// the upstream TLS handshake — necessary because standard WebSocket upgrades use
// the HTTP/1.1 Upgrade header mechanism which does not exist in HTTP/2.
// Regular (non-upgrade) requests continue to use the cluster's auto_config
// ALPN and get HTTP/2 when available.
//
// Ref: https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/http/upgrades
func buildTLSWebSocketUpgrade() []any {
	return []any{
		map[string]any{
			"upgrade_type": "websocket",
			"filters": []any{
				// Force HTTP/1.1 ALPN for upstream TLS connection.
				map[string]any{
					"name": "envoy.filters.http.set_filter_state",
					"typed_config": map[string]any{
						"@type": "type.googleapis.com/envoy.extensions.filters.http.set_filter_state.v3.Config",
						"on_request_headers": []any{
							map[string]any{
								"object_key": "envoy.network.application_protocols",
								"format_string": map[string]any{
									"text_format_source": map[string]any{
										"inline_string": "http/1.1",
									},
								},
							},
						},
					},
				},
				routerFilter(),
			},
		},
	}
}

// buildDenyFilterChain creates the default deny filter chain that resets connections.
// Access logging with proto="deny" so blocked connection attempts are visible in the
// egress monitoring dashboard alongside allowed traffic.
func buildDenyFilterChain(als ALSConfig) map[string]any {
	return map[string]any{
		"filter_chain_match": map[string]any{},
		"filters": []any{
			map[string]any{
				"name": "envoy.filters.network.tcp_proxy",
				"typed_config": map[string]any{
					"@type":       "type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy",
					"stat_prefix": "deny_all",
					"cluster":     "deny_cluster",
					// idle_timeout omitted — Envoy default (1h) applies.
					// The previous explicit "0s" disabled the timeout
					// entirely, which the tcp_proxy.proto docs warn yields
					// connection leaks ("Disabling this timeout is likely
					// to yield connection leaks"). A hostile agent opening
					// N never-closed TCP connections to a blocked SNI
					// would exhaust Envoy's per-process FD/socket budget.
					// Verified DoS surface 2026-05-23.
					"access_log": buildTCPAccessLog("", "denied", als),
				},
			},
		},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// TCP/SSH listeners
// ──────────────────────────────────────────────────────────────────────────────

// tcpDefaultPort returns the effective destination port for a TCP/SSH rule.
func tcpDefaultPort(r config.EgressRule) int {
	if r.Port != 0 {
		return r.Port
	}
	if strings.EqualFold(r.Proto, "ssh") {
		return 22
	}
	return 443
}

// tcpClusterName returns the Envoy cluster name for a TCP/SSH rule.
func tcpClusterName(r config.EgressRule) string {
	return fmt.Sprintf("tcp_%s_%d", sanitizeName(r.Dst), tcpDefaultPort(r))
}

// buildTCPListener creates a TCP/SSH listener on the given port.
func buildTCPListener(r config.EgressRule, port int, als ALSConfig) map[string]any {
	clusterName := tcpClusterName(r)

	return map[string]any{
		"name": clusterName,
		"address": map[string]any{
			"socket_address": map[string]any{
				"address":    "0.0.0.0",
				"port_value": port,
			},
		},
		"filter_chains": []any{
			map[string]any{
				"filters": []any{
					map[string]any{
						"name": "envoy.filters.network.tcp_proxy",
						"typed_config": map[string]any{
							"@type":       "type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy",
							"stat_prefix": clusterName,
							"cluster":     clusterName,
							"access_log":  buildTCPAccessLog(r.Proto, "allowed", als, r.Dst),
						},
					},
				},
			},
		},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Clusters
// ──────────────────────────────────────────────────────────────────────────────

// buildClusters constructs all Envoy clusters.
func buildClusters(tls, tcp, http []config.EgressRule, als ALSConfig) []any {
	clusters := []any{
		// Deny cluster (no endpoints — connection reset).
		map[string]any{
			"name":            "deny_cluster",
			"connect_timeout": "1s",
			"type":            "STATIC",
			"load_assignment": map[string]any{
				"cluster_name": "deny_cluster",
				"endpoints":    []any{},
			},
		},
	}
	// OpenTelemetry Access Log Service sink + cluster only emitted when
	// mTLS material is wired. In degraded mode the access loggers omit
	// the OTel entry (buildHTTPAccessLog / buildTCPAccessLog gate on
	// als.MTLS), so the cluster has no referrers — leaving it would only
	// fail per-record dials to the untrusted lane, which infra services
	// must never touch. STRICT_DNS resolves `otel-collector` (clawker-net
	// DNS) on every refresh; when the monitoring stack is down the gRPC
	// dial fails per-record and Envoy drops oldest from the access
	// logger's default 16KB buffer — never blocks the data path.
	if als.MTLS {
		clusters = append(clusters, buildOtelALSCluster(als))
	}

	// Per-(domain, port) TLS clusters — LOGICAL_DNS with upstream re-encryption.
	// Separate clusters per (domain, port) pair maintain isolated connection pools and
	// allow the same domain on different ports (e.g., example.com:443 and example.com:8443).
	seen := make(map[string]bool)
	for _, r := range tls {
		domain := normalizeDomain(r.Dst)
		key := fmt.Sprintf("%s:%d", domain, r.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		clusters = append(clusters, buildTLSDNSCluster(domain, r.Port))
	}

	// Per-(domain, port) HTTP clusters — LOGICAL_DNS without TLS.
	httpSeen := make(map[string]bool)
	for _, r := range http {
		domain := normalizeDomain(r.Dst)
		key := fmt.Sprintf("%s:%d", domain, r.Port)
		if httpSeen[key] {
			continue
		}
		httpSeen[key] = true
		clusters = append(clusters, buildHTTPDNSCluster(domain, r.Port))
	}

	// Per-destination TCP clusters.
	for _, r := range tcp {
		name := tcpClusterName(r)
		clusters = append(clusters, map[string]any{
			"name":              name,
			"connect_timeout":   "5s",
			"type":              "LOGICAL_DNS",
			"dns_lookup_family": "V4_ONLY",
			"load_assignment": map[string]any{
				"cluster_name": name,
				"endpoints": []any{
					map[string]any{
						"lb_endpoints": []any{
							map[string]any{
								"endpoint": map[string]any{
									"address": map[string]any{
										"socket_address": map[string]any{
											"address":    r.Dst,
											"port_value": tcpDefaultPort(r),
										},
									},
								},
							},
						},
					},
				},
			},
		})
	}

	return clusters
}

// buildTLSDNSCluster creates a per-domain LOGICAL_DNS cluster with upstream TLS.
// The cluster endpoint is the domain name itself — Envoy resolves it via DNS and
// connects to the result. auto_sni derives the TLS SNI from the endpoint hostname,
// auto_san_validation validates the upstream certificate against it.
// auto_config enables HTTP/2 when the upstream supports it via ALPN negotiation.
func buildTLSDNSCluster(domain string, port int) map[string]any {
	return map[string]any{
		"name":              tlsClusterName(domain, port),
		"connect_timeout":   "10s",
		"type":              "LOGICAL_DNS",
		"dns_lookup_family": "V4_ONLY",
		"load_assignment": map[string]any{
			"cluster_name": tlsClusterName(domain, port),
			"endpoints": []any{
				map[string]any{
					"lb_endpoints": []any{
						map[string]any{
							"endpoint": map[string]any{
								"address": map[string]any{
									"socket_address": map[string]any{
										"address":    domain,
										"port_value": port,
									},
								},
							},
						},
					},
				},
			},
		},
		"transport_socket": map[string]any{
			"name": "envoy.transport_sockets.tls",
			"typed_config": map[string]any{
				"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext",
				"common_tls_context": map[string]any{
					"alpn_protocols": []string{"h2", "http/1.1"},
					"tls_params": map[string]any{
						"ecdh_curves": []string{"X25519", "P-256", "P-384"},
					},
					"validation_context": map[string]any{
						"trusted_ca": map[string]any{
							"filename": "/etc/ssl/certs/ca-certificates.crt",
						},
					},
				},
			},
		},
		"typed_extension_protocol_options": map[string]any{
			"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": map[string]any{
				"@type": "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions",
				"upstream_http_protocol_options": map[string]any{
					"auto_sni":            true,
					"auto_san_validation": true,
				},
				"auto_config": map[string]any{
					"http_protocol_options":  map[string]any{},
					"http2_protocol_options": map[string]any{},
				},
			},
		},
	}
}

// buildOtelALSCluster returns the cluster definition that backs the
// `envoy.access_loggers.open_telemetry` sink. Caller is responsible
// for only emitting the cluster when als.MTLS is true; infra services
// must never push OTLP across the untrusted lane.
//
// STRICT_DNS resolves `otel-collector` (clawker-net DNS) on every
// refresh, http2 is required because OTLP/gRPC runs on HTTP/2. The
// upstream TLS context loads the leaf+intermediate chain bind-mounted
// at /etc/envoy/otel-tls/client.{pem,key} and validates the
// collector's server cert against the CLI root CA at ca.pem.
//
// `ensureOtelServerCert` includes consts.MonitoringServiceOtelCollector
// ("otel-collector") in the cert's DNS SANs, alongside
// "host.docker.internal" / "localhost" / "127.0.0.1". SNI is set to
// "otel-collector" so Envoy presents the expected hostname in the
// ClientHello, and `match_typed_subject_alt_names` pins the upstream
// cert to that SAN — defense-in-depth on top of the CLI-root trust
// boundary so a different CLI-root-chained leaf (future infra service)
// can't impersonate the collector for this cluster.
func buildOtelALSCluster(als ALSConfig) map[string]any {
	return map[string]any{
		"name":            otelCollectorALSClusterName,
		"type":            "STRICT_DNS",
		"connect_timeout": "1s",
		"load_assignment": map[string]any{
			"cluster_name": otelCollectorALSClusterName,
			"endpoints": []any{
				map[string]any{
					"lb_endpoints": []any{
						map[string]any{
							"endpoint": map[string]any{
								"address": map[string]any{
									"socket_address": map[string]any{
										"address":    consts.MonitoringServiceOtelCollector,
										"port_value": als.Port,
									},
								},
							},
						},
					},
				},
			},
		},
		"typed_extension_protocol_options": map[string]any{
			"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": map[string]any{
				"@type": "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions",
				"explicit_http_config": map[string]any{
					"http2_protocol_options": map[string]any{},
				},
			},
		},
		"transport_socket": map[string]any{
			"name": "envoy.transport_sockets.tls",
			"typed_config": map[string]any{
				"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext",
				"sni":   consts.MonitoringServiceOtelCollector,
				"common_tls_context": map[string]any{
					"tls_certificates": []any{
						map[string]any{
							"certificate_chain": map[string]any{
								"filename": "/etc/envoy/otel-tls/client.pem",
							},
							"private_key": map[string]any{
								"filename": "/etc/envoy/otel-tls/client.key",
							},
						},
					},
					"validation_context": map[string]any{
						"trusted_ca": map[string]any{
							"filename": "/etc/envoy/otel-tls/ca.pem",
						},
						"match_typed_subject_alt_names": []any{
							map[string]any{
								"san_type": "DNS",
								"matcher": map[string]any{
									"exact": consts.MonitoringServiceOtelCollector,
								},
							},
						},
					},
				},
			},
		},
	}
}

// buildHTTPDNSCluster creates a per-domain LOGICAL_DNS cluster for plaintext HTTP.
// No upstream TLS — used by the HTTP filter chain for proto: http rules.
func buildHTTPDNSCluster(domain string, port int) map[string]any {
	return map[string]any{
		"name":              httpClusterName(domain, port),
		"connect_timeout":   "10s",
		"type":              "LOGICAL_DNS",
		"dns_lookup_family": "V4_ONLY",
		"load_assignment": map[string]any{
			"cluster_name": httpClusterName(domain, port),
			"endpoints": []any{
				map[string]any{
					"lb_endpoints": []any{
						map[string]any{
							"endpoint": map[string]any{
								"address": map[string]any{
									"socket_address": map[string]any{
										"address":    domain,
										"port_value": port,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// statNameReplacer replaces dots and other special characters with underscores for Envoy stat names.
var statNameReplacer = strings.NewReplacer(".", "_", "-", "_", ":", "_")

// sanitizeName replaces dots and other special characters with underscores for Envoy stat names.
func sanitizeName(s string) string {
	return statNameReplacer.Replace(s)
}

// serverNames returns the Envoy server_names list for SNI matching.
// For wildcard domains (leading dot, e.g. ".datadoghq.com"), it returns both
// the suffix match form (".datadoghq.com") and the apex ("datadoghq.com").
// However, if a separate exact rule exists for the same apex (tracked in
// exactDomains), the apex is omitted to avoid duplicate filter chain matches.
// For exact domains, it returns a single-element list.
func serverNames(dst string, exactDomains map[string]bool) []string {
	domain := normalizeDomain(dst)
	if isWildcardDomain(dst) {
		if exactDomains[domain] {
			// Separate exact rule owns the apex — wildcard covers subdomains only.
			return []string{"." + domain}
		}
		return []string{"." + domain, domain}
	}
	return []string{domain}
}

// virtualHostName returns a unique Envoy virtual host name for a rule's destination.
// Wildcard domains get a "wildcard_" prefix so they don't collide with exact rules
// for the same apex domain in the same route_config.
func virtualHostName(dst string) string {
	domain := normalizeDomain(dst)
	if isWildcardDomain(dst) {
		return "wildcard_" + domain
	}
	return domain
}

// httpDomains returns the Envoy virtual host domains list for Host header matching.
// For wildcard domains (leading dot), it returns both "*.domain" and "domain".
// If a separate exact rule exists for the apex, the apex is omitted.
// For exact domains, it returns a single-element list.
func httpDomains(dst string, exactDomains map[string]bool) []string {
	domain := normalizeDomain(dst)
	if isWildcardDomain(dst) {
		if exactDomains[domain] {
			// Exact rule owns the apex — wildcard only, plus port variant.
			return []string{"*." + domain, "*." + domain + ":*"}
		}
		// Wildcard covers both apex and subdomains, with port variants.
		return []string{"*." + domain, "*." + domain + ":*", domain, domain + ":*"}
	}
	// Exact domain plus port variant so Host: domain:443 also matches.
	return []string{domain, domain + ":*"}
}
