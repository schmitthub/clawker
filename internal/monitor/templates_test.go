package monitor

import (
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
)

// testMonitoringConfig creates a config.Config with monitoring settings and returns
// a pointer to its MonitoringConfig. The provided yaml string represents the
// full monitoring settings (overrides all defaults), not a partial merge.
func testMonitoringConfig(t *testing.T, yaml string) *config.MonitoringConfig {
	t.Helper()
	cfg := configmocks.NewFromString("", yaml)
	mon := cfg.MonitoringConfig()
	return &mon
}

func TestNewMonitorTemplateData(t *testing.T) {
	mon := testMonitoringConfig(t, `
monitoring:
  otel_collector_port: 4318
  otel_collector_host: "localhost"
  otel_collector_internal: "otel-collector"
  otel_grpc_port: 4317
  loki_port: 3100
  prometheus_port: 9090
  jaeger_port: 16686
  grafana_port: 3000
  prometheus_metrics_port: 8889
`)

	data := NewMonitorTemplateData(mon)

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
	mon := testMonitoringConfig(t, `
monitoring:
  otel_collector_port: 5318
  otel_grpc_port: 5317
`)

	data := NewMonitorTemplateData(mon)
	if data.OtelGRPCPort != 5317 {
		t.Errorf("OtelGRPCPort = %d, want 5317", data.OtelGRPCPort)
	}
}

func TestNewMonitorTemplateData_DefaultGRPCPort(t *testing.T) {
	// When OtelGRPCPort is not specified, NewFromString does not merge defaults,
	// so this test explicitly includes the default value.
	mon := testMonitoringConfig(t, `
monitoring:
  otel_collector_port: 4318
  otel_grpc_port: 4317
`)

	data := NewMonitorTemplateData(mon)
	if data.OtelGRPCPort != 4317 {
		t.Errorf("OtelGRPCPort = %d, want 4317", data.OtelGRPCPort)
	}
}

func TestRenderTemplate_Compose(t *testing.T) {
	mon := testMonitoringConfig(t, `
monitoring:
  otel_collector_port: 5318
  otel_grpc_port: 5317
  loki_port: 4100
  prometheus_port: 10090
  jaeger_port: 17686
  grafana_port: 4000
  prometheus_metrics_port: 9889
  otel_collector_internal: "my-collector"
`)

	data := NewMonitorTemplateData(mon)
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
	mon := testMonitoringConfig(t, `
monitoring:
  otel_collector_port: 5318
  otel_grpc_port: 5317
  loki_port: 4100
  prometheus_metrics_port: 9889
`)

	data := NewMonitorTemplateData(mon)
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
	mon := testMonitoringConfig(t, `
monitoring:
  prometheus_metrics_port: 9889
  otel_collector_internal: "my-otel"
`)

	data := NewMonitorTemplateData(mon)
	result, err := RenderTemplate("prometheus.yaml", PrometheusTemplate, data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	if !strings.Contains(result, "my-otel:9889") {
		t.Errorf("prometheus.yaml should contain 'my-otel:9889', got:\n%s", result)
	}
}

func TestRenderTemplate_GrafanaDatasources(t *testing.T) {
	mon := testMonitoringConfig(t, `
monitoring:
  prometheus_port: 10090
  jaeger_port: 17686
  loki_port: 4100
`)

	data := NewMonitorTemplateData(mon)
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

func TestRenderTemplate_PromtailConfig(t *testing.T) {
	mon := testMonitoringConfig(t, `
monitoring:
  loki_port: 4100
`)

	data := NewMonitorTemplateData(mon)
	result, err := RenderTemplate("promtail-config.yaml", PromtailConfigTemplate, data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	checks := []struct {
		desc    string
		contain string
	}{
		{"Loki push URL", "loki:4100/loki/api/v1/push"},
		{"Docker socket", "/var/run/docker.sock"},
		{"firewall label filter", "dev.clawker.purpose=firewall"},
		{"service_name relabel", "service_name"},
		{"envoy match selector", `service_name="envoy"`},
		{"coredns match selector", `service_name="coredns"`},
		{"json stage", "json:"},
		{"regex stage for coredns", "regex:"},
		{"domain label", "domain:"},
		{"proto label", "proto:"},
		{"rcode label", "rcode:"},
		{"agent label", "agent:"},
		{"client_ip label", "client_ip:"},
		{"duration label for coredns", "duration:"},
	}

	for _, check := range checks {
		if !strings.Contains(result, check.contain) {
			t.Errorf("promtail-config.yaml should contain %q (%s)", check.contain, check.desc)
		}
	}
}

func TestNewMonitorTemplateData_PromtailImage(t *testing.T) {
	mon := testMonitoringConfig(t, `
monitoring:
  otel_collector_port: 4318
`)
	data := NewMonitorTemplateData(mon)
	if data.PromtailImage != PromtailImage {
		t.Errorf("PromtailImage = %q, want %q", data.PromtailImage, PromtailImage)
	}
}

func TestRenderTemplate_InvalidTemplate(t *testing.T) {
	_, err := RenderTemplate("bad", "{{.Missing}", MonitorTemplateData{})
	if err == nil {
		t.Error("RenderTemplate should fail on invalid template syntax")
	}
}
