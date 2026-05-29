package firewall

import (
	"testing"

	"github.com/schmitthub/clawker/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// End-to-end config-generation tests: parse a rules document the SAME way
// production does (storage.NewFromString[EgressRulesFile] — the real read
// engine), run it through NormalizeAndDedup + GenerateEnvoyConfig, then inspect
// the complete marshalled envoy.yaml. GenerateEnvoyConfig self-validates against
// Envoy's bootstrap proto, but that's only a structural backstop — these tests
// assert the actual chains/vhosts/clusters are present and correct.
//
// Each case supplies its own rules inline as YAML so the rule setup is explicit
// and self-contained per test.

func testPorts() EnvoyPorts {
	return EnvoyPorts{EgressPort: 10000, TCPPortBase: 15000, HealthPort: 10001}
}

// genFromYAML parses rulesYAML via the real storage engine, normalizes, and
// generates the complete config — returning the unmarshalled tree, the raw
// bytes, and any generation warnings.
func genFromYAML(t *testing.T, rulesYAML string, als ALSConfig) (map[string]any, []byte, []string) {
	t.Helper()
	store, err := storage.NewFromString[EgressRulesFile](rulesYAML)
	require.NoError(t, err, "parse rules yaml")
	rules, _ := NormalizeAndDedup(store.Read().Rules)

	out, warnings, err := GenerateEnvoyConfig(rules, testPorts(), als)
	require.NoError(t, err)

	var tree map[string]any
	require.NoError(t, yaml.Unmarshal(out, &tree))
	return tree, out, warnings
}

func staticListeners(t *testing.T, tree map[string]any) []any {
	t.Helper()
	sr, ok := tree["static_resources"].(map[string]any)
	require.True(t, ok, "static_resources present")
	ls, _ := sr["listeners"].([]any)
	return ls
}

func staticClusters(t *testing.T, tree map[string]any) []any {
	t.Helper()
	cs, _ := tree["static_resources"].(map[string]any)["clusters"].([]any)
	return cs
}

func listenerByName(t *testing.T, tree map[string]any, name string) map[string]any {
	t.Helper()
	for _, l := range staticListeners(t, tree) {
		lm := l.(map[string]any)
		if lm["name"] == name {
			return lm
		}
	}
	t.Fatalf("listener %q not found", name)
	return nil
}

// rawBufferHCM returns the single plaintext raw_buffer chain's HCM, asserting
// exactly one such chain exists (the C2 invariant: all plaintext hosts share
// one chain).
func rawBufferHCM(t *testing.T, listener map[string]any) map[string]any {
	t.Helper()
	chains, _ := listener["filter_chains"].([]any)
	var found []map[string]any
	for _, c := range chains {
		cm := c.(map[string]any)
		if m, _ := cm["filter_chain_match"].(map[string]any); m != nil && m["transport_protocol"] == "raw_buffer" {
			found = append(found, cm)
		}
	}
	require.Len(t, found, 1, "exactly one raw_buffer filter chain")
	filters := found[0]["filters"].([]any)
	require.Len(t, filters, 1)
	f := filters[0].(map[string]any)
	require.Equal(t, "envoy.filters.network.http_connection_manager", f["name"])
	return f["typed_config"].(map[string]any)
}

func vhostsByName(t *testing.T, hcm map[string]any) map[string][]string {
	t.Helper()
	vh, _ := hcm["route_config"].(map[string]any)["virtual_hosts"].([]any)
	out := map[string][]string{}
	for _, v := range vh {
		vm := v.(map[string]any)
		doms := vm["domains"].([]any)
		ds := make([]string, len(doms))
		for i, d := range doms {
			ds[i] = d.(string)
		}
		out[vm["name"].(string)] = ds
	}
	return out
}

// vhostByName returns the named virtual_host map from an HCM's route_config.
func vhostByName(t *testing.T, hcm map[string]any, name string) map[string]any {
	t.Helper()
	vh, _ := hcm["route_config"].(map[string]any)["virtual_hosts"].([]any)
	for _, v := range vh {
		vm := v.(map[string]any)
		if vm["name"] == name {
			return vm
		}
	}
	t.Fatalf("vhost %q not found", name)
	return nil
}

// vhostDFPDisabled reports whether the vhost carries a typed_per_filter_config
// that disables the dynamic_forward_proxy filter for the whole vhost.
func vhostDFPDisabled(t *testing.T, hcm map[string]any, vhost string) bool {
	t.Helper()
	pfc, ok := vhostByName(t, hcm, vhost)["typed_per_filter_config"].(map[string]any)
	if !ok {
		return false
	}
	cfg, ok := pfc["envoy.filters.http.dynamic_forward_proxy"].(map[string]any)
	if !ok {
		return false
	}
	return cfg["disabled"] == true
}

// vhostAllowCluster returns the cluster the vhost's first allow route targets
// (its "route.cluster"); empty if the first route is a deny/direct_response.
func vhostAllowCluster(t *testing.T, hcm map[string]any, vhost string) string {
	t.Helper()
	routes := vhostByName(t, hcm, vhost)["routes"].([]any)
	for _, r := range routes {
		if rt, ok := r.(map[string]any)["route"].(map[string]any); ok {
			return rt["cluster"].(string)
		}
	}
	return ""
}

// assertDFPCluster verifies the shared plaintext dynamic_forward_proxy cluster:
// CLUSTER_PROVIDED LB, the dynamic_forward_proxy cluster_type, a dns_cache_config
// matching the filter's by name, and NO hardcoded resolver (system → CoreDNS).
func assertDFPCluster(t *testing.T, cl map[string]any) {
	t.Helper()
	assert.Equal(t, "CLUSTER_PROVIDED", cl["lb_policy"])
	ct := cl["cluster_type"].(map[string]any)
	assert.Equal(t, "envoy.clusters.dynamic_forward_proxy", ct["name"])
	tc := ct["typed_config"].(map[string]any)
	assert.Equal(t, "type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig", tc["@type"])
	dc := tc["dns_cache_config"].(map[string]any)
	assert.Equal(t, "http_dfp_cache", dc["name"])
	assert.NotContains(t, dc, "typed_dns_resolver_config", "no hardcoded resolver — use the container's CoreDNS")
}

func clusterByName(t *testing.T, tree map[string]any, name string) map[string]any {
	t.Helper()
	for _, c := range staticClusters(t, tree) {
		cm := c.(map[string]any)
		if cm["name"] == name {
			return cm
		}
	}
	t.Fatalf("cluster %q not found", name)
	return nil
}

func assertLogicalDNS(t *testing.T, cl map[string]any, host string, port int) {
	t.Helper()
	assert.Equal(t, "LOGICAL_DNS", cl["type"])
	sa := cl["load_assignment"].(map[string]any)["endpoints"].([]any)[0].(map[string]any)["lb_endpoints"].([]any)[0].(map[string]any)["endpoint"].(map[string]any)["address"].(map[string]any)["socket_address"].(map[string]any)
	assert.Equal(t, host, sa["address"])
	assert.EqualValues(t, port, sa["port_value"])
}

// TestGenerateEnvoyConfig_HTTP_MultiHostMultiPort is the headline case: two
// ports of the same host plus a second host, all plaintext http. It proves the
// complete config handles every permutation with non-colliding chains.
func TestGenerateEnvoyConfig_HTTP_MultiHostMultiPort(t *testing.T) {
	tree, _, warnings := genFromYAML(t, `
rules:
  - dst: example.com
    proto: http
    port: 80
  - dst: .example.com
    proto: http
    port: 8080
  - dst: some.com
    proto: http
    port: 80
  - dst: .other.com
    proto: http
    port: 80
`, ALSConfig{})
	require.Empty(t, warnings)

	egress := listenerByName(t, tree, "egress")

	// A plaintext-only listener needs NO tls_inspector — raw_buffer is Envoy's
	// default with no listener filter. tls_inspector is the TLS layer's concern.
	_, hasLF := egress["listener_filters"]
	assert.False(t, hasLF, "plaintext-only egress listener must not carry a tls_inspector")

	// All http endpoints — exact AND wildcard — share ONE raw_buffer chain
	// (Envoy rejects duplicate filter_chain_match; the accumulator merges vhosts).
	hcm := rawBufferHCM(t, egress)

	// HCM hardening.
	assert.Equal(t, true, hcm["normalize_path"])
	assert.Equal(t, true, hcm["merge_slashes"])
	assert.Equal(t, "UNESCAPE_AND_REDIRECT", hcm["path_with_escaped_slashes_action"])

	// Wildcard rules are present → the shared chain carries the DFP filter before
	// the router (generation-wide, identical on every permutation's HCM).
	hf := hcm["http_filters"].([]any)
	require.Len(t, hf, 2)
	assert.Equal(t, "envoy.filters.http.dynamic_forward_proxy", hf[0].(map[string]any)["name"])
	assert.Equal(t, "envoy.filters.http.router", hf[1].(map[string]any)["name"])

	// Five vhosts, PORT-SCOPED domains — the bare host belongs only to :80.
	// Wildcard vhosts cover apex + subtree (apex[:p] and *.apex[:p]).
	vh := vhostsByName(t, hcm)
	require.Len(t, vh, 5)
	assert.ElementsMatch(t, []string{"example.com", "example.com:80"}, vh["example_com_80"])
	assert.ElementsMatch(t, []string{"some.com", "some.com:80"}, vh["some_com_80"])
	assert.ElementsMatch(t, []string{"*.example.com:8080", "example.com:8080"}, vh["wildcard_example_com_8080"])
	assert.ElementsMatch(t, []string{"*.other.com", "*.other.com:80", "other.com", "other.com:80"}, vh["wildcard_other_com_80"])
	require.Contains(t, vh, "deny_all")
	assert.Equal(t, []string{"*"}, vh["deny_all"])

	// CRITICAL trap guard: no domain may be claimed by two vhosts — Envoy
	// rejects duplicate domains across vhosts in one route_config, and
	// validateBootstrap does NOT catch it (it's an RDS-load semantic check).
	owner := map[string]string{}
	for name, doms := range vh {
		for _, d := range doms {
			if prev, dup := owner[d]; dup {
				t.Fatalf("domain %q claimed by both %q and %q", d, prev, name)
			}
			owner[d] = name
		}
	}

	// SECURITY: the DFP filter must be DISABLED on every vhost that does not
	// follow the Host (exact-allow + deny_all) so it never pre-resolves (and
	// 503s) a request bound for a pinned LOGICAL_DNS cluster or a direct_response
	// 403. It must stay ENABLED only on the wildcard vhosts.
	assert.True(t, vhostDFPDisabled(t, hcm, "example_com_80"), "exact vhost disables DFP")
	assert.True(t, vhostDFPDisabled(t, hcm, "some_com_80"), "exact vhost disables DFP")
	assert.True(t, vhostDFPDisabled(t, hcm, "deny_all"), "deny_all disables DFP")
	assert.False(t, vhostDFPDisabled(t, hcm, "wildcard_example_com_8080"), "wildcard vhost keeps DFP live")
	assert.False(t, vhostDFPDisabled(t, hcm, "wildcard_other_com_80"), "wildcard vhost keeps DFP live")

	// Routes target the right upstream: exact → pinned LOGICAL_DNS cluster;
	// wildcard → the shared DFP cluster (the DFP LB dials the actual subdomain).
	assert.Equal(t, "http_example_com_80", vhostAllowCluster(t, hcm, "example_com_80"))
	assert.Equal(t, "http_some_com_80", vhostAllowCluster(t, hcm, "some_com_80"))
	assert.Equal(t, "http_dfp", vhostAllowCluster(t, hcm, "wildcard_example_com_8080"))
	assert.Equal(t, "http_dfp", vhostAllowCluster(t, hcm, "wildcard_other_com_80"))

	// Clusters: one LOGICAL_DNS per EXACT endpoint + ONE shared DFP cluster for
	// all wildcard rules (no per-wildcard cluster, no apex-pinned LOGICAL_DNS).
	require.Len(t, staticClusters(t, tree), 3)
	assertLogicalDNS(t, clusterByName(t, tree, "http_example_com_80"), "example.com", 80)
	assertLogicalDNS(t, clusterByName(t, tree, "http_some_com_80"), "some.com", 80)
	assertDFPCluster(t, clusterByName(t, tree, "http_dfp"))
}

// TestGenerateEnvoyConfig_HTTP_NoWildcard_NoDFP is the inverse guard: with only
// exact http rules the shared chain must stay DFP-free — router-only filters, no
// per-vhost disable, no dynamic_forward_proxy cluster. (dfpActive is the only
// thing that should ever introduce DFP.)
func TestGenerateEnvoyConfig_HTTP_NoWildcard_NoDFP(t *testing.T) {
	tree, raw, _ := genFromYAML(t, `
rules:
  - dst: example.com
    proto: http
    port: 80
  - dst: some.com
    proto: http
`, ALSConfig{})

	hcm := rawBufferHCM(t, listenerByName(t, tree, "egress"))
	hf := hcm["http_filters"].([]any)
	require.Len(t, hf, 1, "no wildcard rule → no DFP filter, router only")
	assert.Equal(t, "envoy.filters.http.router", hf[0].(map[string]any)["name"])

	assert.False(t, vhostDFPDisabled(t, hcm, "example_com_80"), "no DFP filter → nothing to disable")
	assert.False(t, vhostDFPDisabled(t, hcm, "deny_all"))
	assert.NotContains(t, string(raw), "dynamic_forward_proxy", "no DFP cluster or filter anywhere")

	for _, c := range staticClusters(t, tree) {
		assert.NotEqual(t, "http_dfp", c.(map[string]any)["name"])
	}
}

// TestGenerateEnvoyConfig_HTTP_WildcardSubdomainReachesDFP proves a single
// wildcard rule routes its whole subtree through the shared DFP upstream (the
// fix for the apex-pin bug: a subdomain must NOT be silently dialed at the apex).
func TestGenerateEnvoyConfig_HTTP_WildcardSubdomainReachesDFP(t *testing.T) {
	tree, _, _ := genFromYAML(t, `
rules:
  - dst: .api.example.com
    proto: http
`, ALSConfig{})

	hcm := rawBufferHCM(t, listenerByName(t, tree, "egress"))
	vh := vhostsByName(t, hcm)
	require.Contains(t, vh, "wildcard_api_example_com_80")
	assert.ElementsMatch(t, []string{"*.api.example.com", "*.api.example.com:80", "api.example.com", "api.example.com:80"}, vh["wildcard_api_example_com_80"])

	// The wildcard vhost keeps DFP live and routes to the shared DFP cluster —
	// the DFP LB dials whatever subdomain arrives, not a pinned apex.
	assert.False(t, vhostDFPDisabled(t, hcm, "wildcard_api_example_com_80"))
	assert.Equal(t, "http_dfp", vhostAllowCluster(t, hcm, "wildcard_api_example_com_80"))

	// No apex-pinned LOGICAL_DNS cluster was emitted for the wildcard.
	require.Len(t, staticClusters(t, tree), 1)
	assertDFPCluster(t, clusterByName(t, tree, "http_dfp"))
}

// TestGenerateEnvoyConfig_HTTP_PathRules verifies path rules sort longest-prefix
// first and that deny is a 403 direct_response.
func TestGenerateEnvoyConfig_HTTP_PathRules(t *testing.T) {
	tree, _, _ := genFromYAML(t, `
rules:
  - dst: x.com
    proto: http
    port: 80
    path_default: deny
    path_rules:
      - path: /api
        action: allow
      - path: /api/secret
        action: deny
`, ALSConfig{})

	hcm := rawBufferHCM(t, listenerByName(t, tree, "egress"))
	routes := hcm["route_config"].(map[string]any)["virtual_hosts"].([]any)[0].(map[string]any)["routes"].([]any)
	require.Len(t, routes, 3)

	assert.Equal(t, "/api/secret", routes[0].(map[string]any)["match"].(map[string]any)["prefix"], "longest prefix first")
	assert.Contains(t, routes[0].(map[string]any), "direct_response")
	assert.EqualValues(t, 403, routes[0].(map[string]any)["direct_response"].(map[string]any)["status"])
	assert.Equal(t, "/api", routes[1].(map[string]any)["match"].(map[string]any)["prefix"])
	assert.Contains(t, routes[1].(map[string]any), "route")
	assert.Equal(t, "/", routes[2].(map[string]any)["match"].(map[string]any)["prefix"])
	assert.Contains(t, routes[2].(map[string]any), "direct_response", "path_default: deny → trailing 403")
}

// TestGenerateEnvoyConfig_HTTP_WebSocketDeniedByDefault: a plain http rule emits
// no upgrade_configs — websocket upgrades are denied until a ws token adds them.
func TestGenerateEnvoyConfig_HTTP_WebSocketDeniedByDefault(t *testing.T) {
	_, raw, _ := genFromYAML(t, `
rules:
  - dst: example.com
    proto: http
`, ALSConfig{})
	assert.NotContains(t, string(raw), "upgrade_configs")
	assert.NotContains(t, string(raw), "websocket")
}

// TestGenerateEnvoyConfig_HTTP_OtelAccessLogGatedOnMTLS: the OTel ALS sink only
// appears when ALSConfig.MTLS is true; stdout is always present.
func TestGenerateEnvoyConfig_HTTP_OtelAccessLogGatedOnMTLS(t *testing.T) {
	const r = "rules:\n  - dst: example.com\n    proto: http\n"

	_, plain, _ := genFromYAML(t, r, ALSConfig{})
	assert.Contains(t, string(plain), "envoy.access_loggers.stdout")
	assert.NotContains(t, string(plain), "open_telemetry")

	_, mtls, _ := genFromYAML(t, r, ALSConfig{Port: 4319, MTLS: true})
	assert.Contains(t, string(mtls), "envoy.access_loggers.open_telemetry")
}

// TestGenerateEnvoyConfig_UnsupportedProtoWarnsAndSkips: tokens not yet built
// (ssh, here) are skipped with a warning, not emitted.
func TestGenerateEnvoyConfig_UnsupportedProtoWarnsAndSkips(t *testing.T) {
	tree, _, warnings := genFromYAML(t, `
rules:
  - dst: host.com
    proto: ssh
    port: 22
`, ALSConfig{})
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "ssh")

	// No egress listener emitted (nothing supported was derived).
	for _, l := range staticListeners(t, tree) {
		assert.NotEqual(t, "egress", l.(map[string]any)["name"])
	}
}
