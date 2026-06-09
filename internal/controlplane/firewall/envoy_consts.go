package firewall

// Envoy config-generation constants. Magic values used by the layered
// generator and the cross-layer glue live here so callers never hardcode them.
const (
	// envoyAdminPort is the loopback-only Envoy admin endpoint port.
	envoyAdminPort = 9901

	// egressListenerName is the shared TCP egress listener's name. http/https
	// ride it (eBPF connect4 redirects them here); raw tcp/ssh get dedicated
	// listeners.
	egressListenerName = "egress"

	// egressQUICListenerName is the shared UDP/QUIC egress listener — the h3 peer
	// of egressListenerName, bound to the SAME port over UDP. https/wss rules
	// emit a QUIC filter chain here (HTTP/3) in addition to their TCP tls chain;
	// the TCP chain advertises h3 via alt-svc so clients discover it.
	egressQUICListenerName = "egress_quic"

	// healthListenerName is the dedicated readiness listener (a 200-OK HTTP
	// endpoint bound to EnvoyPorts.HealthPort). Stack.EnsureRunning probes
	// http://<EnvoyIP>:HealthPort/ on a non-cancellable context until it answers
	// — so this listener MUST be emitted whenever HealthPort > 0, or firewall
	// bringup hangs forever (the stack comes up but route-seed + agent
	// re-enrollment never run). GenerateEnvoyConfig fail-closes if it is missing.
	healthListenerName = "health_check"

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

	// httpsDFPClusterName / httpsDFPCacheName name the single shared https
	// (TLS-reencrypt) dynamic-forward-proxy cluster + its DNS cache. The https
	// peer of httpDFPClusterName: one DFP cluster serves every wildcard-https
	// rule, Host-keyed (the DFP LB derives upstream host+port from :authority),
	// with an UpstreamTlsContext re-encrypt socket. Distinct cache from the
	// plaintext one so the secure-upstream default port (443) is honored.
	httpsDFPClusterName = "https_dfp"
	httpsDFPCacheName   = "https_dfp_cache"

	// wssDFPClusterName is the h1.1-pinned dynamic-forward-proxy cluster for
	// wildcard wss (websocket-over-TLS) origins. It shares httpsDFPCacheName with
	// httpsDFPClusterName but pins the upstream to HTTP/1.1 (WS-native) via
	// explicit_http_config — a distinct body, so it needs its own name to avoid an
	// AddCluster dedup clash with the auto-config https_dfp when both a plain
	// https-wildcard and a wss-wildcard rule are present.
	wssDFPClusterName = "wss_dfp"

	// denyClusterName is the STATIC, zero-endpoint cluster backing the egress
	// listener's default_filter_chain: any flow matching no allow chain —
	// a TLS connection with unknown/absent SNI, or a raw-TCP flow to a
	// disallowed host/port — is tcp_proxy'd here and reset.
	denyClusterName = "deny_cluster"

	// Per-domain MITM cert file paths inside the Envoy container. certs.go signs
	// these against the firewall CA and stack.go bind-mounts the dir; the
	// generator only references the filenames. %s = certBasename(dst) — the flat
	// on-disk stem (folds a CIDR's "/" to "_", e.g. 10.0.0.0/8 → 10.0.0.0_8) so
	// the ref matches the cert certs.go writes.
	envoyCertFileFmt = "/etc/envoy/certs/%s-cert.pem"
	envoyKeyFileFmt  = "/etc/envoy/certs/%s-key.pem"

	// upstreamTrustedCAFile is the system CA bundle Envoy validates re-encrypted
	// upstream TLS against (the real server's real cert — NOT the MITM CA).
	upstreamTrustedCAFile = "/etc/ssl/certs/ca-certificates.crt"

	// firewallBlockedBody is the response body for every clawker direct_response
	// 403 (per-path deny, deny_all vhost). Generic and non-fingerprinting — an
	// injected-prompt adversary should not trivially distinguish a clawker block
	// from a generic upstream "Forbidden". The verdict travels on the `action`
	// access-log field, never the body.
	firewallBlockedBody = "Forbidden\n"
)
