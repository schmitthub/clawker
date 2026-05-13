package monitor

import (
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
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
  otel_grpc_port: 4317
  prometheus_port: 9090
  prometheus_metrics_port: 8889
  opensearch_port: 9200
  opensearch_dashboards_port: 5601
  opensearch_heap_mb: 512
`)

	data := NewMonitorTemplateData(mon)

	if data.OtelCollectorPort != 4318 {
		t.Errorf("OtelCollectorPort = %d, want 4318", data.OtelCollectorPort)
	}
	if data.OtelGRPCPort != 4317 {
		t.Errorf("OtelGRPCPort = %d, want 4317 (from config)", data.OtelGRPCPort)
	}
	if data.OpenSearchPort != 9200 {
		t.Errorf("OpenSearchPort = %d, want 9200", data.OpenSearchPort)
	}
	if data.OpenSearchDashboardsPort != 5601 {
		t.Errorf("OpenSearchDashboardsPort = %d, want 5601", data.OpenSearchDashboardsPort)
	}
	if data.OpenSearchHeapMB != 512 {
		t.Errorf("OpenSearchHeapMB = %d, want 512", data.OpenSearchHeapMB)
	}

	// Service hostnames pulled from consts — not knobs.
	if data.OtelCollectorService != consts.MonitoringServiceOtelCollector {
		t.Errorf("OtelCollectorService = %q, want %q", data.OtelCollectorService, consts.MonitoringServiceOtelCollector)
	}
	if data.OpenSearchNodeService != consts.MonitoringServiceOpenSearchNode {
		t.Errorf("OpenSearchNodeService = %q, want %q", data.OpenSearchNodeService, consts.MonitoringServiceOpenSearchNode)
	}
	if data.OpenSearchDashboardsService != consts.MonitoringServiceOpenSearchDashboards {
		t.Errorf("OpenSearchDashboardsService = %q, want %q", data.OpenSearchDashboardsService, consts.MonitoringServiceOpenSearchDashboards)
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

func TestRenderTemplate_Compose(t *testing.T) {
	mon := testMonitoringConfig(t, `
monitoring:
  otel_collector_port: 5318
  otel_grpc_port: 5317
  prometheus_port: 10090
  prometheus_metrics_port: 9889
  opensearch_port: 19200
  opensearch_dashboards_port: 15601
  opensearch_heap_mb: 1024
`)

	data := NewMonitorTemplateData(mon)
	result, err := RenderTemplate("compose.yaml", ComposeTemplate, data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	// Verify custom ports + heap + new services appear, old services do not.
	mustContain := []struct {
		desc    string
		contain string
	}{
		{"OTEL HTTP port", "5318:5318"},
		{"OTEL gRPC port", "5317:5317"},
		{"Prometheus port", "10090:10090"},
		{"OpenSearch REST host bind", "127.0.0.1:19200:9200"},
		{"OpenSearch Dashboards host bind", "127.0.0.1:15601:5601"},
		{"OpenSearch heap", "-Xms1024m -Xmx1024m"},
		{"OTEL collector service key", consts.MonitoringServiceOtelCollector + ":"},
		{"Prometheus service key", consts.MonitoringServicePrometheus + ":"},
		{"OpenSearch node service key", consts.MonitoringServiceOpenSearchNode + ":"},
		{"OpenSearch dashboards service key", consts.MonitoringServiceOpenSearchDashboards + ":"},
		{"dashboards points at opensearch-node", `OPENSEARCH_HOSTS=["http://` + consts.MonitoringServiceOpenSearchNode + `:9200"]`},
	}
	for _, check := range mustContain {
		if !strings.Contains(result, check.contain) {
			t.Errorf("compose.yaml should contain %q (%s)", check.contain, check.desc)
		}
	}

	// Verify removed services are gone.
	mustNotContain := []string{"jaeger", "loki", "grafana", "promtail"}
	for _, banned := range mustNotContain {
		if strings.Contains(result, banned) {
			t.Errorf("compose.yaml should not contain %q after stack swap", banned)
		}
	}
}

func TestRenderTemplate_OtelConfig(t *testing.T) {
	mon := testMonitoringConfig(t, `
monitoring:
  otel_collector_port: 5318
  otel_grpc_port: 5317
  prometheus_metrics_port: 9889
  opensearch_port: 9200
`)

	data := NewMonitorTemplateData(mon)
	result, err := RenderTemplate("otel-config.yaml", OtelConfigTemplate, data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	mustContain := []string{
		"0.0.0.0:5317", // gRPC endpoint
		"0.0.0.0:5318", // HTTP endpoint
		"0.0.0.0:9889", // prometheus exporter
		"opensearch/logs:",
		"opensearch/traces:",
		"http://" + consts.MonitoringServiceOpenSearchNode + ":9200",
		"exporters: [opensearch/traces]",
		"exporters: [opensearch/logs]",
		"exporters: [prometheus]",
	}
	for _, check := range mustContain {
		if !strings.Contains(result, check) {
			t.Errorf("otel-config.yaml should contain %q", check)
		}
	}

	mustNotContain := []string{"jaeger", "loki", "otlphttp/loki", "otlp/jaeger"}
	for _, banned := range mustNotContain {
		if strings.Contains(result, banned) {
			t.Errorf("otel-config.yaml should not contain %q after stack swap", banned)
		}
	}
}

func TestRenderTemplate_Prometheus(t *testing.T) {
	mon := testMonitoringConfig(t, `
monitoring:
  prometheus_metrics_port: 9889
`)

	data := NewMonitorTemplateData(mon)
	result, err := RenderTemplate("prometheus.yaml", PrometheusTemplate, data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	target := consts.MonitoringServiceOtelCollector + ":9889"
	if !strings.Contains(result, target) {
		t.Errorf("prometheus.yaml should contain %q, got:\n%s", target, result)
	}
}

func TestNewMonitorTemplateData_OpenSearchImages(t *testing.T) {
	mon := testMonitoringConfig(t, `
monitoring:
  otel_collector_port: 4318
`)
	data := NewMonitorTemplateData(mon)
	if data.OpenSearchImage != OpenSearchImage {
		t.Errorf("OpenSearchImage = %q, want %q", data.OpenSearchImage, OpenSearchImage)
	}
	if data.OpenSearchDashboardsImage != OpenSearchDashboardsImage {
		t.Errorf("OpenSearchDashboardsImage = %q, want %q", data.OpenSearchDashboardsImage, OpenSearchDashboardsImage)
	}
	// Defensive: pins must include the @sha256 digest delimiter so a
	// future bump can't silently regress to a tag-only reference.
	for _, img := range []string{OpenSearchImage, OpenSearchDashboardsImage} {
		if !strings.Contains(img, "@sha256:") {
			t.Errorf("image pin must include @sha256: digest, got %q", img)
		}
	}
}

func TestRenderTemplate_InvalidTemplate(t *testing.T) {
	_, err := RenderTemplate("bad", "{{.Missing}", MonitorTemplateData{})
	if err == nil {
		t.Error("RenderTemplate should fail on invalid template syntax")
	}
}
