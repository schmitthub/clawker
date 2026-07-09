package monitor

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/schmitthub/clawker/internal/bundler"
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

// Rendered-file modes for the generated (non-secret) monitoring config
// tree; bootstrap.sh runs as the container entrypoint and needs +x.
const (
	bootstrapFileMode   = os.FileMode(0o644)
	bootstrapScriptMode = os.FileMode(0o755)
	bootstrapDirMode    = os.FileMode(0o755)
)

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

	// Units is the collector routing data for every ACTIVE monitoring
	// unit: otel-config.yaml.tmpl ranges over it to emit per-lane
	// exporters, routing-table entries, pipelines, and metric rename
	// statements. Inactive units contribute nothing — their records fall
	// to the untrusted_unrouted debug pipeline.
	Units []UnitRouting

	// ISMIndexPatternsJSON is the shared retention policy's
	// ism_template.index_patterns value: the infra indices plus every
	// active default-retention unit lane index, JSON-encoded Go-side so
	// the template never hand-quotes.
	ISMIndexPatternsJSON string
}

// NewMonitorTemplateData constructs template data from Settings and the
// active monitoring unit set. Service hostnames are populated from
// [consts.MonitoringService*] — changing a hostname in consts propagates
// here without further edits. Settings.Monitoring drives ports/heap;
// units drive collector routing and the shared retention policy's index
// patterns.
func NewMonitorTemplateData(s *config.Settings, units []ResolvedUnit) (MonitorTemplateData, error) {
	routings, err := BuildUnitRoutings(units)
	if err != nil {
		return MonitorTemplateData{}, err
	}
	ismPatterns, err := ismIndexPatternsJSON(units)
	if err != nil {
		return MonitorTemplateData{}, err
	}
	mon := s.Monitoring
	return MonitorTemplateData{
		Units:                       routings,
		ISMIndexPatternsJSON:        ismPatterns,
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
	}, nil
}

// ismIndexPatternsJSON builds the shared retention policy's index
// pattern list: the reserved infra indices plus every active unit lane
// that participates in default retention. Custom-retention lanes ship
// their own unit policies instead.
func ismIndexPatternsJSON(units []ResolvedUnit) (string, error) {
	patterns := consts.ReservedMonitoringIndices()
	for _, u := range units {
		for _, lane := range u.Manifest().Logs {
			if lane.Retention == "" || lane.Retention == config.MonitoringRetentionDefault {
				patterns = append(patterns, lane.Index)
			}
		}
	}
	raw, err := json.Marshal(patterns)
	if err != nil {
		return "", fmt.Errorf("monitor: encode ISM index patterns: %w", err)
	}
	return string(raw), nil
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
// preserving directory structure, then overlays every ACTIVE monitoring
// unit's artifacts into the same category dirs. Files ending in `.tmpl`
// are rendered with [MonitorTemplateData] and written with the `.tmpl`
// suffix stripped; everything else (JSON, NDJSON) is copied verbatim.
// Unit files are always verbatim — units carry no templates.
//
// A unit artifact whose path collides with a core file or another unit's
// is a hard error naming both provenances: PUT names are
// basename-derived and index templates reference pipelines by name, so a
// silent overwrite would rewrite cluster behavior. Saved-object IDs are
// collision-checked across every ndjson line and explore panel filename
// for the same reason (`_import?overwrite=true` is last-write-wins).
//
// The destination is the workdir subdir bind-mounted into the
// clawker-opensearch-bootstrap container at /opensearch-bootstrap, so
// the on-disk layout mirrors what the script reads at runtime. Callers
// (monitor init) should pass `<monitorDir>/<OpenSearchBootstrapDirName>`.
//
// Idempotent: destDir holds only generated content and is wiped first,
// so files removed from the embedded tree — or belonging to a
// deactivated unit — don't linger and get re-imported by bootstrap.sh's
// directory loops. A [UnitsMarkerFile] records the active set for
// `monitor up`'s drift warning.
func WriteOpenSearchBootstrap(destDir string, data MonitorTemplateData, units []ResolvedUnit) error {
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("clear bootstrap dir %s: %w", destDir, err)
	}

	written := map[string]string{} // rel path → provenance
	if err := writeBootstrapCore(destDir, data, written); err != nil {
		return err
	}
	for _, u := range units {
		if err := overlayUnitArtifacts(destDir, u, written); err != nil {
			return err
		}
	}
	if err := validateSavedObjectIDs(destDir, written); err != nil {
		return err
	}
	return writeUnitsMarker(destDir, units)
}

// writeBootstrapCore mirrors the embedded core tree into destDir,
// recording every written rel path.
func writeBootstrapCore(destDir string, data MonitorTemplateData, written map[string]string) error {
	const root = "templates/" + OpenSearchBootstrapDirName

	err := fs.WalkDir(OpenSearchBootstrapFS, root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("relpath %s: %w", path, relErr)
		}
		if rel == "." {
			return os.MkdirAll(destDir, bootstrapDirMode)
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(destDir, rel), bootstrapDirMode)
		}
		return writeCoreFile(destDir, path, rel, data, written)
	})
	if err != nil {
		return fmt.Errorf("mirror bootstrap core: %w", err)
	}
	return nil
}

