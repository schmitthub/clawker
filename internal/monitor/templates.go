package monitor

import (
	_ "embed"
)

// Embedded templates for monitoring stack configuration

//go:embed templates/compose.yaml
var ComposeTemplate string

//go:embed templates/otel-config.yaml
var OtelConfigTemplate string

//go:embed templates/grafana-datasources.yaml
var GrafanaDatasourcesTemplate string

//go:embed templates/prometheus.yaml
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
