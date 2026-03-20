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
}

// Cert path formats inside the Envoy container (mounted volume).
const (
	envoyCertFileFmt = "/etc/envoy/certs/%s-cert.pem"
	envoyKeyFileFmt  = "/etc/envoy/certs/%s-key.pem"
)

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
	)
	for _, r := range rules {
		if strings.EqualFold(r.Action, "deny") {
			continue // Deny rules are handled by the default deny chain.
		}
		if isIPOrCIDR(r.Dst) {
			warnings = append(warnings, fmt.Sprintf("skipping IP/CIDR rule %q (not supported in Envoy SNI proxy)", r.Dst))
			continue
		}
		proto := strings.ToLower(r.Proto)
		if proto == "ssh" || proto == "tcp" {
			tcpRules = append(tcpRules, r)
			continue
		}
		if len(r.PathRules) > 0 {
			mitmRules = append(mitmRules, r)
		} else {
			passthroughRules = append(passthroughRules, r)
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
			"listeners": buildListeners(mitmRules, passthroughRules, tcpRules, ports),
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
func buildListeners(mitm, passthrough, tcp []config.EgressRule, ports EnvoyPorts) []any {
	var listeners []any

	// Main TLS listener — handles both MITM and passthrough.
	if len(mitm) > 0 || len(passthrough) > 0 {
		listeners = append(listeners, buildTLSListener(mitm, passthrough, ports.TLSPort))
	}

	// Per-rule TCP/SSH listeners on sequential ports.
	for i, r := range tcp {
		port := ports.TCPPortBase + i
		listeners = append(listeners, buildTCPListener(r, port))
	}

	return listeners
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
	port := r.Port // Already normalized by addRulesToStore (TLS defaults to 443).

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
				},
			},
		},
	}
}

// buildDenyFilterChain creates the default deny filter chain that resets connections.
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
