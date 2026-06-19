package monitor

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// OpenSearchBootstrapFS embeds the bootstrap asset tree (script + index
// templates + ISM policies + saved objects). `monitor init` walks this
// FS and writes each file to <workdir>/opensearch-bootstrap/. Files
// ending in `.tmpl` (bootstrap.sh.tmpl, datasources/clawker_prometheus.json.tmpl)
// are rendered via RenderTemplate; the JSON/NDJSON payloads ship
// verbatim so users can audit them as static config.
//
//go:embed all:templates/opensearch-bootstrap
var OpenSearchBootstrapFS embed.FS

// Template file names for writing to disk
const (
	ComposeFileName            = "compose.yaml"
	OtelConfigFileName         = "otel-config.yaml"
	PrometheusFileName         = "prometheus.yaml"
	OpenSearchBootstrapDirName = "opensearch-bootstrap"
)

// Monitoring stack container images — pinned to version + SHA256 manifest-list digest.
// All digests are multi-arch (linux/amd64 + linux/arm64) — verify with
// `docker buildx imagetools inspect <pin>` before bumping.
const (
	OtelCollectorImage        = "otel/opentelemetry-collector-contrib:0.152.0@sha256:f41d7995565df3733b7568702073a9c490792f9c6ac60684fe6a4da21a313f8d"
	PrometheusImage           = "prom/prometheus:v3.11.3@sha256:e4254400b85610324913f0dc4acf92603d9984e7519414c5a12811aa6146acc3"
	OpenSearchImage           = "opensearchproject/opensearch:3.6.0@sha256:57bd3c879ad27123a9a6cd75e2adba504189d3131d00a669f3baf9210bc4538c"
	OpenSearchDashboardsImage = "opensearchproject/opensearch-dashboards:3.6.0@sha256:9fe2cbf1d82c3f66a0860ed140415692ce55de4711ed7877ab738e5da1a357c0"
	// CurlImage is the throwaway shell the clawker-opensearch-bootstrap
	// service uses to PUT index templates / ISM policies and POST saved
	// objects against OpenSearch + Dashboards once they're healthy.
	// curlimages/curl is Alpine-based, ships /bin/sh + curl, ~10 MB.
	CurlImage = "curlimages/curl:8.20.0@sha256:b3f1fb2a51d923260350d21b8654bbc607164a987e2f7c84a0ac199a67df812a"
)

// MonitorTemplateData provides values for rendering monitoring stack templates.
//
// Service hostnames are sourced from [consts.MonitoringService*] so the
// compose template, otel-config endpoints, and the CoreDNS internalHosts
// list in `internal/controlplane/firewall/coredns_config.go` cannot drift.
type MonitorTemplateData struct {
	// Ports — single value drives both sides of the host:container
	// publish mapping AND the container's own listener config (Prometheus
	// --web.listen-address, OpenSearch http.port, Dashboards SERVER_PORT,
	// otel-collector receiver endpoints in otel-config.yaml.tmpl). User
	// changes one knob in Settings.Monitoring and host + internal move
	// together.
	OtelCollectorPort        int
	OtelGRPCPort             int // independent of HTTP port
	OtelInfraPort            int // mTLS-gated host-loopback receiver for trusted infra push (clawkercp + firewall Envoy + CoreDNS)
	PrometheusPort           int
	PrometheusMetricsPort    int
	OpenSearchPort           int
	OpenSearchDashboardsPort int

	// OpenSearch JVM heap (MB) for both -Xms and -Xmx.
	OpenSearchHeapMB int

	// Service hostnames on the clawker network (compose service keys + cross-service
	// references). Mirror consts.MonitoringService*.
	OtelCollectorService        string
	PrometheusService           string
	OpenSearchNodeService       string
	OpenSearchDashboardsService string

	// Host-side paths for CLI-issued mTLS material that gates the
	// trusted otlp/infra receiver. Populated unconditionally by
	// `monitor init` from internal/consts after EnsureAuthMaterial
	// succeeds. The otel-config.yaml template renders the otlp/infra
	// receiver and trusted pipelines unconditionally — it has no
	// `{{ if }}` gate. Degradation is sender-side only: when an infra
	// sender (CP / Envoy / CoreDNS) lacks a valid client cert it stays
	// off this lane (see internal/monitor/CLAUDE.md "Trusted block
	// conditionality"). compose.yaml.tmpl gates the bind mounts + port
	// publish on OtelInfraPort, so a zero port suppresses the host-side
	// wiring even though the receiver block is still emitted into
	// otel-config.
	OtelServerCertHostPath string
	OtelServerKeyHostPath  string
	OtelCAHostPath         string

	// Container images — version + SHA256 pinned.
	OtelCollectorImage        string
	PrometheusImage           string
	OpenSearchImage           string
	OpenSearchDashboardsImage string
	CurlImage                 string

	// OpenSearchBootstrapDirName is the subdir of the rendered monitor
	// workdir that holds bootstrap.sh + index-templates/ + ism-policies/
	// + saved-objects/. Bind-mounted into the bootstrap container at
	// /opensearch-bootstrap. Lifted to a template field so the compose
	// volume mount and the on-disk layout stay in sync from one constant.
	OpenSearchBootstrapDirName string
}