// writeCoreFile writes one embedded core file (rendering .tmpl sources)
// and records its rel path.
func writeCoreFile(destDir, srcPath, rel string, data MonitorTemplateData, written map[string]string) error {
	raw, err := OpenSearchBootstrapFS.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read embed %s: %w", srcPath, err)
	}

	outPath := filepath.Join(destDir, rel)
	mode := bootstrapFileMode
	if strings.HasSuffix(outPath, ".tmpl") {
		rendered, renderErr := RenderTemplate(filepath.Base(srcPath), string(raw), data)
		if renderErr != nil {
			return fmt.Errorf("render %s: %w", srcPath, renderErr)
		}
		raw = []byte(rendered)
		outPath = strings.TrimSuffix(outPath, ".tmpl")
		rel = strings.TrimSuffix(rel, ".tmpl")
		// bootstrap.sh runs as the container entrypoint — needs +x.
		mode = bootstrapScriptMode
	}

	if writeErr := os.WriteFile(outPath, raw, mode); writeErr != nil {
		return fmt.Errorf("write %s: %w", outPath, writeErr)
	}
	written[filepath.ToSlash(rel)] = "clawker core"
	return nil
}

// overlayUnitArtifacts copies one active unit's artifact files into the
// rendered tree, erroring on any path collision.
func overlayUnitArtifacts(destDir string, u ResolvedUnit, written map[string]string) error {
	if u.Unit == nil {
		return fmt.Errorf("monitor: unit %q has no loaded artifacts (load error: %w)", u.Name, u.LoadErr)
	}
	provenance := fmt.Sprintf("unit %q (%s)", u.Name, u.Source)
	err := u.Unit.WalkArtifacts(func(relPath string, content []byte) error {
		if owner, taken := written[relPath]; taken {
			return fmt.Errorf(
				"monitor: bootstrap file %s from %s collides with %s — artifact names must not overlap",
				relPath, provenance, owner,
			)
		}
		outPath := filepath.Join(destDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(outPath), bootstrapDirMode); err != nil {
			return fmt.Errorf("mkdir for %s: %w", relPath, err)
		}
		if err := os.WriteFile(outPath, content, bootstrapFileMode); err != nil {
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		written[relPath] = provenance
		return nil
	})
	if err != nil {
		return fmt.Errorf("overlay unit %q: %w", u.Name, err)
	}
	return nil
}

// validateSavedObjectIDs scans every rendered saved-object source — each
// ndjson line's (type, id) and each explore panel filename (its id) —
// and errors on a duplicate ID across providers. Dashboards `_import`
// runs with overwrite=true, so an undetected duplicate would be silent
// last-write-wins.
func validateSavedObjectIDs(destDir string, written map[string]string) error {
	claim := newSavedObjectClaimer()

	for rel, provenance := range written {
		dir := path.Dir(rel)
		switch {
		case dir == bundler.MonitoringDirSavedObjects && strings.HasSuffix(rel, ".ndjson"):
			if err := scanNDJSONIDs(destDir, rel, provenance, claim); err != nil {
				return err
			}
		case dir == path.Join(bundler.MonitoringDirSavedObjects, bundler.MonitoringDirExplore):
			id := strings.TrimSuffix(path.Base(rel), ".json")
			if err := claim("explore", id, provenance); err != nil {
				return err
			}
		}
	}
	return nil
}

// newSavedObjectClaimer returns a closure recording (type, id) ownership
// and erroring when two providers ship the same saved-object ID.
func newSavedObjectClaimer() func(typ, id, provenance string) error {
	type soKey struct{ typ, id string }
	owners := map[soKey]string{}
	return func(typ, id, provenance string) error {
		key := soKey{typ, id}
		if owner, taken := owners[key]; taken && owner != provenance {
			return fmt.Errorf(
				"monitor: saved object %s/%s shipped by both %s and %s — IDs must be unique across units",
				typ, id, owner, provenance,
			)
		}
		owners[key] = provenance
		return nil
	}
}

// scanNDJSONIDs claims each (type, id) pair in one rendered ndjson file.
func scanNDJSONIDs(destDir, rel, provenance string, claim func(typ, id, provenance string) error) error {
	raw, err := os.ReadFile(filepath.Join(destDir, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("read %s: %w", rel, err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if jsonErr := json.Unmarshal([]byte(line), &obj); jsonErr != nil {
			return fmt.Errorf("monitor: %s (%s): malformed ndjson line: %w", rel, provenance, jsonErr)
		}
		if claimErr := claim(obj.Type, obj.ID, provenance); claimErr != nil {
			return claimErr
		}
	}
	return nil
}

// writeUnitsMarker records the sorted active unit names the tree was
// rendered with.
func writeUnitsMarker(destDir string, units []ResolvedUnit) error {
	names := make([]string, 0, len(units))
	for _, u := range units {
		names = append(names, u.Name)
	}
	sort.Strings(names)
	content := strings.Join(names, "\n")
	if content != "" {
		content += "\n"
	}
	marker := filepath.Join(destDir, UnitsMarkerFile)
	if err := os.WriteFile(marker, []byte(content), bootstrapFileMode); err != nil {
		return fmt.Errorf("write units marker %s: %w", marker, err)
	}
	return nil
}

// ReadUnitsMarker returns the active unit names a rendered bootstrap dir
// was generated with, or nil when no marker exists (pre-units render).
func ReadUnitsMarker(destDir string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(destDir, UnitsMarkerFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read units marker: %w", err)
	}
	var names []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}
