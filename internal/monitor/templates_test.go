package monitor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
)

// testSettings creates a config.Config from the given settings YAML and
// returns a pointer to its Settings. The yaml string represents the
// full settings (overrides all defaults), not a partial merge.
func testSettings(t *testing.T, yaml string) *config.Settings {
	t.Helper()
	cfg := configmocks.NewFromString("", yaml)
	return cfg.SettingsStore().Read()
}

func TestNewMonitorTemplateData(t *testing.T) {
	mon := testSettings(t, `
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
	mon := testSettings(t, `
monitoring:
  otel_collector_port: 5318
  otel_grpc_port: 5317
`)

	data := NewMonitorTemplateData(mon)
	if data.OtelGRPCPort != 5317 {
		t.Errorf("OtelGRPCPort = %d, want 5317", data.OtelGRPCPort)
	}
}

// TestRenderTemplate_Compose verifies that every port/heap/service-name
// field on MonitorTemplateData reaches the rendered compose.yaml in a
// place consumers depend on. Assertions derive their expected strings
// from the data struct itself via fmt.Sprintf — so bumping a default
// in [config.MonitoringConfig] or renaming a service in
// [consts.MonitoringService*] does NOT require touching this test.
//
// The sentinel ports below are arbitrary non-defaults chosen so any
// default-port leak in the template (e.g. hardcoded `4318` instead of
// `{{.OtelCollectorPort}}`) shows up as a missing-port failure rather
// than a false pass.
func TestRenderTemplate_Compose(t *testing.T) {
	mon := testSettings(t, `
monitoring:
  otel_collector_port: 5318
  otel_grpc_port: 5317
  prometheus_port: 10090
  prometheus_metrics_port: 9889
  opensearch_port: 19200
  opensearch_dashboards_port: 15601
  opensearch_heap_mb: 1024
docker:
  socket: /var/run/docker.sock
`)

	data := NewMonitorTemplateData(mon)
	result, err := RenderTemplate("compose.yaml", ComposeTemplate, data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	// Value-routing: every field on MonitorTemplateData consumed by the
	// template must surface at least once in the rendered output. The
	// expected substring is built from `data` so renames/port changes
	// don't break this test.
	valueRouting := []struct {
		desc    string
		contain string
	}{
		// Single-port mappings ({{.X}}:{{.X}}) — host == container for
		// every service. User overrides one setting and both sides move
		// together; the container's listener config (Prometheus
		// --web.listen-address, OpenSearch http.port, Dashboards
		// SERVER_PORT) is wired from the same template variable so the
		// image actually listens on the configured port.
		{"OTEL HTTP port", fmt.Sprintf("%d:%d", data.OtelCollectorPort, data.OtelCollectorPort)},
		{"OTEL gRPC port", fmt.Sprintf("%d:%d", data.OtelGRPCPort, data.OtelGRPCPort)},
		{"Prometheus host:container", fmt.Sprintf("%d:%d", data.PrometheusPort, data.PrometheusPort)},
		{"Prometheus listen-address flag", fmt.Sprintf("--web.listen-address=:%d", data.PrometheusPort)},
		{"OpenSearch REST host:container", fmt.Sprintf("%d:%d", data.OpenSearchPort, data.OpenSearchPort)},
		{"OpenSearch http.port env", fmt.Sprintf("http.port=%d", data.OpenSearchPort)},
		{"OpenSearch Dashboards host:container", fmt.Sprintf("%d:%d", data.OpenSearchDashboardsPort, data.OpenSearchDashboardsPort)},
		{"OpenSearch Dashboards SERVER_PORT env", fmt.Sprintf("SERVER_PORT=%d", data.OpenSearchDashboardsPort)},
		// Heap derived from MonitoringConfig.OpenSearchHeapMB.
		{"OpenSearch heap", fmt.Sprintf("-Xms%dm -Xmx%dm", data.OpenSearchHeapMB, data.OpenSearchHeapMB)},
		// Service hostnames come from consts (firewall plane shares them).
		{"OTEL collector service key", data.OtelCollectorService + ":"},
		{"Prometheus service key", data.PrometheusService + ":"},
		{"OpenSearch node service key", data.OpenSearchNodeService + ":"},
		{"OpenSearch dashboards service key", data.OpenSearchDashboardsService + ":"},
		{"dashboards points at opensearch-node", fmt.Sprintf(`OPENSEARCH_HOSTS=["http://%s:%d"]`, data.OpenSearchNodeService, data.OpenSearchPort)},
	}
	for _, check := range valueRouting {
		if !strings.Contains(result, check.contain) {
			t.Errorf("compose.yaml missing routed value for %s: %q not found", check.desc, check.contain)
		}
	}

	// Loopback-bind policy: every host-published monitoring port
	// (Prometheus, OpenSearch REST, OpenSearch Dashboards) must be
	// 127.0.0.1-bound. Security plugins are disabled for local dev
	// (DISABLE_SECURITY_PLUGIN / DISABLE_SECURITY_DASHBOARDS_PLUGIN),
	// so non-loopback exposure leaks unauthenticated logs/traces/
	// metrics to whatever network the host is on. If you change this
	// policy, update both the template and this assertion together.
	for _, hostBind := range []string{
		fmt.Sprintf(consts.LoopbackIPv4+":%d:%d", data.PrometheusPort, data.PrometheusPort),
		fmt.Sprintf(consts.LoopbackIPv4+":%d:%d", data.OpenSearchPort, data.OpenSearchPort),
		fmt.Sprintf(consts.LoopbackIPv4+":%d:%d", data.OpenSearchDashboardsPort, data.OpenSearchDashboardsPort),
	} {
		if !strings.Contains(result, hostBind) {
			t.Errorf("compose.yaml missing loopback bind %q — sensitive port must not be exposed on all interfaces", hostBind)
		}
	}

}

func TestRenderTemplate_OtelConfig(t *testing.T) {
	mon := testSettings(t, `
monitoring:
  otel_collector_port: 5318
  otel_grpc_port: 5317
  otel_infra_port: 5319
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
		"opensearch/logs_cp:",
		"opensearch/logs_claude_code:",
		"opensearch/logs_envoy:",
		"opensearch/logs_coredns:",
		"opensearch/traces:",
		"http://" + consts.MonitoringServiceOpenSearchNode + ":9200",
		"logs_index: clawker-cp",
		"logs_index: claude-code",
		"logs_index: clawker-envoy",
		"logs_index: clawker-coredns",
		"exporters: [opensearch/traces, spanmetrics, debug]",
		"exporters: [opensearch/logs_cp, debug]",
		"exporters: [opensearch/logs_claude_code, debug]",
		"exporters: [opensearch/logs_envoy, debug]",
		"exporters: [opensearch/logs_coredns, debug]",
		"exporters: [routing/untrusted]",
		"exporters: [routing/trusted]",
		"exporters: [prometheus, debug]",
		"routing/untrusted:",
		"routing/trusted:",
		`condition: attributes["service.name"] == "claude-code"`,
		`condition: attributes["service.name"] == "envoy"`,
		`condition: attributes["service.name"] == "coredns"`,
		`condition: attributes["service.name"] == "clawker-cp"`,
		"resource/cp:",
		"resource/coredns:",
		"resource/envoy:",
		"prometheus/self:",
		"spanmetrics:",
		// Metrics pipeline is split into untrusted (otlp only — anything
		// pushed on the unauth'd lane) and trusted (locally-sourced
		// scrapers). Both export to the shared prometheus exporter.
		"receivers: [prometheus/self, spanmetrics]",
		// mTLS-gated receiver feeds the trusted-routing pipeline.
		"receivers: [otlp/infra]",
		// Routing connectors are the receivers for the per-source log
		// pipelines.
		"receivers: [routing/trusted]",
		"receivers: [routing/untrusted]",
		// gRPC variant of the trusted receiver is what Envoy ALS dials.
		"0.0.0.0:5319",
	}
	for _, check := range mustContain {
		if !strings.Contains(result, check) {
			t.Errorf("otel-config.yaml should contain %q", check)
		}
	}

}

