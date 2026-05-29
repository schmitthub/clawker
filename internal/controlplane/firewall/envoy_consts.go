package firewall

// Envoy config-generation constants. Magic values used by the layered
// generator and the cross-layer glue live here so callers never hardcode them.
const (
	// envoyAdminPort is the loopback-only Envoy admin endpoint port.
	envoyAdminPort = 9901

	// egressListenerName is the shared egress listener's name. http/https ride
	// it (eBPF connect4 redirects them here); raw tcp/ssh get dedicated
	// listeners.
	egressListenerName = "egress"

	// defaultBindAddress is the address Envoy listeners bind inside the
	// firewall container.
	defaultBindAddress = "0.0.0.0"

	// Default destination ports applied when an EgressRule omits Port.
	defaultDestPort = 443 // generic TCP / https default
	sshDefaultPort  = 22  // ssh
	defaultHTTPPort = 80  // plaintext http

	// otelCollectorALSClusterName is the cluster the OpenTelemetry access-log
	// sink dials (only emitted when ALSConfig.MTLS is true).
	otelCollectorALSClusterName = "otel_collector_als"

	// dynamicForwardProxyFilterName is the fixed Envoy HTTP filter name for the
	// dynamic forward proxy. It is the typed_per_filter_config map key the app
	// block uses to disable DFP on non-wildcard vhosts.
	dynamicForwardProxyFilterName = "envoy.filters.http.dynamic_forward_proxy"

	// httpDFPClusterName / httpDFPCacheName name the single shared plaintext-HTTP
	// dynamic-forward-proxy cluster and its DNS cache (the filter and the cluster
	// must reference the same cache by name). One DFP cluster serves every
	// wildcard-http rule: the DFP load balancer derives the upstream host AND port
	// from the request :authority, and the per-rule vhost domains gate which Hosts
	// reach it — so port-scoping rides the authority, no per-port cluster needed.
	httpDFPClusterName = "http_dfp"
	httpDFPCacheName   = "http_dfp_cache"

	// firewallBlockedBody is the response body for every clawker direct_response
	// 403 (per-path deny, deny_all vhost). Generic and non-fingerprinting — an
	// injected-prompt adversary should not trivially distinguish a clawker block
	// from a generic upstream "Forbidden". The verdict travels on the `action`
	// access-log field, never the body.
	firewallBlockedBody = "Forbidden\n"
)
