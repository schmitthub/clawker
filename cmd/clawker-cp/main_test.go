package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "")
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_KEY", "")
		t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", "")
		require.Nil(t, otelOptionsFromEnv())
	})

	t.Run("logs endpoint precedence over generic", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "https://logs:4319")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://generic:4319")
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "")
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_KEY", "")
		t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", "")
		opts := otelOptionsFromEnv()
		require.NotNil(t, opts)
		require.Equal(t, "logs:4319", opts.Endpoint)
		require.False(t, opts.Insecure)
	})

	t.Run("mTLS triple sets all three and forces secure", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://host:4319") // even http
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "/c.pem")
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_KEY", "/k.pem")
		t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", "/ca.pem")

		opts := otelOptionsFromEnv()
		require.NotNil(t, opts)
		require.Equal(t, "host:4319", opts.Endpoint)
		require.False(t, opts.Insecure, "mTLS forces TLS even when env says http://")
		require.Equal(t, "/c.pem", opts.ClientCertFile)
		require.Equal(t, "/k.pem", opts.ClientKeyFile)
		require.Equal(t, "/ca.pem", opts.CACertFile)
	})

	t.Run("bare host_port defaults secure", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector.prod.internal:4319")
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "")
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_KEY", "")
		t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", "")
		opts := otelOptionsFromEnv()
		require.NotNil(t, opts)
		require.Equal(t, "collector.prod.internal:4319", opts.Endpoint)
		require.False(t, opts.Insecure, "bare host:port must default to TLS")
	})
}