// NewMonitorTemplateData constructs template data from Settings.
// Service hostnames are populated from [consts.MonitoringService*] —
// changing a hostname in consts propagates here without further edits.
// Settings.Monitoring drives ports/heap.
func NewMonitorTemplateData(s *config.Settings) MonitorTemplateData {
	mon := s.Monitoring
	return MonitorTemplateData{
		OtelCollectorPort:           mon.OtelCollectorPort,
		OtelGRPCPort:                mon.OtelGRPCPort,
		OtelInfraPort:               int(mon.OtelInfraPort),
		PrometheusPort:              mon.PrometheusPort,
		PrometheusMetricsPort:       mon.PrometheusMetricsPort,
		OpenSearchPort:              mon.OpenSearchPort,
		OpenSearchDashboardsPort:    mon.OpenSearchDashboardsPort,
		OpenSearchHeapMB:            mon.OpenSearchHeapMB,
		OtelCollectorService:        consts.MonitoringServiceOtelCollector,
		PrometheusService:           consts.MonitoringServicePrometheus,
		OpenSearchNodeService:       consts.MonitoringServiceOpenSearchNode,
		OpenSearchDashboardsService: consts.MonitoringServiceOpenSearchDashboards,
		OtelCollectorImage:          OtelCollectorImage,
		PrometheusImage:             PrometheusImage,
		OpenSearchImage:             OpenSearchImage,
		OpenSearchDashboardsImage:   OpenSearchDashboardsImage,
		CurlImage:                   CurlImage,
		OpenSearchBootstrapDirName:  OpenSearchBootstrapDirName,
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

// WriteOpenSearchBootstrap mirrors [OpenSearchBootstrapFS] into destDir,
// preserving directory structure. Files ending in `.tmpl` are rendered
// with [MonitorTemplateData] and written with the `.tmpl` suffix
// stripped; everything else (JSON, NDJSON) is copied verbatim.
//
// The destination is the workdir subdir bind-mounted into the
// clawker-opensearch-bootstrap container at /opensearch-bootstrap, so
// the on-disk layout mirrors what the script reads at runtime. Callers
// (monitor init) should pass `<monitorDir>/<OpenSearchBootstrapDirName>`.
//
// Idempotent: existing files are unconditionally overwritten. `monitor
// init` already enforces the `--force` gate at the top level, so when
// this runs the caller has decided to (re)render.
func WriteOpenSearchBootstrap(destDir string, data MonitorTemplateData) error {
	const root = "templates/" + OpenSearchBootstrapDirName

	// destDir holds only generated content — wipe it so files removed from
	// the embedded tree don't linger and get re-imported by bootstrap.sh,
	// which loops over every file in the rendered dir.
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("clear bootstrap dir %s: %w", destDir, err)
	}

	return fs.WalkDir(OpenSearchBootstrapFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relpath %s: %w", path, err)
		}
		if rel == "." {
			return os.MkdirAll(destDir, 0o755)
		}

		outPath := filepath.Join(destDir, rel)

		if d.IsDir() {
			return os.MkdirAll(outPath, 0o755)
		}

		raw, err := OpenSearchBootstrapFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embed %s: %w", path, err)
		}

		mode := os.FileMode(0o644)
		if strings.HasSuffix(outPath, ".tmpl") {
			rendered, err := RenderTemplate(filepath.Base(path), string(raw), data)
			if err != nil {
				return fmt.Errorf("render %s: %w", path, err)
			}
			raw = []byte(rendered)
			outPath = strings.TrimSuffix(outPath, ".tmpl")
			// bootstrap.sh runs as the container entrypoint — needs +x.
			mode = 0o755
		}

		if err := os.WriteFile(outPath, raw, mode); err != nil {
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		return nil
	})
}
