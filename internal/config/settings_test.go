package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSettings_Populated(t *testing.T) {
	s := DefaultSettings()

	// Logging defaults
	if !s.Logging.IsFileEnabled() {
		t.Error("Logging.IsFileEnabled should be true by default")
	}
	if s.Logging.MaxSizeMB != 50 {
		t.Errorf("Logging.MaxSizeMB = %d, want 50", s.Logging.MaxSizeMB)
	}
	if s.Logging.MaxAgeDays != 7 {
		t.Errorf("Logging.MaxAgeDays = %d, want 7", s.Logging.MaxAgeDays)
	}
	if s.Logging.MaxBackups != 3 {
		t.Errorf("Logging.MaxBackups = %d, want 3", s.Logging.MaxBackups)
	}
	if !s.Logging.IsCompressEnabled() {
		t.Error("Logging.IsCompressEnabled should be true by default")
	}

	// OTEL defaults
	if !s.Logging.Otel.IsEnabled() {
		t.Error("Logging.Otel.IsEnabled should be true by default")
	}
	if s.Logging.Otel.GetTimeoutSeconds() != 5 {
		t.Errorf("Otel.GetTimeoutSeconds = %d, want 5", s.Logging.Otel.GetTimeoutSeconds())
	}
	if s.Logging.Otel.GetMaxQueueSize() != 2048 {
		t.Errorf("Otel.GetMaxQueueSize = %d, want 2048", s.Logging.Otel.GetMaxQueueSize())
	}
	if s.Logging.Otel.GetExportIntervalSeconds() != 5 {
		t.Errorf("Otel.GetExportIntervalSeconds = %d, want 5", s.Logging.Otel.GetExportIntervalSeconds())
	}

	// Monitoring defaults
	if s.Monitoring.OtelCollectorPort != 4318 {
		t.Errorf("Monitoring.OtelCollectorPort = %d, want 4318", s.Monitoring.OtelCollectorPort)
	}
	if s.Monitoring.OtelCollectorHost != "localhost" {
		t.Errorf("Monitoring.OtelCollectorHost = %q, want %q", s.Monitoring.OtelCollectorHost, "localhost")
	}
	if s.Monitoring.OtelCollectorInternal != "otel-collector" {
		t.Errorf("Monitoring.OtelCollectorInternal = %q, want %q", s.Monitoring.OtelCollectorInternal, "otel-collector")
	}
	if s.Monitoring.LokiPort != 3100 {
		t.Errorf("Monitoring.LokiPort = %d, want 3100", s.Monitoring.LokiPort)
	}
	if s.Monitoring.PrometheusPort != 9090 {
		t.Errorf("Monitoring.PrometheusPort = %d, want 9090", s.Monitoring.PrometheusPort)
	}
	if s.Monitoring.JaegerPort != 16686 {
		t.Errorf("Monitoring.JaegerPort = %d, want 16686", s.Monitoring.JaegerPort)
	}
	if s.Monitoring.GrafanaPort != 3000 {
		t.Errorf("Monitoring.GrafanaPort = %d, want 3000", s.Monitoring.GrafanaPort)
	}
	if s.Monitoring.PrometheusMetricsPort != 8889 {
		t.Errorf("Monitoring.PrometheusMetricsPort = %d, want 8889", s.Monitoring.PrometheusMetricsPort)
	}
}

