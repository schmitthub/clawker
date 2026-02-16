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
	if s.Monitoring.OtelGRPCPort != 4317 {
		t.Errorf("Monitoring.OtelGRPCPort = %d, want 4317", s.Monitoring.OtelGRPCPort)
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

	// Telemetry defaults
	tel := s.Monitoring.Telemetry
	if tel.MetricsPath != "/v1/metrics" {
		t.Errorf("Telemetry.MetricsPath = %q, want %q", tel.MetricsPath, "/v1/metrics")
	}
	if tel.LogsPath != "/v1/logs" {
		t.Errorf("Telemetry.LogsPath = %q, want %q", tel.LogsPath, "/v1/logs")
	}
	if tel.MetricExportIntervalMs != 10000 {
		t.Errorf("Telemetry.MetricExportIntervalMs = %d, want 10000", tel.MetricExportIntervalMs)
	}
	if tel.LogsExportIntervalMs != 5000 {
		t.Errorf("Telemetry.LogsExportIntervalMs = %d, want 5000", tel.LogsExportIntervalMs)
	}
	if !tel.IsLogToolDetailsEnabled() {
		t.Error("Telemetry.IsLogToolDetailsEnabled should be true by default")
	}
	if !tel.IsLogUserPromptsEnabled() {
		t.Error("Telemetry.IsLogUserPromptsEnabled should be true by default")
	}
	if !tel.IsIncludeAccountUUIDEnabled() {
		t.Error("Telemetry.IsIncludeAccountUUIDEnabled should be true by default")
	}
	if !tel.IsIncludeSessionIDEnabled() {
		t.Error("Telemetry.IsIncludeSessionIDEnabled should be true by default")
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

	// Telemetry defaults via Viper
	if settings.Monitoring.OtelGRPCPort != 4317 {
		t.Errorf("OtelGRPCPort = %d, want 4317 (default)", settings.Monitoring.OtelGRPCPort)
	}
	if settings.Monitoring.Telemetry.MetricExportIntervalMs != 10000 {
		t.Errorf("MetricExportIntervalMs = %d, want 10000 (default)", settings.Monitoring.Telemetry.MetricExportIntervalMs)
	}
	if settings.Monitoring.Telemetry.LogsExportIntervalMs != 5000 {
		t.Errorf("LogsExportIntervalMs = %d, want 5000 (default)", settings.Monitoring.Telemetry.LogsExportIntervalMs)
	}
	if !settings.Monitoring.Telemetry.IsLogToolDetailsEnabled() {
		t.Error("LogToolDetails should default to true")
	}
	if !settings.Monitoring.Telemetry.IsIncludeAccountUUIDEnabled() {
		t.Error("IncludeAccountUUID should default to true")
	}
}

func TestTelemetryConfig_Defaults(t *testing.T) {
	// Zero-value TelemetryConfig — getters should return defaults
	cfg := TelemetryConfig{}
	if cfg.GetMetricsPath() != "/v1/metrics" {
		t.Errorf("GetMetricsPath = %q, want %q", cfg.GetMetricsPath(), "/v1/metrics")
	}
	if cfg.GetLogsPath() != "/v1/logs" {
		t.Errorf("GetLogsPath = %q, want %q", cfg.GetLogsPath(), "/v1/logs")
	}
	if cfg.GetMetricExportIntervalMs() != 10000 {
		t.Errorf("GetMetricExportIntervalMs = %d, want 10000", cfg.GetMetricExportIntervalMs())
	}
	if cfg.GetLogsExportIntervalMs() != 5000 {
		t.Errorf("GetLogsExportIntervalMs = %d, want 5000", cfg.GetLogsExportIntervalMs())
	}
	if !cfg.IsLogToolDetailsEnabled() {
		t.Error("nil LogToolDetails should default to true")
	}
	if !cfg.IsLogUserPromptsEnabled() {
		t.Error("nil LogUserPrompts should default to true")
	}
	if !cfg.IsIncludeAccountUUIDEnabled() {
		t.Error("nil IncludeAccountUUID should default to true")
	}
	if !cfg.IsIncludeSessionIDEnabled() {
		t.Error("nil IncludeSessionID should default to true")
	}
}

func TestTelemetryConfig_CustomValues(t *testing.T) {
	f := false
	cfg := TelemetryConfig{
		MetricsPath:            "/custom/metrics",
		LogsPath:               "/custom/logs",
		MetricExportIntervalMs: 30000,
		LogsExportIntervalMs:   10000,
		LogToolDetails:         &f,
		LogUserPrompts:         &f,
		IncludeAccountUUID:     &f,
		IncludeSessionID:       &f,
	}

	if cfg.GetMetricsPath() != "/custom/metrics" {
		t.Errorf("GetMetricsPath = %q, want %q", cfg.GetMetricsPath(), "/custom/metrics")
	}
	if cfg.GetLogsPath() != "/custom/logs" {
		t.Errorf("GetLogsPath = %q, want %q", cfg.GetLogsPath(), "/custom/logs")
	}
	if cfg.GetMetricExportIntervalMs() != 30000 {
		t.Errorf("GetMetricExportIntervalMs = %d, want 30000", cfg.GetMetricExportIntervalMs())
	}
	if cfg.GetLogsExportIntervalMs() != 10000 {
		t.Errorf("GetLogsExportIntervalMs = %d, want 10000", cfg.GetLogsExportIntervalMs())
	}
	if cfg.IsLogToolDetailsEnabled() {
		t.Error("explicit false LogToolDetails should be false")
	}
	if cfg.IsLogUserPromptsEnabled() {
		t.Error("explicit false LogUserPrompts should be false")
	}
	if cfg.IsIncludeAccountUUIDEnabled() {
		t.Error("explicit false IncludeAccountUUID should be false")
	}
	if cfg.IsIncludeSessionIDEnabled() {
		t.Error("explicit false IncludeSessionID should be false")
	}
}

func TestMonitoringConfig_GetOtelGRPCPort(t *testing.T) {
	// Default (zero value)
	cfg := MonitoringConfig{}
	if cfg.GetOtelGRPCPort() != 4317 {
		t.Errorf("default GetOtelGRPCPort = %d, want 4317", cfg.GetOtelGRPCPort())
	}

	// Custom
	cfg.OtelGRPCPort = 5317
	if cfg.GetOtelGRPCPort() != 5317 {
		t.Errorf("custom GetOtelGRPCPort = %d, want 5317", cfg.GetOtelGRPCPort())
	}
}

func TestMonitoringConfig_GetMetricsEndpoint(t *testing.T) {
	cfg := MonitoringConfig{
		OtelCollectorPort:     4318,
		OtelCollectorInternal: "otel-collector",
	}
	want := "http://otel-collector:4318/v1/metrics"
	if got := cfg.GetMetricsEndpoint(); got != want {
		t.Errorf("GetMetricsEndpoint = %q, want %q", got, want)
	}
}

func TestMonitoringConfig_GetLogsEndpoint(t *testing.T) {
	cfg := MonitoringConfig{
		OtelCollectorPort:     4318,
		OtelCollectorInternal: "otel-collector",
	}
	want := "http://otel-collector:4318/v1/logs"
	if got := cfg.GetLogsEndpoint(); got != want {
		t.Errorf("GetLogsEndpoint = %q, want %q", got, want)
	}
}

func TestMonitoringConfig_CustomParts(t *testing.T) {
	cfg := MonitoringConfig{
		OtelCollectorPort:     5318,
		OtelCollectorInternal: "custom-collector",
		Telemetry: TelemetryConfig{
			MetricsPath: "/custom/metrics",
			LogsPath:    "/custom/logs",
		},
	}

	wantMetrics := "http://custom-collector:5318/custom/metrics"
	if got := cfg.GetMetricsEndpoint(); got != wantMetrics {
		t.Errorf("GetMetricsEndpoint = %q, want %q", got, wantMetrics)
	}

	wantLogs := "http://custom-collector:5318/custom/logs"
	if got := cfg.GetLogsEndpoint(); got != wantLogs {
		t.Errorf("GetLogsEndpoint = %q, want %q", got, wantLogs)
	}
}

func TestMonitoringConfig_FullPipeline(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv(ClawkerHomeEnv, tmpDir)

		loader, err := NewSettingsLoader()
		if err != nil {
			t.Fatalf("NewSettingsLoader: %v", err)
		}
		settings, err := loader.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		mon := settings.Monitoring

		t.Logf("=== MonitoringConfig Values ===")
		t.Logf("  OtelCollectorPort:     %d", mon.OtelCollectorPort)
		t.Logf("  OtelCollectorHost:     %q", mon.OtelCollectorHost)
		t.Logf("  OtelCollectorInternal: %q", mon.OtelCollectorInternal)
		t.Logf("  OtelGRPCPort:          %d", mon.OtelGRPCPort)
		t.Logf("  LokiPort:              %d", mon.LokiPort)
		t.Logf("  PrometheusPort:        %d", mon.PrometheusPort)
		t.Logf("  JaegerPort:            %d", mon.JaegerPort)
		t.Logf("  GrafanaPort:           %d", mon.GrafanaPort)
		t.Logf("  PrometheusMetricsPort: %d", mon.PrometheusMetricsPort)
		t.Logf("")
		t.Logf("=== TelemetryConfig Values ===")
		t.Logf("  MetricsPath:            %q", mon.Telemetry.GetMetricsPath())
		t.Logf("  LogsPath:               %q", mon.Telemetry.GetLogsPath())
		t.Logf("  MetricExportIntervalMs: %d", mon.Telemetry.GetMetricExportIntervalMs())
		t.Logf("  LogsExportIntervalMs:   %d", mon.Telemetry.GetLogsExportIntervalMs())
		t.Logf("  LogToolDetails:         %v", mon.Telemetry.IsLogToolDetailsEnabled())
		t.Logf("  LogUserPrompts:         %v", mon.Telemetry.IsLogUserPromptsEnabled())
		t.Logf("  IncludeAccountUUID:     %v", mon.Telemetry.IsIncludeAccountUUIDEnabled())
		t.Logf("  IncludeSessionID:       %v", mon.Telemetry.IsIncludeSessionIDEnabled())
		t.Logf("")
		t.Logf("=== Constructed URLs ===")
		t.Logf("  OtelCollectorEndpoint():    %q", mon.OtelCollectorEndpoint())
		t.Logf("  OtelCollectorInternalURL(): %q", mon.OtelCollectorInternalURL())
		t.Logf("  GetMetricsEndpoint():       %q", mon.GetMetricsEndpoint())
		t.Logf("  GetLogsEndpoint():          %q", mon.GetLogsEndpoint())
		t.Logf("  GetOtelGRPCPort():          %d", mon.GetOtelGRPCPort())
		t.Logf("  GrafanaURL():               %q", mon.GrafanaURL())
		t.Logf("  JaegerURL():                %q", mon.JaegerURL())
		t.Logf("  PrometheusURL():            %q", mon.PrometheusURL())
		t.Logf("  LokiInternalURL():          %q", mon.LokiInternalURL())

		defaults := DefaultSettings()
		if mon.OtelCollectorPort != defaults.Monitoring.OtelCollectorPort {
			t.Errorf("OtelCollectorPort = %d, want %d", mon.OtelCollectorPort, defaults.Monitoring.OtelCollectorPort)
		}
		if mon.GetOtelGRPCPort() != 4317 {
			t.Errorf("GetOtelGRPCPort = %d, want 4317", mon.GetOtelGRPCPort())
		}
		if mon.GetMetricsEndpoint() != "http://otel-collector:4318/v1/metrics" {
			t.Errorf("GetMetricsEndpoint = %q", mon.GetMetricsEndpoint())
		}
		if mon.GetLogsEndpoint() != "http://otel-collector:4318/v1/logs" {
			t.Errorf("GetLogsEndpoint = %q", mon.GetLogsEndpoint())
		}
	})

	t.Run("clawker_home_settings", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv(ClawkerHomeEnv, tmpDir)

		content := `monitoring:
  otel_collector_port: 5318
  otel_collector_host: "monitoring.internal"
  otel_collector_internal: "custom-collector"
  otel_grpc_port: 5317
  loki_port: 4100
  prometheus_port: 10090
  jaeger_port: 17686
  grafana_port: 4000
  prometheus_metrics_port: 9889
  telemetry:
    metrics_path: "/v1/metrics"
    logs_path: "/v1/logs"
    metric_export_interval_ms: 30000
    logs_export_interval_ms: 10000
    log_tool_details: false
    log_user_prompts: false
    include_account_uuid: false
    include_session_id: false
`
		if err := os.WriteFile(filepath.Join(tmpDir, SettingsFileName), []byte(content), 0644); err != nil {
			t.Fatalf("write settings: %v", err)
		}

		loader, err := NewSettingsLoader()
		if err != nil {
			t.Fatalf("NewSettingsLoader: %v", err)
		}
		settings, err := loader.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		mon := settings.Monitoring

		t.Logf("=== MonitoringConfig Values (from file) ===")
		t.Logf("  OtelCollectorPort:     %d", mon.OtelCollectorPort)
		t.Logf("  OtelGRPCPort:          %d", mon.OtelGRPCPort)
		t.Logf("  GetMetricsEndpoint():  %q", mon.GetMetricsEndpoint())
		t.Logf("  GetLogsEndpoint():     %q", mon.GetLogsEndpoint())

		if mon.OtelCollectorPort != 5318 {
			t.Errorf("OtelCollectorPort = %d, want 5318", mon.OtelCollectorPort)
		}
		if mon.OtelGRPCPort != 5317 {
			t.Errorf("OtelGRPCPort = %d, want 5317", mon.OtelGRPCPort)
		}
		if mon.GetMetricsEndpoint() != "http://custom-collector:5318/v1/metrics" {
			t.Errorf("GetMetricsEndpoint = %q", mon.GetMetricsEndpoint())
		}
		if mon.Telemetry.GetMetricExportIntervalMs() != 30000 {
			t.Errorf("MetricExportIntervalMs = %d, want 30000", mon.Telemetry.GetMetricExportIntervalMs())
		}
		if mon.Telemetry.IsLogToolDetailsEnabled() {
			t.Error("LogToolDetails should be false from file")
		}
		if mon.Telemetry.IsIncludeAccountUUIDEnabled() {
			t.Error("IncludeAccountUUID should be false from file")
		}
	})

	t.Run("env_var_override", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv(ClawkerHomeEnv, tmpDir)
		t.Setenv("CLAWKER_MONITORING_OTEL_GRPC_PORT", "6317")
		t.Setenv("CLAWKER_MONITORING_GRAFANA_PORT", "5000")

		loader, err := NewSettingsLoader()
		if err != nil {
			t.Fatalf("NewSettingsLoader: %v", err)
		}
		settings, err := loader.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		mon := settings.Monitoring

		if mon.OtelGRPCPort != 6317 {
			t.Errorf("OtelGRPCPort = %d, want 6317 (env override)", mon.OtelGRPCPort)
		}
		if mon.GrafanaPort != 5000 {
			t.Errorf("GrafanaPort = %d, want 5000 (env override)", mon.GrafanaPort)
		}
	})

	t.Run("env_var_telemetry", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv(ClawkerHomeEnv, tmpDir)
		t.Setenv("CLAWKER_MONITORING_TELEMETRY_METRIC_EXPORT_INTERVAL_MS", "1000")

		loader, err := NewSettingsLoader()
		if err != nil {
			t.Fatalf("NewSettingsLoader: %v", err)
		}
		settings, err := loader.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if settings.Monitoring.Telemetry.MetricExportIntervalMs != 1000 {
			t.Errorf("MetricExportIntervalMs = %d, want 1000 (env override)", settings.Monitoring.Telemetry.MetricExportIntervalMs)
		}
	})
}
