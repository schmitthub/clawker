package monitor

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	"github.com/schmitthub/clawker/internal/config"
)

// Embedded templates for monitoring stack configuration.
// Templates with .tmpl extension contain Go template variables
// and must be rendered via RenderTemplate before writing to disk.

//go:embed templates/compose.yaml.tmpl
var ComposeTemplate string

//go:embed templates/otel-config.yaml.tmpl
var OtelConfigTemplate string

//go:embed templates/grafana-datasources.yaml.tmpl
var GrafanaDatasourcesTemplate string

//go:embed templates/prometheus.yaml.tmpl
var PrometheusTemplate string

//go:embed templates/grafana-dashboards.yaml
var GrafanaDashboardsTemplate string

//go:embed templates/grafana-dashboard.json
var GrafanaDashboardTemplate string

// Template file names for writing to disk
const (
	ComposeFileName            = "compose.yaml"
	OtelConfigFileName         = "otel-config.yaml"
	GrafanaDatasourcesFileName = "grafana-datasources.yaml"
	PrometheusFileName         = "prometheus.yaml"
	GrafanaDashboardsFileName  = "grafana-dashboards.yaml"
	GrafanaDashboardFileName   = "grafana-dashboard.json"
)

// Monitoring stack container images — pinned to version + SHA256 digest.
const (
	OtelCollectorImage = "otel/opentelemetry-collector-contrib:0.148.0@sha256:8164eab2e6bca9c9b0837a8d2f118a6618489008a839db7f9d6510e66be3923c"
	JaegerImage        = "jaegertracing/all-in-one:1.76.0@sha256:ab6f1a1f0fb49ea08bcd19f6b84f6081d0d44b364b6de148e1798eb5816bacac"
	PrometheusImage    = "prom/prometheus:v3.10.0@sha256:4a61322ac1103a0e3aea2a61ef1718422a48fa046441f299d71e660a3bc71ae9"
	LokiImage          = "grafana/loki:3.7.0@sha256:c316b7c7589a5eeca843b6926c7446149d18300b79ac8538dc4ae063bc478da2"
	GrafanaImage       = "grafana/grafana:12.4.2@sha256:83749231c3835e390a3144e5e940203e42b9589761f20ef3169c716e734ad505"
)

// MonitorTemplateData provides values for rendering monitoring stack templates.
type MonitorTemplateData struct {
	OtelCollectorPort     int
	OtelGRPCPort          int // independent of HTTP port — from config.GetOtelGRPCPort()
	LokiPort              int
	PrometheusPort        int
	JaegerPort            int
	GrafanaPort           int
	PrometheusMetricsPort int
	OtelCollectorInternal string

	// Container images — version + SHA256 pinned.
	OtelCollectorImage string
	JaegerImage        string
	PrometheusImage    string
	LokiImage          string
	GrafanaImage       string
}

// NewMonitorTemplateData constructs template data from MonitoringConfig.
func NewMonitorTemplateData(cfg *config.MonitoringConfig) MonitorTemplateData {
	return MonitorTemplateData{
		OtelCollectorPort:     cfg.OtelCollectorPort,
		OtelGRPCPort:          cfg.OtelGRPCPort,
		LokiPort:              cfg.LokiPort,
		PrometheusPort:        cfg.PrometheusPort,
		JaegerPort:            cfg.JaegerPort,
		GrafanaPort:           cfg.GrafanaPort,
		PrometheusMetricsPort: cfg.PrometheusMetricsPort,
		OtelCollectorInternal: cfg.OtelCollectorInternal,
		OtelCollectorImage:    OtelCollectorImage,
		JaegerImage:           JaegerImage,
		PrometheusImage:       PrometheusImage,
		LokiImage:             LokiImage,
		GrafanaImage:          GrafanaImage,
	}
}

// RenderTemplate renders a Go text/template with the given data.
func RenderTemplate(name, tmplContent string, data MonitorTemplateData) (string, error) {
	t, err := template.New(name).Parse(tmplContent)
	if err != nil {
		return "", fmt.Errorf("failed to parse template %s: %w", name, err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render template %s: %w", name, err)
	}

	return buf.String(), nil
}
