package firewall

import (
	"maps"
	"sort"
)

// envoy_accesslog.go holds the access-log builders shared by every HCM and
// tcp_proxy clawker emits. Two sinks: a stdout JSON sink (always, for
// `docker logs clawker-envoy` triage) and the OpenTelemetry ALS sink (only when
// ALSConfig.MTLS is true — without mTLS material we cannot reach the trusted
// otlp/infra receiver, and the untrusted otel-collector:4317 lane is reserved
// for agent containers). Fields follow OTel network/server/client/tls semconv.

// buildHTTPAccessLog returns access loggers for an HCM. HTTP-specific fields
// (method/path/response_code) are included. server.address travels uniformly:
// SNI (%REQUESTED_SERVER_NAME%) for TLS, Host (%REQ(Host)%) for plaintext where
// SNI is unavailable.
func buildHTTPAccessLog(tlsTerminated bool, transport, action string, als ALSConfig) []any {
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
		// Plaintext HTTP: SNI is unavailable, so stamp server.address from the
		// Host/:authority header instead of %REQUESTED_SERVER_NAME%.
		extra["server.address"] = "%REQ(Host)%"
	}
	// transport is the ACTUAL L4 (tcp for the TCP egress chains, quic for the
	// HTTP/3-over-QUIC chains) — never hardcoded, so the QUIC HCM that reuses this
	// app block reports quic, not tcp.
	sinks := []any{stdoutAccessLogEntry(transport, "http", tlsEst, action, extra)}
	if als.MTLS {
		sinks = append(sinks, otelAccessLogEntry(transport, "http", tlsEst, action, extra))
	}
	return sinks
}

// buildTCPAccessLog returns access loggers for an opaque L4 proxy (tcp_proxy /
// udp_proxy). No HTTP fields — there is no L7 to inspect. The verdict is the
// caller-supplied action literal, not per-route metadata: an opaque chain pins a
// single cluster, so the chain itself decides the verdict at generation — an
// allow terminal passes "allowed" (the pin IS the gate), a deny terminal passes
// "denied" (blackholed to the deny cluster). transport is the actual L4
// ("tcp"/"udp"); l7Proto is the opaque app token ("ssh"/"tcp"/"udp") recorded as
// network.protocol.name; serverAddress is the pinned host literal (no SNI/Host is
// available on an opaque connection). tls.established is always false (no TLS
// terminated here).
func buildTCPAccessLog(transport, l7Proto, serverAddress, action string, als ALSConfig) []any {
	extra := map[string]string{"server.address": serverAddress}
	sinks := []any{stdoutAccessLogEntry(transport, l7Proto, "false", action, extra)}
	if als.MTLS {
		sinks = append(sinks, otelAccessLogEntry(transport, l7Proto, "false", action, extra))
	}
	return sinks
}

// accessLogFields is the canonical field map shared by both sinks so a rename
// updates both at once. `action` carries the clawker verdict (allowed/denied),
// stamped at generation time — for mixed-verdict HCMs the call site passes the
// %METADATA(ROUTE:clawker:action)% token so Envoy copies per-route metadata at
// emit time. The verdict is NEVER inferred from response_code.
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
	if tlsEstablished != "" {
		f["tls.established"] = tlsEstablished
	}
	maps.Copy(f, extra)
	return f
}

// stdoutAccessLogEntry builds the JSON stdout access-log sink.
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

// otelAccessLogEntry builds the OpenTelemetry ALS sink (OTLP/gRPC to the
// otel-collector, tagged service.name=envoy). Attribute order is sorted for
// deterministic output.
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
				"envoy_grpc": map[string]any{"cluster_name": otelCollectorALSClusterName},
			},
			"resource_attributes": map[string]any{
				"values": []any{
					map[string]any{
						"key":   "service.name",
						"value": map[string]any{"string_value": "envoy"},
					},
				},
			},
			"body":       map[string]any{"string_value": "envoy access_log"},
			"attributes": map[string]any{"values": values},
		},
	}
}
