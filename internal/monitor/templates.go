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

//go:embed templates/grafana-cp-dashboard.json
var GrafanaCPDashboardTemplate string

// Template file names for writing to disk
const (
	ComposeFileName            = "compose.yaml"
	OtelConfigFileName         = "otel-config.yaml"
	GrafanaDatasourcesFileName = "grafana-datasources.yaml"
	PrometheusFileName         = "prometheus.yaml"
	GrafanaDashboardsFileName  = "grafana-dashboards.yaml"
	GrafanaDashboardFileName     = "grafana-dashboard.json"
	GrafanaCPDashboardFileName   = "grafana-cp-dashboard.json"
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
}

// NewMonitorTemplateData constructs template data from MonitoringConfig.
func NewMonitorTemplateData(cfg *config.MonitoringConfig) MonitorTemplateData {
	return MonitorTemplateData{
		OtelCollectorPort:     cfg.OtelCollectorPort,
		OtelGRPCPort:          cfg.GetOtelGRPCPort(),
		LokiPort:              cfg.LokiPort,
		PrometheusPort:        cfg.PrometheusPort,
		JaegerPort:            cfg.JaegerPort,
		GrafanaPort:           cfg.GrafanaPort,
		PrometheusMetricsPort: cfg.PrometheusMetricsPort,
		OtelCollectorInternal: cfg.OtelCollectorInternal,
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
