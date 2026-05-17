package main

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/internal/logger"
	"github.com/stretchr/testify/require"
)

// TestWireInitExecutor_NilBus pins the CP-resilience contract: a
// regression that swaps the `if err != nil { return nil }` for
// `panic(err)` would crash CP and strand eBPF programs unsupervised,
// silently breaking the firewall enforcement boundary. The structured
// `event=<subsystem>_unavailable` log line is the only triage surface
// operators have once the wrapper degrades.
func TestWireInitExecutor_NilBus(t *testing.T) {
	var buf bytes.Buffer
	log := logger.NewWriter(&buf)

	exec := wireInitExecutor(nil, log)

	require.Nil(t, exec, "nil bus must yield nil Executor (degrade), not crash")
	require.Contains(t, buf.String(), "agent_init_executor_unavailable",
		"degraded path must emit the structured event so operators can triage")
}

func TestParseOtlpEndpoint(t *testing.T) {
	cases := []struct {
		name         string
		raw          string
		wantEndpoint string
		wantInsecure bool
	}{
		{"bare host_port defaults secure", "host.docker.internal:4319", "host.docker.internal:4319", false},
		{"https stays secure", "https://host.docker.internal:4319", "host.docker.internal:4319", false},
		{"explicit http opts in to plaintext", "http://collector:4317", "collector:4317", true},
		{"https with path strips path", "https://host:4319/v1/logs", "host:4319", false},
		{"http with path strips path", "http://host:4318/v1/logs", "host:4318", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint, insecure := parseOtlpEndpoint(tc.raw)
			require.Equal(t, tc.wantEndpoint, endpoint)
			require.Equal(t, tc.wantInsecure, insecure)
		})
	}
}

func TestOtelOptionsFromEnv(t *testing.T) {
	t.Run("no env returns nil", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		require.Nil(t, otelOptionsFromEnv())
	})

	t.Run("logs endpoint precedence over generic", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "https://logs:4319")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://generic:4319")
		opts := otelOptionsFromEnv()
		require.NotNil(t, opts)
		require.Equal(t, "logs:4319", opts.Endpoint)
		require.False(t, opts.Insecure)
	})

	// CLI-root-direct cert env vars are deliberately ignored. The CP's
	// trusted-lane exporter takes its TLSConfig in-process from
	// internal/controlplane/otelcerts; allowing env-driven cert paths
	// would let an operator smuggle in a CLI-root-direct leaf, which
	// agent containers also hold — they could then forge
	// service.name=clawker-cp records on the trusted receiver.
	t.Run("client cert env vars are not consulted", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "https://host:4319")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "/c.pem")
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_KEY", "/k.pem")
		t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", "/ca.pem")

		opts := otelOptionsFromEnv()
		require.NotNil(t, opts)
		require.Empty(t, opts.ClientCertFile, "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE must be ignored by the CP wiring")
		require.Empty(t, opts.ClientKeyFile, "OTEL_EXPORTER_OTLP_CLIENT_KEY must be ignored by the CP wiring")
		require.Empty(t, opts.CACertFile, "OTEL_EXPORTER_OTLP_CERTIFICATE must be ignored by the CP wiring")
		require.Nil(t, opts.TLSConfig, "TLSConfig is wired in-process by main, not from env")
	})

	t.Run("bare host_port defaults secure", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector.prod.internal:4319")
		opts := otelOptionsFromEnv()
		require.NotNil(t, opts)
		require.Equal(t, "collector.prod.internal:4319", opts.Endpoint)
		require.False(t, opts.Insecure, "bare host:port must default to TLS")
	})
}
