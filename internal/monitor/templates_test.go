package monitor

import (
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
)

// newTestMonitorConfig creates a Manager with a mock config pointing at the given temp dir.
func newTestMonitorConfig(t *testing.T, dir string) config.MonitoringConfig {
	t.Helper()
	cfg := config.NewMockConfig()
	t.Setenv(cfg.ConfigDirEnvVar(), dir)

	return cfg.MonitoringConfig()
}

func TestNewMonitorTemplateData(t *testing.T) {
	cfg := &config.MonitoringConfig{
		OtelCollectorPort:     4318,
		OtelCollectorHost:     "localhost",
		OtelCollectorInternal: "otel-collector",
		OtelGRPCPort:          4317,
		LokiPort:              3100,
		PrometheusPort:        9090,
		JaegerPort:            16686,
		GrafanaPort:           3000,
		PrometheusMetricsPort: 8889,
	}

	data := NewMonitorTemplateData(cfg)

	if data.OtelCollectorPort != 4318 {
		t.Errorf("OtelCollectorPort = %d, want 4318", data.OtelCollectorPort)
	}
	if data.OtelGRPCPort != 4317 {
		t.Errorf("OtelGRPCPort = %d, want 4317 (from config)", data.OtelGRPCPort)
	}
	if data.OtelCollectorInternal != "otel-collector" {
		t.Errorf("OtelCollectorInternal = %q, want %q", data.OtelCollectorInternal, "otel-collector")
	}
}

func TestNewMonitorTemplateData_CustomGRPCPort(t *testing.T) {
	// gRPC port is independent — not derived from HTTP port
	cfg := &config.MonitoringConfig{
		OtelCollectorPort: 5318,
		OtelGRPCPort:      5317,
	}

	data := NewMonitorTemplateData(cfg)
	if data.OtelGRPCPort != 5317 {
		t.Errorf("OtelGRPCPort = %d, want 5317", data.OtelGRPCPort)
	}
}

func TestNewMonitorTemplateData_DefaultGRPCPort(t *testing.T) {
	// Zero OtelGRPCPort should use default 4317 via getter
	cfg := &config.MonitoringConfig{
		OtelCollectorPort: 4318,
	}

	data := NewMonitorTemplateData(cfg)
	if data.OtelGRPCPort != 4317 {
		t.Errorf("OtelGRPCPort = %d, want 4317 (default from getter)", data.OtelGRPCPort)
	}
}

func TestRenderTemplate_Compose(t *testing.T) {
	data := MonitorTemplateData{
		OtelCollectorPort:     5318,
		OtelGRPCPort:          5317,
		LokiPort:              4100,
		PrometheusPort:        10090,
		JaegerPort:            17686,
		GrafanaPort:           4000,
		PrometheusMetricsPort: 9889,
		OtelCollectorInternal: "my-collector",
	}

	result, err := RenderTemplate("compose.yaml", ComposeTemplate, data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	// Verify custom ports appear in output (not defaults)
	checks := []struct {
		desc    string
		contain string
	}{
		{"OTEL HTTP port", "5318:5318"},
		{"OTEL gRPC port", "5317:5317"},
		{"Jaeger port", "17686:17686"},
		{"Prometheus port", "10090:10090"},
		{"Loki port", "4100:4100"},
		{"Grafana port", "4000:4000"},
	}

	for _, check := range checks {
		if !strings.Contains(result, check.contain) {
			t.Errorf("compose.yaml should contain %s (%s), output:\n%s", check.contain, check.desc, result)
		}
	}

	// Verify no hardcoded defaults remain
	if strings.Contains(result, "4318:4318") {
		t.Error("compose.yaml should not contain default port 4318 when custom port is set")
	}
}

func TestRenderTemplate_OtelConfig(t *testing.T) {
	data := MonitorTemplateData{
		OtelCollectorPort:     5318,
		OtelGRPCPort:          5317,
		LokiPort:              4100,
		PrometheusMetricsPort: 9889,
	}

	result, err := RenderTemplate("otel-config.yaml", OtelConfigTemplate, data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	checks := []string{
		"0.0.0.0:5317",   // gRPC endpoint
		"0.0.0.0:5318",   // HTTP endpoint
		"jaeger:5317",    // jaeger exporter
		"0.0.0.0:9889",   // prometheus exporter
		"loki:4100/otlp", // loki exporter
	}

	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("otel-config.yaml should contain %q", check)
		}
	}
}

func TestRenderTemplate_Prometheus(t *testing.T) {
	data := MonitorTemplateData{
		PrometheusMetricsPort: 9889,
		OtelCollectorInternal: "my-otel",
	}

	result, err := RenderTemplate("prometheus.yaml", PrometheusTemplate, data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	if !strings.Contains(result, "my-otel:9889") {
		t.Errorf("prometheus.yaml should contain 'my-otel:9889', got:\n%s", result)
	}
}

func TestRenderTemplate_GrafanaDatasources(t *testing.T) {
	data := MonitorTemplateData{
		PrometheusPort: 10090,
		JaegerPort:     17686,
		LokiPort:       4100,
	}

	result, err := RenderTemplate("grafana-datasources.yaml", GrafanaDatasourcesTemplate, data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	checks := []string{
		"prometheus:10090",
		"jaeger:17686",
		"loki:4100",
	}

	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("grafana-datasources.yaml should contain %q", check)
		}
	}
}

func TestRenderTemplate_InvalidTemplate(t *testing.T) {
	_, err := RenderTemplate("bad", "{{.Missing}", MonitorTemplateData{})
	if err == nil {
		t.Error("RenderTemplate should fail on invalid template syntax")
	}
}