func TestMonitoringConfig_DerivedURLs(t *testing.T) {
	cfg := MonitoringConfig{
		OtelCollectorPort:     4318,
		OtelCollectorHost:     "localhost",
		OtelCollectorInternal: "otel-collector",
		LokiPort:              3100,
		PrometheusPort:        9090,
		JaegerPort:            16686,
		GrafanaPort:           3000,
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"OtelCollectorEndpoint", cfg.OtelCollectorEndpoint(), "localhost:4318"},
		{"OtelCollectorInternalURL", cfg.OtelCollectorInternalURL(), "http://otel-collector:4318"},
		{"LokiInternalURL", cfg.LokiInternalURL(), "http://loki:3100/otlp"},
		{"GrafanaURL", cfg.GrafanaURL(), "http://localhost:3000"},
		{"JaegerURL", cfg.JaegerURL(), "http://localhost:16686"},
		{"PrometheusURL", cfg.PrometheusURL(), "http://localhost:9090"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestMonitoringConfig_CustomPorts(t *testing.T) {
	cfg := MonitoringConfig{
		OtelCollectorPort:     5318,
		OtelCollectorHost:     "myhost",
		OtelCollectorInternal: "my-collector",
		LokiPort:              4100,
		GrafanaPort:           4000,
		JaegerPort:            17686,
		PrometheusPort:        10090,
	}

	if cfg.OtelCollectorEndpoint() != "myhost:5318" {
		t.Errorf("custom endpoint = %q", cfg.OtelCollectorEndpoint())
	}
	if cfg.GrafanaURL() != "http://localhost:4000" {
		t.Errorf("custom GrafanaURL = %q", cfg.GrafanaURL())
	}
}

func TestLoggingConfig_BoolGetters(t *testing.T) {
	// Nil guard — zero-value struct
	cfg := LoggingConfig{}
	if !cfg.IsFileEnabled() {
		t.Error("nil FileEnabled should default to true")
	}
	if !cfg.IsCompressEnabled() {
		t.Error("nil Compress should default to true")
	}

	// Explicit false
	f := false
	cfg.FileEnabled = &f
	cfg.Compress = &f
	if cfg.IsFileEnabled() {
		t.Error("explicit false FileEnabled should be false")
	}
	if cfg.IsCompressEnabled() {
		t.Error("explicit false Compress should be false")
	}
}

func TestOtelConfig_Defaults(t *testing.T) {
	// Zero-value OtelConfig — getters should return defaults
	cfg := OtelConfig{}
	if !cfg.IsEnabled() {
		t.Error("nil Enabled should default to true")
	}
	if cfg.GetTimeoutSeconds() != 5 {
		t.Errorf("GetTimeoutSeconds = %d, want 5", cfg.GetTimeoutSeconds())
	}
	if cfg.GetMaxQueueSize() != 2048 {
		t.Errorf("GetMaxQueueSize = %d, want 2048", cfg.GetMaxQueueSize())
	}
	if cfg.GetExportIntervalSeconds() != 5 {
		t.Errorf("GetExportIntervalSeconds = %d, want 5", cfg.GetExportIntervalSeconds())
	}
}

func TestSettingsLoader_EnvOverride(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClawkerHomeEnv, tmpDir)

	// Write settings file with grafana_port: 3000
	content := `monitoring:
  grafana_port: 3000
`
	if err := os.WriteFile(filepath.Join(tmpDir, SettingsFileName), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write settings: %v", err)
	}

	// ENV override should win
	t.Setenv("CLAWKER_MONITORING_GRAFANA_PORT", "4000")

	loader, err := NewSettingsLoader()
	if err != nil {
		t.Fatalf("NewSettingsLoader() error: %v", err)
	}

	settings, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if settings.Monitoring.GrafanaPort != 4000 {
		t.Errorf("GrafanaPort = %d, want 4000 (ENV override)", settings.Monitoring.GrafanaPort)
	}
}

func TestSettingsLoader_Defaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClawkerHomeEnv, tmpDir)

	// No settings file — should use defaults
	loader, err := NewSettingsLoader()
	if err != nil {
		t.Fatalf("NewSettingsLoader() error: %v", err)
	}

	settings, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	defaults := DefaultSettings()
	if settings.Monitoring.OtelCollectorPort != defaults.Monitoring.OtelCollectorPort {
		t.Errorf("OtelCollectorPort = %d, want %d (default)", settings.Monitoring.OtelCollectorPort, defaults.Monitoring.OtelCollectorPort)
	}
	if settings.Monitoring.GrafanaPort != defaults.Monitoring.GrafanaPort {
		t.Errorf("GrafanaPort = %d, want %d (default)", settings.Monitoring.GrafanaPort, defaults.Monitoring.GrafanaPort)
	}
	if !settings.Logging.IsFileEnabled() {
		t.Error("FileEnabled should default to true")
	}
}
