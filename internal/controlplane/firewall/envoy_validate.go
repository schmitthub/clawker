package firewall

import (
	"encoding/json"
	"fmt"

	bootstrapv3 "github.com/envoyproxy/go-control-plane/envoy/config/bootstrap/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"

	// Blank imports register the extension message types referenced by @type
	// URLs in the generated config, so protojson can resolve them when parsing
	// google.protobuf.Any fields. The set below covers every @type the generator
	// currently emits (HCM, tcp_proxy, udp_proxy, DFP, TLS, QUIC, access loggers,
	// router…). Add one entry per NEW @type a future generator change starts
	// emitting, or validation fails with "unable to resolve".
	_ "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/access_loggers/open_telemetry/v3"
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/access_loggers/stream/v3"
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/clusters/dynamic_forward_proxy/v3"
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/dynamic_forward_proxy/v3"
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/listener/tls_inspector/v3"
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/tcp_proxy/v3"
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/udp/udp_proxy/v3"
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/quic/v3"
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	_ "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
)

// validateBootstrap parses generated envoy.yaml against Envoy's bootstrap proto
// schema — pure Go, no Envoy binary, no sockets. It catches three classes of
// generator bug before the config is ever written:
//   - structural: an unknown / typo'd field (protojson DiscardUnknown defaults
//     to false) or a wrong value type → unmarshal error;
//   - constraint: a PGV (protoc-gen-validate) rule violation → ValidateAll error.
//
// GenerateEnvoyConfig runs this as a fail-closed self-check so the generator
// never ships a config Envoy would reject at load (which would strand the
// firewall). It does NOT prove runtime behavior (allow/deny enforcement) — that
// stays host UAT — only that the config is structurally valid Envoy.
func validateBootstrap(yamlCfg []byte) error {
	// yaml.v3 decodes mappings into map[string]any (string keys), so a plain
	// json.Marshal round-trips cleanly into the JSON protojson expects.
	var tree any
	if err := yaml.Unmarshal(yamlCfg, &tree); err != nil {
		return fmt.Errorf("yaml decode: %w", err)
	}
	jsonCfg, err := json.Marshal(tree)
	if err != nil {
		return fmt.Errorf("yaml->json: %w", err)
	}

	var bs bootstrapv3.Bootstrap
	if err := protojson.Unmarshal(jsonCfg, &bs); err != nil {
		return fmt.Errorf("bootstrap unmarshal (bad/unknown field or wrong type): %w", err)
	}
	if err := bs.ValidateAll(); err != nil {
		return fmt.Errorf("bootstrap constraints: %w", err)
	}
	return nil
}
