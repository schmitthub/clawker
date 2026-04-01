package firewall

import (
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"gopkg.in/yaml.v3"
)

// EnvoyPorts holds the port configuration for the Envoy proxy, sourced from config.Config.
type EnvoyPorts struct {
	TLSPort     int // Listener port for TLS egress (terminates TLS, inspects HTTP, re-encrypts upstream).
	TCPPortBase int // Starting port for TCP/SSH listeners.
	HTTPPort    int // Listener port for plain HTTP egress (Host header domain detection).
	HealthPort  int // Dedicated health check listener port for external probes.
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

// HTTPMappings computes HTTP port mappings from egress rules.
// Unlike TCP mappings (one listener per rule), all HTTP rules share a single listener
// because the Host header provides domain detection. Multiple rules on the same port
// produce a single DNAT entry.
func HTTPMappings(rules []config.EgressRule, httpPort int) []TCPMapping {
	seen := make(map[int]bool)
	var mappings []TCPMapping
	for _, r := range rules {
		if strings.ToLower(r.Proto) != "http" {
			continue
		}
		action := strings.ToLower(r.Action)
		if action != "allow" && action != "" {
			continue
		}
		if isIPOrCIDR(r.Dst) {
			continue
		}
		port := r.Port
		if port == 0 {
			continue // HTTP rules require explicit port.
		}
		if seen[port] {
			continue // Deduplicate — multiple domains on same port share one DNAT entry.
		}
		seen[port] = true
		mappings = append(mappings, TCPMapping{
			Dst:       "http",
			DstPort:   port,
			EnvoyPort: httpPort,
		})
	}
	return mappings
}

// Cert path formats inside the Envoy container (mounted volume).
const (
	envoyCertFileFmt = "/etc/envoy/certs/%s-cert.pem"
	envoyKeyFileFmt  = "/etc/envoy/certs/%s-key.pem"
)

// buildHTTPAccessLog returns an Envoy stdout access log for http_connection_manager contexts.
// Includes HTTP-specific fields (method, path, response_code) that are only available
// when Envoy terminates HTTP — used by TLS filter chains and the HTTP listener.
func buildHTTPAccessLog(proto string) []any {
	return []any{accessLogEntry(proto, map[string]any{
		"method":        "%REQ(:METHOD)%",
		"path":          "%REQ(:PATH)%",
		"response_code": "%RESPONSE_CODE%",
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
		"listener_port":  "%DOWNSTREAM_LOCAL_ADDRESS_WITHOUT_PORT%",
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

// dnsCacheName is the shared DNS cache name for dynamic forward proxy.
const dnsCacheName = "dynamic_forward_proxy_cache_config"

// Cluster names for dynamic forward proxy routing.
const (
	// clusterDFPPlaintext is for HTTP listener routes — no upstream TLS.
	clusterDFPPlaintext = "dynamic_forward_proxy_cluster"
	// clusterDFPTLS is for TLS listener routes — re-encrypts upstream after MITM termination.
	clusterDFPTLS = "dynamic_forward_proxy_cluster_tls"
)

// GenerateEnvoyConfig produces an Envoy static bootstrap YAML from egress rules.
// Returns the YAML bytes and a list of warnings (non-fatal issues).
func GenerateEnvoyConfig(rules []config.EgressRule, ports EnvoyPorts) ([]byte, []string, error) {
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
			"clusters":  buildClusters(tcpRules),
		},
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal envoy config: %w", err)
	}
	return out, warnings, nil
}

// buildListeners constructs all Envoy listeners.
func buildListeners(tls, tcp, http []config.EgressRule, tcpMappings []TCPMapping, ports EnvoyPorts, tlsExactDomains, httpExactDomains map[string]bool) []any {
	var listeners []any

	// Main TLS listener — terminates TLS, inspects HTTP, re-encrypts upstream.
	if len(tls) > 0 {
		listeners = append(listeners, buildTLSListener(tls, ports.TLSPort, tlsExactDomains))
	}

	// HTTP listener — domain detection via Host header.
	if len(http) > 0 {
		listeners = append(listeners, buildHTTPListener(http, ports.HTTPPort, httpExactDomains))
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
							"http_filters": []any{
								map[string]any{
									"name": "envoy.filters.http.router",
									"typed_config": map[string]any{
										"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router",
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

// buildTLSListener creates the main TLS listener on the given port.
// All TLS rules terminate TLS with per-domain certificates, inspect HTTP
// traffic (making paths visible in access logs), then re-encrypt upstream.
func buildTLSListener(rules []config.EgressRule, tlsPort int, exactDomains map[string]bool) map[string]any {
	var filterChains []any

	// Per-domain TLS filter chains.
	for _, r := range rules {
		filterChains = append(filterChains, buildTLSFilterChain(r, exactDomains))
	}

	// Default deny chain (catch-all → connection reset).
	filterChains = append(filterChains, buildDenyFilterChain())

	return map[string]any{
		"name": "tls_egress",
		"address": map[string]any{
			"socket_address": map[string]any{
				"address":    "0.0.0.0",
				"port_value": tlsPort,
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

// buildHTTPListener creates a plain HTTP listener that detects destination domains
// via the Host header — the HTTP equivalent of the TLS listener's SNI detection.
// Each allowed domain gets a virtual host; unknown domains get a 403 deny response.
// Rules with PathRules get per-path route matching; rules without get allow-all.
func buildHTTPListener(rules []config.EgressRule, httpPort int, exactDomains map[string]bool) map[string]any {
	var virtualHosts []any

	for _, r := range rules {
		domain := normalizeDomain(r.Dst)
		var routes []any

		if len(r.PathRules) > 0 {
			// Path-level access control — same logic as TLS filter chains.
			routes = buildHTTPRoutes(r, clusterDFPPlaintext)
		} else {
			// No path rules — allow all traffic to this domain.
			routes = []any{
				map[string]any{
					"match": map[string]any{"prefix": "/"},
					"route": map[string]any{"cluster": clusterDFPPlaintext},
				},
			}
		}

		virtualHosts = append(virtualHosts, map[string]any{
			"name":    domain,
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
		"name": "http_egress",
		"address": map[string]any{
			"socket_address": map[string]any{
				"address":    "0.0.0.0",
				"port_value": httpPort,
			},
		},
		"filter_chains": []any{
			map[string]any{
				"filters": []any{
					map[string]any{
						"name": "envoy.filters.network.http_connection_manager",
						"typed_config": map[string]any{
							"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
							"stat_prefix": "http_egress",
							"codec_type":  "AUTO",
							"access_log":  buildHTTPAccessLog("http"),
							"route_config": map[string]any{
								"name":          "http_egress_routes",
								"virtual_hosts": virtualHosts,
							},
							"http_filters": []any{
								map[string]any{
									"name": "envoy.filters.http.dynamic_forward_proxy",
									"typed_config": map[string]any{
										"@type": "type.googleapis.com/envoy.extensions.filters.http.dynamic_forward_proxy.v3.FilterConfig",
										"dns_cache_config": map[string]any{
											"name":              dnsCacheName,
											"dns_lookup_family": "V4_ONLY",
										},
									},
								},
								map[string]any{
									"name": "envoy.filters.http.router",
									"typed_config": map[string]any{
										"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router",
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

// buildTLSFilterChain creates a filter chain that terminates TLS with a per-domain
// certificate, inspects HTTP traffic, then forwards upstream with re-encryption.
// Rules with PathRules get per-path routing; rules without get allow-all.
func buildTLSFilterChain(r config.EgressRule, exactDomains map[string]bool) map[string]any {
	domain := normalizeDomain(r.Dst)
	certFile := fmt.Sprintf(envoyCertFileFmt, domain)
	keyFile := fmt.Sprintf(envoyKeyFileFmt, domain)

	// Build routes: path rules when configured, otherwise allow-all.
	// TLS filter chains use the TLS cluster to re-encrypt upstream after MITM termination.
	var routes []any
	if len(r.PathRules) > 0 {
		routes = buildHTTPRoutes(r, clusterDFPTLS)
	} else {
		routes = []any{
			map[string]any{
				"match": map[string]any{"prefix": "/"},
				"route": map[string]any{"cluster": clusterDFPTLS},
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
					"route_config": map[string]any{
						"name": fmt.Sprintf("tls_route_%s", sanitizeName(domain)),
						"virtual_hosts": []any{
							map[string]any{
								"name":    domain,
								"domains": httpDomains(r.Dst, exactDomains),
								"routes":  routes,
							},
							map[string]any{
								"name":    "deny_" + sanitizeName(domain),
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
							},
						},
					},
					"http_filters": []any{
						map[string]any{
							"name": "envoy.filters.http.dynamic_forward_proxy",
							"typed_config": map[string]any{
								"@type": "type.googleapis.com/envoy.extensions.filters.http.dynamic_forward_proxy.v3.FilterConfig",
								"dns_cache_config": map[string]any{
									"name":              dnsCacheName,
									"dns_lookup_family": "V4_ONLY",
								},
							},
						},
						map[string]any{
							"name": "envoy.filters.http.router",
							"typed_config": map[string]any{
								"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router",
							},
						},
					},
				},
			},
		},
	}
}

// buildHTTPRoutes converts path rules into Envoy route entries.
// clusterName determines the upstream cluster — clusterDFPTLS for TLS filter chains
// (re-encrypts upstream), clusterDFPPlaintext for HTTP listener routes.
func buildHTTPRoutes(r config.EgressRule, clusterName string) []any {
	var routes []any

	// Explicit path rules first.
	for _, pr := range r.PathRules {
		if strings.EqualFold(pr.Action, "allow") {
			routes = append(routes, map[string]any{
				"match": map[string]any{"prefix": pr.Path},
				"route": map[string]any{"cluster": clusterName},
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
			"route": map[string]any{"cluster": clusterName},
		})
	}

	return routes
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

// dfpClusterType returns the shared dynamic forward proxy cluster_type config.
// Both plaintext and TLS clusters use the same DNS cache for resolution.
func dfpClusterType() map[string]any {
	return map[string]any{
		"name": "envoy.clusters.dynamic_forward_proxy",
		"typed_config": map[string]any{
			"@type": "type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig",
			"dns_cache_config": map[string]any{
				"name":              dnsCacheName,
				"dns_lookup_family": "V4_ONLY",
			},
		},
	}
}

// buildClusters constructs all Envoy clusters.
func buildClusters(tcp []config.EgressRule) []any {
	clusters := []any{
		// Dynamic forward proxy cluster — plaintext upstream (HTTP listener).
		map[string]any{
			"name":            clusterDFPPlaintext,
			"connect_timeout": "10s",
			"lb_policy":       "CLUSTER_PROVIDED",
			"cluster_type":    dfpClusterType(),
		},
		// Dynamic forward proxy cluster — TLS upstream (re-encrypts after MITM termination).
		// auto_sni and auto_san_validation bind upstream TLS validation to the
		// dynamically resolved host instead of trusting any public-CA-signed endpoint.
		map[string]any{
			"name":            clusterDFPTLS,
			"connect_timeout": "10s",
			"lb_policy":       "CLUSTER_PROVIDED",
			"cluster_type":    dfpClusterType(),
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
		},
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

// httpDomains returns the Envoy virtual host domains list for Host header matching.
// For wildcard domains (leading dot), it returns both "*.domain" and "domain".
// If a separate exact rule exists for the apex, the apex is omitted.
// For exact domains, it returns a single-element list.
func httpDomains(dst string, exactDomains map[string]bool) []string {
	domain := normalizeDomain(dst)
	if isWildcardDomain(dst) {
		if exactDomains[domain] {
			return []string{"*." + domain}
		}
		return []string{"*." + domain, domain}
	}
	return []string{domain}
}
