package monitor

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

// Embedded templates for monitoring stack configuration.
// Templates with .tmpl extension contain Go template variables
// and must be rendered via RenderTemplate before writing to disk.

//go:embed templates/compose.yaml.tmpl
var ComposeTemplate string

//go:embed templates/otel-config.yaml.tmpl
var OtelConfigTemplate string

//go:embed templates/prometheus.yaml.tmpl
var PrometheusTemplate string

// Template file names for writing to disk
const (
	ComposeFileName    = "compose.yaml"
	OtelConfigFileName = "otel-config.yaml"
	PrometheusFileName = "prometheus.yaml"
)

// Monitoring stack container images — pinned to version + SHA256 manifest-list digest.
// All digests are multi-arch (linux/amd64 + linux/arm64) — verify with
// `docker buildx imagetools inspect <pin>` before bumping.
const (
	OtelCollectorImage        = "otel/opentelemetry-collector-contrib:0.148.0@sha256:8164eab2e6bca9c9b0837a8d2f118a6618489008a839db7f9d6510e66be3923c"
	PrometheusImage           = "prom/prometheus:v3.10.0@sha256:4a61322ac1103a0e3aea2a61ef1718422a48fa046441f299d71e660a3bc71ae9"
	OpenSearchImage           = "opensearchproject/opensearch:3.6.0@sha256:57bd3c879ad27123a9a6cd75e2adba504189d3131d00a669f3baf9210bc4538c"
	OpenSearchDashboardsImage = "opensearchproject/opensearch-dashboards:3.6.0@sha256:9fe2cbf1d82c3f66a0860ed140415692ce55de4711ed7877ab738e5da1a357c0"
)

// MonitorTemplateData provides values for rendering monitoring stack templates.
//
// Service hostnames are sourced from [consts.MonitoringService*] so the
// compose template, otel-config endpoints, and the CoreDNS internalHosts
// list in `internal/controlplane/firewall/coredns_config.go` cannot drift.
type MonitorTemplateData struct {
	// Ports
	OtelCollectorPort        int
	OtelGRPCPort             int // independent of HTTP port
	OtelCPPort               int // mTLS-gated host-loopback receiver for clawker-cp push
	PrometheusPort           int
	PrometheusMetricsPort    int
	OpenSearchPort           int
	OpenSearchDashboardsPort int

	// OpenSearch JVM heap (MB) for both -Xms and -Xmx.
	OpenSearchHeapMB int

	// Service hostnames on clawker-net (compose service keys + cross-service
	// references). Mirror consts.MonitoringService*.
	OtelCollectorService        string
	PrometheusService           string
	OpenSearchNodeService       string
	OpenSearchDashboardsService string

	// Host-side paths for CLI-issued mTLS material that gates the
	// CP-only OTLP receiver. Populated by the monitor init command from
	// internal/consts. Empty disables the gated receiver — the
	// otel-config template branches on OtelCPPort to decide whether to
	// emit the second receiver block.
	OtelServerCertHostPath string
	OtelServerKeyHostPath  string
	OtelCAHostPath         string

	// Host paths consumed by the otel-collector's hostmetrics +
	// docker_stats receivers. HostFilesystem is hardcoded to "/" — Linux
	// host root or Docker Desktop VM root; mounted RO at /hostfs.
	// DockerSocketPath comes from Settings.Docker.Socket (defaults to
	// /var/run/docker.sock); mounted RO at /var/run/docker.sock.
	HostFilesystem   string
	DockerSocketPath string

	// Container images — version + SHA256 pinned.
	OtelCollectorImage        string
	PrometheusImage           string
	OpenSearchImage           string
	OpenSearchDashboardsImage string
}

// NewMonitorTemplateData constructs template data from Settings.
// Service hostnames are populated from [consts.MonitoringService*] —
// changing a hostname in consts propagates here without further edits.
// Settings.Monitoring drives ports/heap; Settings.Docker.Socket feeds
// the otel-collector docker_stats receiver mount.
func NewMonitorTemplateData(s *config.Settings) MonitorTemplateData {
	mon := s.Monitoring
	return MonitorTemplateData{
		OtelCollectorPort:           mon.OtelCollectorPort,
		OtelGRPCPort:                mon.OtelGRPCPort,
		OtelCPPort:                  mon.OtelCPPort,
		PrometheusPort:              mon.PrometheusPort,
		PrometheusMetricsPort:       mon.PrometheusMetricsPort,
		OpenSearchPort:              mon.OpenSearchPort,
		OpenSearchDashboardsPort:    mon.OpenSearchDashboardsPort,
		OpenSearchHeapMB:            mon.OpenSearchHeapMB,
		OtelCollectorService:        consts.MonitoringServiceOtelCollector,
		PrometheusService:           consts.MonitoringServicePrometheus,
		OpenSearchNodeService:       consts.MonitoringServiceOpenSearchNode,
		OpenSearchDashboardsService: consts.MonitoringServiceOpenSearchDashboards,
		HostFilesystem:              "/",
		DockerSocketPath:            s.Docker.Socket,
		OtelCollectorImage:          OtelCollectorImage,
		PrometheusImage:             PrometheusImage,
		OpenSearchImage:             OpenSearchImage,
		OpenSearchDashboardsImage:   OpenSearchDashboardsImage,
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
