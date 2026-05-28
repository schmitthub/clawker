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
// HTTP-specific fields (method, path, response_code) are only available when
// Envoy terminates HTTP. The destination host travels uniformly on
// `server.address` for both TLS (SNI via %REQUESTED_SERVER_NAME%) and
// plaintext HTTP (Host/:authority header via %REQ(Host)%) — stamped at the
// common path in accessLogFields so consumers query one field regardless of
// filter-chain shape.
func buildHTTPAccessLog(tlsTerminated bool, action string, als ALSConfig) []any {
	extra := map[string]string{
		"method":                            "%REQ(:METHOD)%",
		"path":                              "%REQ(:PATH)%",
		"response_code":                     "%RESPONSE_CODE%",
		"response_code_details":             "%RESPONSE_CODE_DETAILS%",
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
	} else {
		// Plaintext HTTP chain: SNI is unavailable (%REQUESTED_SERVER_NAME%
		// is empty), so override server.address to the Host/:authority
		// header. LOGICAL_DNS cluster pinning by Host header preserves the
		// security boundary — server.address records the client's stated
		// destination on every record regardless of filter-chain shape.
		extra["server.address"] = "%REQ(Host)%"
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
// and TCP/SSH listeners. Optional serverAddress overrides
// %REQUESTED_SERVER_NAME% for raw TCP where SNI is unavailable.
//
// `action` is a clawker-internal literal stamped at config generation:
// uniform-verdict TCP filter chains hardcode it ("denied" for deny_cluster,
// "allowed" for per-rule TCP/SSH listeners). It carries the firewall
// decision in the dedicated `action` field, never overloaded into `proto`.
//
// `tls.established` field handling: the deny chain (l7Proto=="" with
// action=="denied") catches both TLS and plaintext flows that didn't
// match any allow chain — Envoy resets the connection before it can
// observe whether the client was attempting a TLS handshake or sending
// plaintext bytes. Stamping `tls.established: "false"` on a denied TLS
// handshake misleads forensics, so the deny chain OMITS the field
// entirely. Per-rule TCP/SSH allow listeners (l7Proto!="") still stamp
// "false" because the rule declares the listener as opaque TCP — no TLS
// termination is in scope by construction.
func buildTCPAccessLog(l7Proto, action string, als ALSConfig, serverAddress ...string) []any {
	var extra map[string]string
	if len(serverAddress) > 0 && serverAddress[0] != "" {
		extra = map[string]string{"server.address": serverAddress[0]}
	}
	// Deny chain catch-all: l7 protocol unknown and verdict is denied.
	// Empty tlsEstablished tells accessLogFields to omit the key.
	tlsEst := "false"
	if l7Proto == "" && action == "denied" {
		tlsEst = ""
	}
	sinks := []any{stdoutAccessLogEntry("tcp", l7Proto, tlsEst, action, extra)}
	if als.MTLS {
		sinks = append(sinks, otelAccessLogEntry("tcp", l7Proto, tlsEst, action, extra))
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
// traversal like `/allowed/%2e%2e/denied` literally starts with `/allowed/`
// — Envoy's route matcher sees the un-normalized path, the allow prefix
// matches, and the request forwards upstream, bypassing the deny path rule
// that should have caught it.
//
// Field-by-field rationale:
//   - normalize_path / merge_slashes / path_with_escaped_slashes_action:
//     RFC 3986 normalization BEFORE route matching closes the smuggling
//     vector. UNESCAPE_AND_REDIRECT issues a 307 with the canonical path
//     instead of silently rewriting, so the matcher sees what the agent
//     actually sent.
//   - headers_with_underscores_action: REJECT_REQUEST: defends against
//     RFC 9110 §5.4.5 header-name aliasing (`X_AUTH` vs `X-AUTH`).
//   - http2_protocol_options.max_concurrent_streams: h2 amplification
//     cap; default is conservative for forward-proxy threat model.
//   - http2_protocol_options.allow_connect: enables RFC 8441 extended
//     CONNECT downstream so HTTP/2 clients can run WebSocket without
//     falling back to HTTP/1.1. Envoy then translates the upgrade
//     downward to HTTP/1.1 RFC 6455 Upgrade for upstream — the upstream
//     ALPN is force-pinned to h1.1 by a per-request filter in
//     buildTLSWebSocketUpgrade (Envoy can't reliably probe for RFC 8441
//     support upstream, so it stays on h1.1 for universal compat).
//
// Timeouts (request_timeout / stream_idle_timeout / idle_timeout) are NOT
// set here. LLM API calls regularly stream for minutes with multi-MB
// request bodies, and short defaults rupture mid-stream. Envoy's built-in
// defaults are deliberate.
//
// Applied via maps.Copy at HCM construction sites — keeps each HCM literal
// readable while ensuring no site forgets a hardening field.
func httpConnectionManagerHardening() map[string]any {
	return map[string]any{
		"normalize_path":                   true,
		"merge_slashes":                    true,
		"path_with_escaped_slashes_action": "UNESCAPE_AND_REDIRECT",
		"common_http_protocol_options": map[string]any{
			"headers_with_underscores_action": "REJECT_REQUEST",
		},
		"http2_protocol_options": map[string]any{
			"max_concurrent_streams": 100,
			"allow_connect":          true,
		},
	}
}

// clawkerActionMetadata returns a route-level metadata block whose
// filter_metadata.clawker.action is read by the HTTP access log via
// %METADATA(ROUTE:clawker:action)% substitution. Every route literal in
// the route table MUST carry this so Envoy stamps the correct action
// per-record at emit time. `action` is a Go literal ("allowed" / "denied")
// — never a runtime computation — exactly the same generation-time model
// as `l7Proto`/`action` hardcoded at TCP filter chain access loggers
// (buildTCPAccessLog call sites in buildDenyFilterChain and
// buildTCPListener).
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
// Parameters:
//
//   - `transport` is the OTel network.transport enum and is always
//     hardcoded "tcp" at every call site (every clawker filter chain is
//     TCP-bound; future UDP/QUIC listeners would pass "udp" / "quic").
//   - `l7Proto` is the OTel network.protocol.name. HCM filter chains
//     (TLS-MITM + plaintext HTTP) pass "http"; per-rule opaque TCP/SSH
//     listeners pass the rule's `proto:` value (`ssh` / `tcp` / unknown);
//     the deny chain catch-all passes "" because no L7 was negotiated.
//   - `tlsEstablished` is the OTel tls.established bool-as-string. Only
//     "true" for TLS-terminating HCM chains. Per-rule opaque TCP/SSH
//     listeners pass "false" (the rule declares the listener as opaque
//     TCP — no TLS termination in scope by construction). The deny chain
//     passes "" to OMIT the field — see the buildTCPAccessLog doc for
//     the forensics rationale (a denied TLS handshake stamped "false"
//     would mislead consumers about what was actually on the wire).
//   - `action` carries the clawker firewall decision (`allowed` /
//     `denied`), stamped at config generation time. For uniform-verdict
//     filter chains (deny_cluster, TCP/SSH listeners) the call site
//     passes a literal; for mixed-verdict HTTP filter chains (one HCM
//     serves both allow + deny routes) the call site passes the
//     substitution token `%METADATA(ROUTE:clawker:action)%` so Envoy
//     copies the per-route metadata value into the log line at emit
//     time. The field is NEVER inferred from response_code,
//     response_flags, or any downstream-of-routing signal.
//
// `action` and `l7Proto` are distinct fields — never overload one into
// the other. The firewall verdict travels on `action` only.
//
// `extra` carries context-specific overrides (HTTP method/path/code, or a
// static server.address for raw TCP). Nil-safe.
//
// Field naming follows OTel network/server/client/tls semantic conventions
// where they exist: `server.address` (replaces deprecated `tls.server.name`
// — the canonical "host the client was trying to reach", stable since
// semconv v1.21), `client.address`, `network.peer.{address,port}` (post-
// resolution upstream peer per network semconv), `tls.*`, `network.*`.
// Envoy-specific operational fields stay flat under their original names
// (`listener_ip`, `upstream_tls_*`, `bytes_*`, `duration_ms`, etc.) — no
// OTel equivalent.
func accessLogFields(transport, l7Proto, tlsEstablished, action string, extra map[string]string) map[string]string {
	f := map[string]string{
		"server.address":          "%REQUESTED_SERVER_NAME%",
		"client.address":          "%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%",
		"listener_ip":             "%DOWNSTREAM_LOCAL_ADDRESS_WITHOUT_PORT%",
		"network.peer.address":    "%UPSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%",
		"network.peer.port":       "%UPSTREAM_REMOTE_PORT%",
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
		"action":                  action,
	}
	// Empty tlsEstablished omits the key — used by the deny chain where
	// stamping "false" on a denied TLS handshake would mislead forensics.
	if tlsEstablished != "" {
		f["tls.established"] = tlsEstablished
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

// tlsExactClusterName returns the per-domain, per-port cluster name for an
// exact-rule TLS upstream. Each (domain, port) pair gets its own LOGICAL_DNS
// cluster with upstream TLS re-encryption. Port is included so that rules for
// the same domain on different ports (e.g., example.com:443 and
// example.com:8443) get separate clusters. The `exact` segment is symmetric
// with `tlsWildcardClusterName`'s `wildcard` segment so the cluster-kind
// namespace cannot collide with a user-controlled domain string: a user
// domain `wildcard.foo.com` produces `tls_exact_wildcard_foo_com_443`, a
// wildcard `.foo.com` produces `tls_wildcard_foo_com_443` — different kind
// segment, no collision possible.
func tlsExactClusterName(domain string, port int) string {
	return fmt.Sprintf("tls_exact_%s_%d", sanitizeName(domain), port)
}

// tlsWildcardClusterName returns the cluster name for the wildcard-rule TLS upstream.
// Wildcard rules require a separate cluster from any exact rule for the same apex —
// the wildcard cluster is dynamic_forward_proxy with sub_clusters keyed by SNI, the
// exact cluster is LOGICAL_DNS pinned to the apex. The symmetric `tls_wildcard_` /
// `tls_exact_` prefixes keep the cluster-kind namespace structurally distinct from
// any user-controlled domain string (see `tlsExactClusterName`).
func tlsWildcardClusterName(apex string, port int) string {
	return fmt.Sprintf("tls_wildcard_%s_%d", sanitizeName(apex), port)
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
		switch proto {
		case "ssh", "tcp":
			tcpRules = append(tcpRules, r)
			continue
		case "http":
			// Plaintext HTTP. Routes to the raw_buffer HCM filter chain
			// (no TLS termination). Used by sites that genuinely serve
			// plaintext on port 80.
			if r.Port == 0 {
				r.Port = 80
			}
			httpRules = append(httpRules, r)
			continue
		case "https", "":
			// TLS-MITM. Envoy terminates TLS with a per-domain cert,
			// inspects HTTP semantics (paths visible in access logs),
			// then re-encrypts upstream. Rules with PathRules get
			// per-path routing; rules without get allow-all. Empty proto
			// defaults here (the common case post-NormalizeRule).
			if r.Port == 0 {
				r.Port = 443
			}
			tlsRules = append(tlsRules, r)
			continue
		default:
			// Unknown L7 name: route to opaque TCP listener so the
			// destination is at least proxied — arbitrary `proto:`
			// strings fall through to TCP passthrough.
			tcpRules = append(tcpRules, r)
		}
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

	clusters := buildClusters(tlsRules, tcpRules, httpRules, als)
	// Defense in depth: cluster names are constructed from kind-segmented
	// builders (`tls_exact_…`, `tls_wildcard_…`, `http_…`, `tcp_…`,
	// `deny_cluster`, `otel_collector_als`) so collisions are not reachable
	// via any combination of inputs. Catch any future builder regression
	// here — Envoy would reject the config opaquely at load (cds.update_rejected),
	// stranding the firewall. Fail closed at config-gen time with a structured
	// error instead.
	if dup := firstDuplicateClusterName(clusters); dup != "" {
		return nil, nil, fmt.Errorf("duplicate cluster name %q in generated envoy config (cluster-naming invariant violated)", dup)
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
			"clusters":  clusters,
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
// certificate, inspects HTTP traffic, then forwards to the per-domain upstream
// cluster that re-encrypts upstream.
//
// For exact rules the cluster is LOGICAL_DNS whose endpoint is the apex hostname:
// the upstream destination is determined by the cluster endpoint, NOT by the HTTP
// Host header, which closes the confused-deputy vector where a malicious client
// could manipulate Host to redirect traffic.
//
// For wildcard rules the cluster is dynamic_forward_proxy with sub_clusters_config:
// the filter chain prepends an HTTP set_filter_state filter that copies the
// downstream SNI (%REQUESTED_SERVER_NAME%) into envoy.upstream.dynamic_host. The
// DFP filter then resolves the actual subdomain rather than the apex, so e.g.
// www.mintlify.com on Cloudflare and the mintlify.com apex on Vercel each reach
// their own backend. The trust boundary still holds: this chain only entered
// when SNI matched the wildcard suffix, the dynamic_host is sourced from SNI
// (not Host), and each host:port gets its own sub-cluster pool so same-SAN
// HTTP/2 coalescing across hostnames cannot happen.
func buildTLSFilterChain(r config.EgressRule, exactDomains map[string]bool, als ALSConfig) map[string]any {
	domain := normalizeDomain(r.Dst)
	certFile := fmt.Sprintf(envoyCertFileFmt, domain)
	keyFile := fmt.Sprintf(envoyKeyFileFmt, domain)
	wildcard := isWildcardDomain(r.Dst)
	cluster := tlsExactClusterName(domain, r.Port)
	if wildcard {
		cluster = tlsWildcardClusterName(domain, r.Port)
	}

	// Build routes: path rules when configured, otherwise allow-all.
	// Exact rules route to a per-domain LOGICAL_DNS cluster pinned to the apex;
	// wildcard rules route to a per-apex dynamic_forward_proxy cluster whose
	// sub-cluster is keyed on the downstream SNI written into
	// envoy.upstream.dynamic_host by the chain's set_filter_state filter.
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
				"typed_config": tlsHCMTypedConfig(r, domain, exactDomains, routes, wildcard, als),
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

func tlsHCMTypedConfig(r config.EgressRule, domain string, exactDomains map[string]bool, routes []any, wildcard bool, als ALSConfig) map[string]any {
	tc := map[string]any{
		"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
		"stat_prefix": fmt.Sprintf("tls_%s", sanitizeName(domain)),
		"codec_type":  "AUTO",
		"access_log":  buildHTTPAccessLog(true, "%METADATA(ROUTE:clawker:action)%", als),
		// TLS upstream uses auto_config (H2+H1.1 ALPN). WebSocket Upgrade
		// doesn't exist in HTTP/2, so the upgrade filter chain overrides
		// ALPN to force HTTP/1.1 on the upstream TLS handshake. For
		// wildcard chains the upgrade chain additionally writes the
		// dynamic_host filter state so DFP resolves the actual SNI host,
		// not the apex.
		"upgrade_configs": buildTLSWebSocketUpgrade(wildcard),
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
		"http_filters": tlsHTTPFilters(wildcard),
	}
	maps.Copy(tc, httpConnectionManagerHardening())
	return tc
}

// tlsHTTPFilters returns the http_filters list for a TLS-MITM HCM. Every TLS
// chain prepends sniLockFilter — without it Router::Filter::decodeHeaders
// would fall back to deriving upstream SNI / SAN-validation target from the
// HTTP :authority header, letting an attacker who controls Host validate the
// upstream cert against a different name than the one that selected the
// filter chain. Wildcard chains additionally insert the dynamic_host writer
// and the DFP HTTP filter so cluster resolution sees the per-subdomain SNI
// rather than the apex; the dynamic_host writer must execute before the DFP
// filter on every request.
func tlsHTTPFilters(wildcard bool) []any {
	if !wildcard {
		return []any{sniLockFilter(), routerFilter()}
	}
	return []any{
		sniLockFilter(),
		dynamicHostFromSNIFilter(),
		dynamicForwardProxyHTTPFilter(),
		routerFilter(),
	}
}

// sniLockFilter pre-populates the two filter-state keys the router would
// otherwise derive from the :authority header under auto_sni /
// auto_san_validation:
//
//   - envoy.network.upstream_server_name — upstream TLS SNI.
//   - envoy.network.upstream_subject_alt_names — expected upstream cert SAN.
//
// Router::Filter::decodeHeaders only writes these when filter state does not
// already carry them, so a pre-populated value sourced from
// %REQUESTED_SERVER_NAME% (the downstream SNI that selected this filter
// chain) wins. Result: upstream TLS handshake and SAN validation are bound
// to the SNI a connecting client presented, never to the attacker-influenced
// Host header. Applies to every TLS chain — exact and wildcard — because the
// attack does not require DFP (TCP destination follows the cluster endpoint
// on exact rules, but a Host-derived upstream SNI can still cause a request
// to be served at the cluster's edge under a different cert name and routed
// to an unallowed sibling at the app layer).
func sniLockFilter() map[string]any {
	return setFilterStateFromSNI(
		"envoy.network.upstream_server_name",
		"envoy.network.upstream_subject_alt_names",
	)
}

// dynamicHostFromSNIFilter writes envoy.upstream.dynamic_host from the
// downstream SNI for wildcard chains. The DFP HTTP filter consumes this key
// (when allow_dynamic_host_from_filter_state is set) for cluster / DNS
// resolution, so the actual subdomain is routed rather than the apex.
func dynamicHostFromSNIFilter() map[string]any {
	return setFilterStateFromSNI("envoy.upstream.dynamic_host")
}

// setFilterStateFromSNI builds an envoy.filters.http.set_filter_state config
// that writes each given object_key with the downstream SNI value
// (%REQUESTED_SERVER_NAME%) on every request.
func setFilterStateFromSNI(objectKeys ...string) map[string]any {
	entries := make([]any, 0, len(objectKeys))
	for _, k := range objectKeys {
		entries = append(entries, map[string]any{
			"object_key": k,
			"format_string": map[string]any{
				"text_format_source": map[string]any{
					"inline_string": "%REQUESTED_SERVER_NAME%",
				},
			},
		})
	}
	return map[string]any{
		"name": "envoy.filters.http.set_filter_state",
		"typed_config": map[string]any{
			"@type":              "type.googleapis.com/envoy.extensions.filters.http.set_filter_state.v3.Config",
			"on_request_headers": entries,
		},
	}
}

// dynamicForwardProxyHTTPFilter is the DFP HTTP filter used inside wildcard
// TLS filter chains. allow_dynamic_host_from_filter_state lets the filter
// honor envoy.upstream.dynamic_host instead of the :authority header
// (matching the long-standing SNI/UDP DFP behavior — landed in Envoy 1.35).
// sub_cluster_config.cluster_init_timeout caps how long the first request to
// a new SNI waits for its sub-cluster's DNS resolve before failing.
func dynamicForwardProxyHTTPFilter() map[string]any {
	return map[string]any{
		"name": "envoy.filters.http.dynamic_forward_proxy",
		"typed_config": map[string]any{
			"@type":                                "type.googleapis.com/envoy.extensions.filters.http.dynamic_forward_proxy.v3.FilterConfig",
			"allow_dynamic_host_from_filter_state": true,
			"sub_cluster_config": map[string]any{
				"cluster_init_timeout": "5s",
			},
		},
	}
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
	// response_code). See clawkerActionMetadata.
	//
	// Sort longest-prefix-first: Envoy route matching is first-match-wins on
	// prefix, so slice order would let a broad rule (deny /public/) shadow a
	// narrower override (allow /public/sub/). Stable sort keeps insertion
	// order for equal-length prefixes, preserving MergeRule's caller-wins
	// semantics on identical-path collisions.
	prs := append([]config.PathRule(nil), r.PathRules...)
	sort.SliceStable(prs, func(i, j int) bool {
		return len(prs[i].Path) > len(prs[j].Path)
	})
	for _, pr := range prs {
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
// on TLS filter chains. WebSocket upgrade_configs replace the HCM's
// http_filters wholesale for upgrade requests, so every filter the regular
// request path relies on must be reproduced here.
//
// Filters per chain kind:
//   - Exact rule: [sni-lock, alpn-override(h1.1), router]
//   - Wildcard rule: [sni-lock, dynamic_host writer, alpn-override(h1.1),
//     dynamic_forward_proxy, router]
//
// Why each is here:
//   - sni-lock locks upstream SNI/SAN to downstream SNI (per-request) — same
//     confused-deputy fix as the regular http_filters path.
//   - dynamic_host + DFP (wildcard only) route the upgrade to the actual
//     subdomain rather than the apex.
//   - alpn-override-to-http/1.1 is the recommended Envoy pattern (current
//     in 1.37+) for forcing upstream ALPN to h1.1 per-request, overriding
//     whatever the cluster's auto_config would negotiate. Without it, h2
//     gets negotiated upstream and the upgrade fails on any upstream that
//     doesn't implement RFC 8441 extended CONNECT (e.g. ngrok edge,
//     plenty of origin servers). With it, Envoy automatically translates
//     downstream h2 extended CONNECT → upstream h1.1 RFC 6455 Upgrade,
//     so HTTP/2 WebSocket clients still work end-to-end.
//
// h2 WebSocket support comes from `allow_connect: true` on the downstream
// HCM's http2_protocol_options (see httpConnectionManagerHardening).
//
// Ref: https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/http/upgrades
func buildTLSWebSocketUpgrade(wildcard bool) []any {
	alpnOverride := map[string]any{
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
	}

	var filters []any
	if wildcard {
		filters = append(filters, sniLockFilter(), dynamicHostFromSNIFilter(), alpnOverride, dynamicForwardProxyHTTPFilter(), routerFilter())
	} else {
		filters = append(filters, sniLockFilter(), alpnOverride, routerFilter())
	}

	return []any{map[string]any{"upgrade_type": "websocket", "filters": filters}}
}

// buildDenyFilterChain creates the default deny filter chain that resets
// connections. Catch-all filter_chain_match: matches any flow no allow
// chain claimed. Access logging emits `action: "denied"` so blocked
// connection attempts are visible in the egress monitoring dashboard
// alongside allowed traffic. The deny chain sees both TLS and plaintext
// flows (Envoy resets before observing which), so the access log omits
// `tls.established` — see buildTCPAccessLog for the forensics rationale.
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
					// An explicit "0s" disables the timeout entirely; the
					// tcp_proxy.proto docs warn this yields connection
					// leaks ("Disabling this timeout is likely to yield
					// connection leaks"). A hostile agent opening N
					// never-closed TCP connections to a blocked SNI
					// would exhaust Envoy's per-process FD/socket budget.
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

// firstDuplicateClusterName scans a slice of cluster maps and returns the
// first duplicate `name` it finds, or "" when all names are unique. Used as
// a config-gen-time sanity check to catch any future builder regression that
// would otherwise be surfaced only by Envoy rejecting the config at load.
func firstDuplicateClusterName(clusters []any) string {
	seen := make(map[string]bool, len(clusters))
	for _, c := range clusters {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		name, _ := cm["name"].(string)
		if name == "" {
			continue
		}
		if seen[name] {
			return name
		}
		seen[name] = true
	}
	return ""
}

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

	// Per-(domain, port) TLS clusters. Exact rules get LOGICAL_DNS with the
	// apex hostname as the pinned endpoint; wildcard rules get a separate
	// dynamic_forward_proxy cluster whose endpoint is resolved at request
	// time from the SNI (written into filter state by the wildcard filter
	// chain). The two cluster kinds are independent — a wildcard rule and
	// an exact rule for the same apex produce two distinct clusters whose
	// connection pools never share. Dedup key includes the kind so the same
	// apex can carry both.
	seen := make(map[string]bool)
	for _, r := range tls {
		domain := normalizeDomain(r.Dst)
		kind := "exact"
		if isWildcardDomain(r.Dst) {
			kind = "wildcard"
		}
		key := fmt.Sprintf("%s:%s:%d", kind, domain, r.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		if isWildcardDomain(r.Dst) {
			clusters = append(clusters, buildTLSWildcardDFPCluster(domain, r.Port))
		} else {
			clusters = append(clusters, buildTLSDNSCluster(domain, r.Port))
		}
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
		"name":              tlsExactClusterName(domain, port),
		"connect_timeout":   "10s",
		"type":              "LOGICAL_DNS",
		"dns_lookup_family": "V4_ONLY",
		"load_assignment": map[string]any{
			"cluster_name": tlsExactClusterName(domain, port),
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

// buildTLSWildcardDFPCluster creates a per-apex dynamic_forward_proxy cluster
// for wildcard rules. Unlike the exact-rule LOGICAL_DNS cluster whose endpoint
// is pinned to the apex hostname, this cluster routes each request to a
// sub-cluster keyed on the actual SNI (provided via the envoy.upstream.dynamic_host
// filter state that the wildcard filter chain writes from %REQUESTED_SERVER_NAME%).
// Each sub-cluster keeps its own DNS resolution and connection pool, so:
//
//   - Subdomains that resolve to different IPs than the apex (e.g.
//     mintlify.com → Vercel apex, www.mintlify.com → Cloudflare) connect to
//     the correct upstream rather than the apex IP.
//   - Same-SAN HTTP/2 connection coalescing across different hostnames
//     (e.g. api.anthropic.com and statsig.anthropic.com sharing a SAN)
//     cannot happen: allow_coalesced_connections defaults to false and
//     lifetime callbacks return empty when off, so no cross-sub-cluster
//     pool reuse is tracked.
//
// Trust boundary: the dynamic_host is sourced from SNI inside a filter chain
// whose SNI match list is owned by serverNames() — the suffix `.<apex>` is
// always matched; the apex itself is matched only when no exact sibling rule
// covers it (so a hostile Host header still cannot redirect the upstream
// connection outside the wildcard's reach).
func buildTLSWildcardDFPCluster(apex string, port int) map[string]any {
	return map[string]any{
		"name":            tlsWildcardClusterName(apex, port),
		"connect_timeout": "10s",
		"lb_policy":       "CLUSTER_PROVIDED",
		// V4_ONLY parity with buildTLSDNSCluster — clawker-net is IPv4-only.
		// Without this, the dynamically-spawned sub-clusters try AAAA records
		// first (Envoy's `AUTO` default prefers IPv6) and fail with
		// `Network is unreachable` for any host with IPv6 records.
		"dns_lookup_family": "V4_ONLY",
		"cluster_type": map[string]any{
			"name": "envoy.clusters.dynamic_forward_proxy",
			"typed_config": map[string]any{
				"@type": "type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig",
				"sub_clusters_config": map[string]any{
					"max_sub_clusters": 1024,
					"sub_cluster_ttl":  "300s",
				},
				// Explicit false instead of proto default. With sub_clusters,
				// each host:port gets its own pool — coalescing across pools
				// would re-enable the SAN-shared h2 reuse race where the
				// first IP to win a pool serves every subsequent same-SAN
				// hostname (api.X / statsig.X collapsing onto one socket).
				// Pinning the flag here makes the security boundary
				// resilient to any future upstream default change.
				"allow_coalesced_connections": false,
			},
		},
		"transport_socket": map[string]any{
			"name": "envoy.transport_sockets.tls",
			"typed_config": map[string]any{
				"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext",
				"common_tls_context": map[string]any{
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
