package monitor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
)

// testMonitoring creates a config.Config from the given settings YAML and
// returns its monitoring block — the only settings group the template data
// consumes. The yaml string represents the full settings (overrides all
// defaults), not a partial merge.
func testMonitoring(t *testing.T, yaml string) config.MonitoringConfig {
	t.Helper()
	cfg := configmocks.NewFromString("", yaml)
	return cfg.MonitoringConfig()
}

func TestNewMonitorTemplateData(t *testing.T) {
	mon := testMonitoring(t, `
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

	data, err := NewMonitorTemplateData(mon, nil)
	if err != nil {
		t.Fatalf("NewMonitorTemplateData: %v", err)
	}

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
		t.Errorf(
			"OpenSearchNodeService = %q, want %q",
			data.OpenSearchNodeService,
			consts.MonitoringServiceOpenSearchNode,
		)
	}
	if data.OpenSearchDashboardsService != consts.MonitoringServiceOpenSearchDashboards {
		t.Errorf(
			"OpenSearchDashboardsService = %q, want %q",
			data.OpenSearchDashboardsService,
			consts.MonitoringServiceOpenSearchDashboards,
		)
	}
}

func TestNewMonitorTemplateData_CustomGRPCPort(t *testing.T) {
	// gRPC port is independent — not derived from HTTP port
	mon := testMonitoring(t, `
monitoring:
  otel_collector_port: 5318
  otel_grpc_port: 5317
`)

	data, err := NewMonitorTemplateData(mon, nil)
	if err != nil {
		t.Fatalf("NewMonitorTemplateData: %v", err)
	}
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
	mon := testMonitoring(t, `
monitoring:
  otel_collector_port: 5318
  otel_grpc_port: 5317
  otel_infra_port: 5319
  otel_agent_ingest_port: 5320
  prometheus_port: 10090
  prometheus_metrics_port: 9889
  opensearch_port: 19200
  opensearch_dashboards_port: 15601
  opensearch_heap_mb: 1024
docker:
  socket: /var/run/docker.sock
`)

	data, err := NewMonitorTemplateData(mon, nil)
	if err != nil {
		t.Fatalf("NewMonitorTemplateData: %v", err)
	}
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
		{
			"OpenSearch Dashboards host:container",
			fmt.Sprintf("%d:%d", data.OpenSearchDashboardsPort, data.OpenSearchDashboardsPort),
		},
		{"OpenSearch Dashboards SERVER_PORT env", fmt.Sprintf("SERVER_PORT=%d", data.OpenSearchDashboardsPort)},
		// Heap derived from MonitoringConfig.OpenSearchHeapMB.
		{"OpenSearch heap", fmt.Sprintf("-Xms%dm -Xmx%dm", data.OpenSearchHeapMB, data.OpenSearchHeapMB)},
		// Service hostnames come from consts (firewall plane shares them).
		{"OTEL collector service key", data.OtelCollectorService + ":"},
		{"Prometheus service key", data.PrometheusService + ":"},
		{"OpenSearch node service key", data.OpenSearchNodeService + ":"},
		{"OpenSearch dashboards service key", data.OpenSearchDashboardsService + ":"},
		{
			"dashboards points at opensearch-node",
			fmt.Sprintf(`OPENSEARCH_HOSTS=["http://%s:%d"]`, data.OpenSearchNodeService, data.OpenSearchPort),
		},
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
		fmt.Sprintf(consts.Localhost+":%d:%d", data.OtelGRPCPort, data.OtelGRPCPort),
		fmt.Sprintf(consts.Localhost+":%d:%d", data.OtelCollectorPort, data.OtelCollectorPort),
		fmt.Sprintf(consts.Localhost+":%d:%d", data.PrometheusPort, data.PrometheusPort),
		fmt.Sprintf(consts.Localhost+":%d:%d", data.OpenSearchPort, data.OpenSearchPort),
		fmt.Sprintf(consts.Localhost+":%d:%d", data.OpenSearchDashboardsPort, data.OpenSearchDashboardsPort),
	} {
		if !strings.Contains(result, hostBind) {
			t.Errorf(
				"compose.yaml missing loopback bind %q — sensitive port must not be exposed on all interfaces",
				hostBind,
			)
		}
	}

	// Every published port is a loopback publish: a `- "<port>:<port>"`
	// entry without the 127.0.0.1 prefix exposes the service on every
	// host interface.
	inPorts := false
	for line := range strings.SplitSeq(result, "\n") {
		entry := strings.TrimSpace(line)
		switch {
		case entry == "ports:":
			inPorts = true
		case entry == "" || strings.HasPrefix(entry, "#"):
		case !strings.HasPrefix(entry, "-"):
			inPorts = false
		case inPorts && !strings.HasPrefix(entry, `- "`+consts.Localhost+":"):
			t.Errorf("compose.yaml publishes %s without a loopback bind", entry)
		}
	}

	// The mTLS receivers have no host sender: the control plane, Envoy,
	// and CoreDNS reach them over the telemetry network, so they are not
	// published on the host at all.
	for _, port := range []int{data.OtelInfraPort, data.OtelAgentIngestPort} {
		if strings.Contains(result, fmt.Sprintf(":%d:%d", port, port)) {
			t.Errorf("compose.yaml publishes mTLS receiver port %d on the host", port)
		}
	}

	// No service joins the agent network. Only the collector joins the
	// telemetry network, which the control plane creates as an internal
	// network, so the compose file must reference it as external.
	if strings.Contains(result, consts.Network) {
		t.Errorf("compose.yaml must not contain %q: monitoring services stay off the agent network", consts.Network)
	}
	var doc struct {
		Services map[string]struct {
			Networks []string `yaml:"networks"`
		} `yaml:"services"`
		Networks map[string]struct {
			External bool   `yaml:"external"`
			Name     string `yaml:"name"`
		} `yaml:"networks"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(result), &doc))
	for name, svc := range doc.Services {
		if name == data.OtelCollectorService {
			assert.ElementsMatch(t, []string{"default", "telemetry"}, svc.Networks, "collector networks")
			continue
		}
		assert.Empty(t, svc.Networks, "service %s must stay on the default compose network", name)
	}
	require.Contains(t, doc.Networks, "telemetry")
	assert.True(t, doc.Networks["telemetry"].External, "telemetry network is created outside compose")
	assert.Equal(t, consts.TelemetryNetwork, doc.Networks["telemetry"].Name)
}

func TestRenderTemplate_OtelConfig(t *testing.T) {
	mon := testMonitoring(t, `
monitoring:
  otel_collector_port: 5318
  otel_grpc_port: 5317
  otel_infra_port: 5319
  otel_agent_ingest_port: 5320
  prometheus_metrics_port: 9889
  opensearch_port: 9200
`)

	data, err := NewMonitorTemplateData(mon, nil)
	if err != nil {
		t.Fatalf("NewMonitorTemplateData: %v", err)
	}
	result, err := RenderTemplate("otel-config.yaml", OtelConfigTemplate, data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	mustContain := []string{
		"0.0.0.0:5317", // gRPC endpoint
		"0.0.0.0:5318", // HTTP endpoint
		"0.0.0.0:9889", // prometheus exporter
		"opensearch/logs_cp:",
		"opensearch/logs_clawker_cli:",
		"opensearch/logs_envoy:",
		"opensearch/logs_coredns:",
		"opensearch/traces:",
		"http://" + consts.MonitoringServiceOpenSearchNode + ":9200",
		"logs_index: clawkercp",
		"logs_index: clawker-cli",
		"logs_index: clawker-envoy",
		"logs_index: clawker-coredns",
		"exporters: [opensearch/traces, spanmetrics, debug]",
		"exporters: [opensearch/logs_cp, debug]",
		"exporters: [opensearch/logs_clawker_cli, debug]",
		"exporters: [opensearch/logs_envoy, debug]",
		"exporters: [opensearch/logs_coredns, debug]",
		"exporters: [routing/untrusted]",
		"exporters: [routing/trusted]",
		"exporters: [prometheus, debug]",
		"routing/untrusted:",
		"routing/trusted:",
		`condition: attributes["service.name"] == "clawker-cli"`,
		`condition: attributes["service.name"] == "envoy"`,
		`condition: attributes["service.name"] == "coredns"`,
		`condition: attributes["service.name"] == "clawkercp"`,
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
		// CP gateway lane: the receiver follows the configured port and
		// the two processors exist. Pipeline wiring is locked by the
		// otel-config golden files.
		"0.0.0.0:5320",
		"resource/agent_gateway:",
		"transform/agent_gateway_metrics:",
	}
	for _, check := range mustContain {
		if !strings.Contains(result, check) {
			t.Errorf("otel-config.yaml should contain %q", check)
		}
	}

	// Only the host CLI lane routes service.name=clawker-cli. The gateway
	// connector carries no such route, so an agent that declares that
	// name cannot land records in the clawker-cli index.
	if got := strings.Count(result, `== "clawker-cli"`); got != 1 {
		t.Errorf("otel-config.yaml routes clawker-cli %d times, want exactly 1 (routing/untrusted only)", got)
	}

	// Zero active units → zero unit lanes anywhere in the collector
	// config, and no routing/agents connector (the collector rejects an
	// empty routing table); the gateway log pipeline exports to debug.
	// Unmatched service names (any inactive unit's traffic) fall to the
	// debug-only unrouted pipeline. The with-lanes shape is locked by the
	// otel-config goldens and assertCollectorGraph.
	for _, absent := range []string{"claude-code", "logs/unit_", "routing/agents:", "[routing/agents]"} {
		if strings.Contains(result, absent) {
			t.Errorf("otel-config.yaml with no active units should not contain %q", absent)
		}
	}
}

func TestRenderTemplate_Prometheus(t *testing.T) {
	mon := testMonitoring(t, `
monitoring:
  prometheus_metrics_port: 9889
`)

	data, err := NewMonitorTemplateData(mon, nil)
	if err != nil {
		t.Fatalf("NewMonitorTemplateData: %v", err)
	}
	result, err := RenderTemplate("prometheus.yaml", PrometheusTemplate, data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	target := consts.MonitoringServiceOtelCollector + ":9889"
	if !strings.Contains(result, target) {
		t.Errorf("prometheus.yaml should contain %q, got:\n%s", target, result)
	}
}

// OpenSearch Dashboards 3.9 builds an index pattern from the `fields`
// attribute of its saved object and no longer fetches the field list from
// the index when that attribute is absent (3.6 did). The baked index
// patterns ship without `fields`, so bootstrap must write the live field
// list into every index pattern after the import, or every visualization
// that names a field fails with "Could not locate that index-pattern-field".
func TestRenderTemplate_BootstrapWritesIndexPatternFields(t *testing.T) {
	data, err := NewMonitorTemplateData(testMonitoring(t, ""), nil)
	if err != nil {
		t.Fatalf("NewMonitorTemplateData: %v", err)
	}
	raw, err := OpenSearchBootstrapFS.ReadFile("templates/opensearch-bootstrap/bootstrap.sh.tmpl")
	if err != nil {
		t.Fatalf("read bootstrap template: %v", err)
	}
	result, err := RenderTemplate("bootstrap.sh", string(raw), data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	importStep := strings.Index(result, "/api/saved_objects/_import?overwrite=true")
	fieldsFetch := strings.Index(result, "/api/index_patterns/_fields_for_wildcard?pattern=")
	// The step reads the index pattern before it writes it; the PUT is the
	// last reference to the saved-object path.
	fieldsWrite := strings.LastIndex(result, "/api/saved_objects/index-pattern/")
	if importStep < 0 || fieldsFetch < 0 || fieldsWrite < 0 {
		t.Fatalf(
			"bootstrap.sh must import saved objects, fetch index-pattern fields, and write them: import=%d fetch=%d write=%d",
			importStep,
			fieldsFetch,
			fieldsWrite,
		)
	}
	if fieldsFetch < importStep || fieldsWrite < fieldsFetch {
		t.Errorf("index-pattern field refresh must run after the saved-object import: import=%d fetch=%d write=%d",
			importStep, fieldsFetch, fieldsWrite)
	}
	for _, s := range []string{
		"/w/${WS_ID}/api/index_patterns/_fields_for_wildcard",
		"/w/${WS_ID}/api/saved_objects/index-pattern/",
	} {
		if !strings.Contains(result, s) {
			t.Errorf("index-pattern field refresh must be workspace-scoped: %q not found", s)
		}
	}
}

func TestNewMonitorTemplateData_OpenSearchImages(t *testing.T) {
	mon := testMonitoring(t, `
monitoring:
  otel_collector_port: 4318
`)
	data, err := NewMonitorTemplateData(mon, nil)
	if err != nil {
		t.Fatalf("NewMonitorTemplateData: %v", err)
	}
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

	data, err := NewMonitorTemplateData(testMonitoring(t, `
monitoring:
  otel_collector_port: 4318
  otel_grpc_port: 4317
  prometheus_port: 9090
  prometheus_metrics_port: 8889
  opensearch_port: 9200
  opensearch_dashboards_port: 5601
  opensearch_heap_mb: 512
`), nil)
	if err != nil {
		t.Fatalf("NewMonitorTemplateData: %v", err)
	}
	if writeErr := WriteOpenSearchBootstrap(destDir, data, nil); writeErr != nil {
		t.Fatalf("WriteOpenSearchBootstrap: %v", writeErr)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file survived re-render: %s", stale)
	}
	// The mirror itself must still be complete.
	for _, want := range []string{
		"bootstrap.sh",
		filepath.Join("saved-objects", "clawker.ndjson"),
		filepath.Join("index-templates", "clawker-envoy.json"),
	} {
		if _, err := os.Stat(filepath.Join(destDir, want)); err != nil {
			t.Errorf("expected %s after render: %v", want, err)
		}
	}
}