func TestRenderTemplate_Prometheus(t *testing.T) {
	mon := testSettings(t, `
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
	mon := testSettings(t, `
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

func TestWriteOpenSearchBootstrap_PrunesStaleFiles(t *testing.T) {
	destDir := filepath.Join(t.TempDir(), OpenSearchBootstrapDirName)

	// Simulate a rendered dir from an older template version: a saved-object
	// file that no longer exists in the embedded tree. bootstrap.sh loops over
	// every *.json in the dir, so a stale file would re-import on every
	// monitor up regardless of volume wipes.
	staleDir := filepath.Join(destDir, "saved-objects", "explore")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(staleDir, "clawker-removed-from-templates.json")
	if err := os.WriteFile(stale, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	data := NewMonitorTemplateData(testSettings(t, `
monitoring:
  otel_collector_port: 4318
  otel_grpc_port: 4317
  prometheus_port: 9090
  prometheus_metrics_port: 8889
  opensearch_port: 9200
  opensearch_dashboards_port: 5601
  opensearch_heap_mb: 512
`))
	if err := WriteOpenSearchBootstrap(destDir, data); err != nil {
		t.Fatalf("WriteOpenSearchBootstrap: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file survived re-render: %s", stale)
	}
	// The mirror itself must still be complete.
	for _, want := range []string{
		"bootstrap.sh",
		filepath.Join("saved-objects", "clawker.ndjson"),
		filepath.Join("saved-objects", "explore", "clawker-claude-code-model-usage.json"),
	} {
		if _, err := os.Stat(filepath.Join(destDir, want)); err != nil {
			t.Errorf("expected %s after render: %v", want, err)
		}
	}
}
