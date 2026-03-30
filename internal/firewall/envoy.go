package firewall

import (
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"gopkg.in/yaml.v3"
)

// EnvoyPorts holds the port configuration for the Envoy proxy, sourced from config.Config.
type EnvoyPorts struct {
	TLSPort     int // Listener port for TLS egress (SNI passthrough + MITM).
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
// when Envoy terminates HTTP — used by MITM filter chains and the HTTP listener.
func buildHTTPAccessLog(proto string) []any {
	return []any{accessLogEntry(proto, map[string]any{
		"method":        "%REQ(:METHOD)%",
		"path":          "%REQ(:PATH)%",
		"response_code": "%RESPONSE_CODE%",
	})}
}

// buildTCPAccessLog returns an Envoy stdout access log for tcp_proxy contexts.
// Omits HTTP fields (method, path, response_code) that are unavailable in TCP proxy —
// used by passthrough TLS, deny, and TCP/SSH listeners.
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

// GenerateEnvoyConfig produces an Envoy static bootstrap YAML from egress rules.
// Returns the YAML bytes and a list of warnings (non-fatal issues).
func GenerateEnvoyConfig(rules []config.EgressRule, ports EnvoyPorts) ([]byte, []string, error) {
	var warnings []string

	// Classify rules.
	var (
		mitmRules        []config.EgressRule
		passthroughRules []config.EgressRule
		tcpRules         []config.EgressRule
		httpRules        []config.EgressRule
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
		// Default: TLS — normalize port defensively (Envoy rejects port_value 0).
		if r.Port == 0 {
			r.Port = 443
		}
		if len(r.PathRules) > 0 {
			mitmRules = append(mitmRules, r)
		} else {
			passthroughRules = append(passthroughRules, r)
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
			"listeners": buildListeners(mitmRules, passthroughRules, tcpRules, httpRules, tcpMappings, ports),
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
func buildListeners(mitm, passthrough, tcp, http []config.EgressRule, tcpMappings []TCPMapping, ports EnvoyPorts) []any {
	var listeners []any

	// Main TLS listener — handles both MITM and passthrough.
	if len(mitm) > 0 || len(passthrough) > 0 {
		listeners = append(listeners, buildTLSListener(mitm, passthrough, ports.TLSPort))
	}

	// HTTP listener — domain detection via Host header.
	if len(http) > 0 {
		listeners = append(listeners, buildHTTPListener(http, ports.HTTPPort))
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
func buildTLSListener(mitm, passthrough []config.EgressRule, tlsPort int) map[string]any {
	var filterChains []any

	// 1. MITM filter chains (TLS termination + HTTP path matching).
	for _, r := range mitm {
		filterChains = append(filterChains, buildMITMFilterChain(r))
	}

	// 2. SNI passthrough filter chains.
	for _, r := range passthrough {
		filterChains = append(filterChains, buildPassthroughFilterChain(r))
	}

	// 3. Default deny chain (catch-all → connection reset).
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
func buildHTTPListener(rules []config.EgressRule, httpPort int) map[string]any {
	var virtualHosts []any

	for _, r := range rules {
		domain := normalizeDomain(r.Dst)
		var routes []any

		if len(r.PathRules) > 0 {
			// Path-level access control — same logic as MITM.
			routes = buildHTTPRoutes(r)
		} else {
			// No path rules — allow all traffic to this domain.
			routes = []any{
				map[string]any{
					"match": map[string]any{"prefix": "/"},
					"route": map[string]any{"cluster": "dynamic_forward_proxy_cluster"},
				},
			}
		}

		virtualHosts = append(virtualHosts, map[string]any{
			"name":    domain,
			"domains": []string{domain},
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

// buildMITMFilterChain creates a filter chain that terminates TLS with a per-domain
// certificate and inspects HTTP paths before forwarding upstream with re-encryption.
func buildMITMFilterChain(r config.EgressRule) map[string]any {
	domain := normalizeDomain(r.Dst)
	certFile := fmt.Sprintf(envoyCertFileFmt, domain)
	keyFile := fmt.Sprintf(envoyKeyFileFmt, domain)

	// Build routes from path rules.
	routes := buildHTTPRoutes(r)

	return map[string]any{
		"filter_chain_match": map[string]any{
			"server_names": []string{domain},
		},
		"transport_socket": map[string]any{
			"name": "envoy.transport_sockets.tls",
			"typed_config": map[string]any{
				"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext",
				"common_tls_context": map[string]any{
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
					"stat_prefix": fmt.Sprintf("mitm_%s", sanitizeName(domain)),
					"codec_type":  "AUTO",
					"access_log":  buildHTTPAccessLog("tls_mitm"),
					"route_config": map[string]any{
						"name": fmt.Sprintf("mitm_route_%s", sanitizeName(domain)),
						"virtual_hosts": []any{
							map[string]any{
								"name":    domain,
								"domains": []string{"*"},
								"routes":  routes,
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
func buildHTTPRoutes(r config.EgressRule) []any {
	var routes []any

	// Explicit path rules first.
	for _, pr := range r.PathRules {
		if strings.EqualFold(pr.Action, "allow") {
			routes = append(routes, map[string]any{
				"match": map[string]any{"prefix": pr.Path},
				"route": map[string]any{"cluster": "dynamic_forward_proxy_cluster"},
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
			"route": map[string]any{"cluster": "dynamic_forward_proxy_cluster"},
		})
	}

	return routes
}

// buildPassthroughFilterChain creates an SNI passthrough filter chain for an allowed domain.
// sni_dynamic_forward_proxy is non-terminal — tcp_proxy must follow it.
func buildPassthroughFilterChain(r config.EgressRule) map[string]any {
	domain := normalizeDomain(r.Dst)
	port := r.Port
	if port == 0 {
		port = 443 // Defensive default — Envoy requires port_value in (0, 65535].
	}

	return map[string]any{
		"filter_chain_match": map[string]any{
			"server_names": []string{domain},
		},
		"filters": []any{
			map[string]any{
				"name": "envoy.filters.network.sni_dynamic_forward_proxy",
				"typed_config": map[string]any{
					"@type": "type.googleapis.com/envoy.extensions.filters.network.sni_dynamic_forward_proxy.v3.FilterConfig",
					"dns_cache_config": map[string]any{
						"name":              dnsCacheName,
						"dns_lookup_family": "V4_ONLY",
					},
					"port_value": port,
				},
			},
			map[string]any{
				"name": "envoy.filters.network.tcp_proxy",
				"typed_config": map[string]any{
					"@type":        "type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy",
					"stat_prefix":  fmt.Sprintf("passthrough_%s", sanitizeName(domain)),
					"cluster":      "dynamic_forward_proxy_cluster",
					"idle_timeout": "0s",
					"access_log":   buildTCPAccessLog("tls"),
				},
			},
		},
	}
}

// buildDenyFilterChain creates the default deny filter chain that resets connections.
// No access logging — the deny chain catches all unrecognized SNI including health probes
// and internal Docker traffic, which generates noise. Blocked domains are visible via
// CoreDNS NXDOMAIN logs instead.
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

// buildClusters constructs all Envoy clusters.
func buildClusters(tcp []config.EgressRule) []any {
	clusters := []any{
		// Dynamic forward proxy cluster (used by both SNI passthrough and MITM).
		map[string]any{
			"name":            "dynamic_forward_proxy_cluster",
			"connect_timeout": "10s",
			"lb_policy":       "CLUSTER_PROVIDED",
			"cluster_type": map[string]any{
				"name": "envoy.clusters.dynamic_forward_proxy",
				"typed_config": map[string]any{
					"@type": "type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig",
					"dns_cache_config": map[string]any{
						"name":              dnsCacheName,
						"dns_lookup_family": "V4_ONLY",
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
