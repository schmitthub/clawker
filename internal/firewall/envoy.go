package firewall

import (
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"gopkg.in/yaml.v3"
)

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

// TCPMapping describes a per-destination iptables DNAT entry for non-TLS traffic.
// Each TCP/SSH rule gets a dedicated Envoy listener port.
type TCPMapping struct {
	Dst       string // Destination domain or IP.
	DstPort   int    // Original destination port (e.g. 22, 8080).
	EnvoyPort int    // Envoy listener port (TCPPortBase + index).
}

// TCPMappings computes TCP port mappings from egress rules.
// The result is deterministic for a given rule set — same rules produce same mappings.
// Used by both GenerateEnvoyConfig (to build listeners) and Enable (to build iptables args).
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
		mappings = append(mappings, TCPMapping{
			Dst:       r.Dst,
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

// buildHTTPAccessLog returns an Envoy stdout access log for http_connection_manager contexts.
// Includes HTTP-specific fields (method, path, response_code, request_host) that are only
// available when Envoy terminates HTTP — used by TLS filter chains and the HTTP listener.
// request_host captures the Host/:authority header, which is the only domain source for
// plaintext HTTP (where SNI/%REQUESTED_SERVER_NAME% is empty).
func buildHTTPAccessLog(proto string) []any {
	return []any{accessLogEntry(proto, map[string]any{
		"method":        "%REQ(:METHOD)%",
		"path":          "%REQ(:PATH)%",
		"response_code": "%RESPONSE_CODE%",
		"request_host":  "%REQ(Host)%",
	})}
}

// buildTCPAccessLog returns an Envoy stdout access log for tcp_proxy contexts.
// Omits HTTP fields (method, path, response_code) that are unavailable in TCP proxy —
// used by deny and TCP/SSH listeners.
// Optional domain overrides %REQUESTED_SERVER_NAME% for raw TCP where SNI is unavailable.
func buildTCPAccessLog(proto string, domain ...string) []any {
	var extra map[string]any
	if len(domain) > 0 && domain[0] != "" {
		extra = map[string]any{"domain": domain[0]}
	}
	return []any{accessLogEntry(proto, extra)}
}

// accessLogEntry builds the common access log structure. Extra fields (if any)
// are merged into the JSON format for HTTP-aware contexts.
func accessLogEntry(proto string, extra map[string]any) map[string]any {
	jf := map[string]any{
		"timestamp":      "%START_TIME%",
		"domain":         "%REQUESTED_SERVER_NAME%",
		"upstream_host":  "%UPSTREAM_HOST%",
		"listener_ip":    "%DOWNSTREAM_LOCAL_ADDRESS_WITHOUT_PORT%",
		"client_ip":      "%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%",
		"response_flags": "%RESPONSE_FLAGS%",
		"bytes_sent":     "%BYTES_SENT%",
		"bytes_received": "%BYTES_RECEIVED%",
		"duration_ms":    "%DURATION%",
		"proto":          proto,
		"source":         "envoy",
	}
	for k, v := range extra {
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
func GenerateEnvoyConfig(rules []config.EgressRule, ports EnvoyPorts) ([]byte, []string, error) {
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
		if proto == "http" {
			if r.Port == 0 {
				r.Port = 80
			}
			httpRules = append(httpRules, r)
			continue
		}
		// Default: TLS — Envoy terminates TLS with a per-domain certificate,
		// inspects HTTP (paths visible in access logs), then re-encrypts upstream.
		// Rules with PathRules get per-path routing; rules without get allow-all.
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

	// Compute TCP port mappings (same function used by manager.Enable for iptables args).
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
			"listeners": buildListeners(tlsRules, tcpRules, httpRules, tcpMappings, ports, tlsExactDomains, httpExactDomains),
			"clusters":  buildClusters(tlsRules, tcpRules, httpRules),
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
func buildListeners(tls, tcp, http []config.EgressRule, tcpMappings []TCPMapping, ports EnvoyPorts, tlsExactDomains, httpExactDomains map[string]bool) []any {
	var listeners []any

	// Main egress listener — handles TLS (per-domain filter chains with SNI matching)
	// and plaintext HTTP (raw_buffer filter chain with Host header routing).
	// tls_inspector differentiates TLS from plaintext at the listener level.
	if len(tls) > 0 || len(http) > 0 {
		listeners = append(listeners, buildEgressListener(tls, http, ports.EgressPort, tlsExactDomains, httpExactDomains))
	}

	// Per-rule TCP/SSH listeners from the port mappings.
	for i, r := range tcp {
		if i < len(tcpMappings) {
			listeners = append(listeners, buildTCPListener(r, tcpMappings[i].EnvoyPort))
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
												"match": map[string]any{"prefix": "/"},
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
func buildEgressListener(tlsRules, httpRules []config.EgressRule, port int, tlsExactDomains, httpExactDomains map[string]bool) map[string]any {
	var filterChains []any

	// Per-domain TLS filter chains (matched by SNI via tls_inspector).
	for _, r := range tlsRules {
		filterChains = append(filterChains, buildTLSFilterChain(r, tlsExactDomains))
	}

	// Plaintext HTTP filter chain (matched by transport_protocol: "raw_buffer").
	// Handles all proto: http rules via Host header routing on this same port.
	if len(httpRules) > 0 {
		filterChains = append(filterChains, buildHTTPFilterChain(httpRules, httpExactDomains))
	}

	// Default deny chain (catch-all → connection reset).
	filterChains = append(filterChains, buildDenyFilterChain())

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
func buildHTTPFilterChain(rules []config.EgressRule, exactDomains map[string]bool) map[string]any {
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
					"match": map[string]any{"prefix": "/"},
					"route": map[string]any{"cluster": cluster, "timeout": "0s", "upgrade_configs": []any{map[string]any{"upgrade_type": "websocket"}}},
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
				"match": map[string]any{"prefix": "/"},
				"direct_response": map[string]any{
					"status": 403,
					"body":   map[string]any{"inline_string": "Blocked by clawker firewall\n"},
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
				"name": "envoy.filters.network.http_connection_manager",
				"typed_config": map[string]any{
					"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
					"stat_prefix": "http_egress",
					"codec_type":  "AUTO",
					"access_log":  buildHTTPAccessLog("http"),
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
				},
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
func buildTLSFilterChain(r config.EgressRule, exactDomains map[string]bool) map[string]any {
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
				"match": map[string]any{"prefix": "/"},
				"route": map[string]any{"cluster": cluster, "timeout": "0s", "upgrade_configs": []any{map[string]any{"upgrade_type": "websocket"}}},
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
				"name": "envoy.filters.network.http_connection_manager",
				"typed_config": map[string]any{
					"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
					"stat_prefix": fmt.Sprintf("tls_%s", sanitizeName(domain)),
					"codec_type":  "AUTO",
					"access_log":  buildHTTPAccessLog("tls"),
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
				},
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

	// Explicit path rules first.
	for _, pr := range r.PathRules {
		if strings.EqualFold(pr.Action, "allow") {
			routes = append(routes, map[string]any{
				"match": map[string]any{"prefix": pr.Path},
				"route": map[string]any{"cluster": clusterName, "timeout": "0s", "upgrade_configs": wsUpgrade},
			})
		} else {
			routes = append(routes, map[string]any{
				"match": map[string]any{"prefix": pr.Path},
				"direct_response": map[string]any{
					"status": 403,
					"body":   map[string]any{"inline_string": "Blocked by clawker firewall\n"},
				},
			})
		}
	}

	// Default action for unmatched paths.
	pathDefault := strings.ToLower(r.PathDefault)
	if pathDefault == "" || pathDefault == "deny" {
		routes = append(routes, map[string]any{
			"match": map[string]any{"prefix": "/"},
			"direct_response": map[string]any{
				"status": 403,
				"body":   map[string]any{"inline_string": "Blocked by clawker firewall\n"},
			},
		})
	} else {
		routes = append(routes, map[string]any{
			"match": map[string]any{"prefix": "/"},
			"route": map[string]any{"cluster": clusterName, "timeout": "0s", "upgrade_configs": wsUpgrade},
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
func buildDenyFilterChain() map[string]any {
	return map[string]any{
		"filter_chain_match": map[string]any{},
		"filters": []any{
			map[string]any{
				"name": "envoy.filters.network.tcp_proxy",
				"typed_config": map[string]any{
					"@type":        "type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy",
					"stat_prefix":  "deny_all",
					"cluster":      "deny_cluster",
					"idle_timeout": "0s",
					"access_log":   buildTCPAccessLog("deny"),
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
func buildTCPListener(r config.EgressRule, port int) map[string]any {
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
							"access_log":  buildTCPAccessLog(r.Proto, r.Dst),
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
func buildClusters(tls, tcp, http []config.EgressRule) []any {
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
