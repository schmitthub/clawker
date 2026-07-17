package monitor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

// StackRender reports the outcome of rendering the monitoring stack config
// files, so the command layer can print what changed and decide whether the
// collector must be recreated.
type StackRender struct {
	// Written lists the top-level config files (re)written this render.
	Written []string
	// Skipped lists the top-level config files left in place (existed, no force).
	Skipped []string
	// OtelConfigChanged reports whether the rendered otel-config.yaml bytes
	// differ from what was on disk before this render — the signal `monitor up`
	// uses to force-recreate the running collector so it re-reads the mounted
	// config. False on a first render (no prior file): a fresh collector reads
	// the new config on creation, so no recreate is needed.
	OtelConfigChanged bool
}

// PrepareTemplateData builds [MonitorTemplateData] from settings and the seeded
// unit union, then mints (idempotently) and populates the OTEL mTLS host cert
// paths that compose.yaml bind-mounts into the collector for its trusted
// otlp/infra receiver. Shared by `monitor init` and `monitor up` so both render
// identical config from the same inputs.
func PrepareTemplateData(s *config.Settings, union []SeededUnit) (MonitorTemplateData, error) {
	data, err := NewMonitorTemplateData(s, union)
	if err != nil {
		return MonitorTemplateData{}, err
	}

	// EnsureAuthMaterial is idempotent: a no-op when the CLI already minted the
	// OTEL mTLS material. The cert paths must be populated on the template data
	// before compose.yaml is rendered so the collector's bind mounts resolve.
	if authErr := auth.EnsureAuthMaterial(); authErr != nil {
		return MonitorTemplateData{}, fmt.Errorf("monitor: ensure auth material for OTEL mTLS: %w", authErr)
	}
	certPath, err := consts.AuthOtelServerCertPath()
	if err != nil {
		return MonitorTemplateData{}, fmt.Errorf("monitor: resolve otel server cert path: %w", err)
	}
	keyPath, err := consts.AuthOtelServerKeyPath()
	if err != nil {
		return MonitorTemplateData{}, fmt.Errorf("monitor: resolve otel server key path: %w", err)
	}
	// Trust anchor for the collector's mTLS-gated otlp/infra receiver: MUST be
	// the infra intermediate CA, never the CLI root. The CLI root also signs
	// agent leaves, so using it here would let any agent forge trusted-lane
	// records; the infra intermediate signs only envoy/coredns/cp leaves.
	caPath, err := consts.AuthInfraCACertPath()
	if err != nil {
		return MonitorTemplateData{}, fmt.Errorf("monitor: resolve otel infra CA path: %w", err)
	}
	data.OtelServerCertHostPath = certPath
	data.OtelServerKeyHostPath = keyPath
	data.OtelCAHostPath = caPath
	return data, nil
}

// RenderStack renders compose.yaml, otel-config.yaml, and prometheus.yaml into
// monitorDir and materializes the opensearch-bootstrap tree from the current
// project's resolvable units. The three top-level files are gated by force —
// skipped when they already exist and force is false (the `monitor init`
// skip-if-exists ergonomic); `monitor up` passes force=true because the
// throwaway-stack model always re-renders from the binary + config. The bootstrap
// tree is always re-rendered (it holds only generated content).
func RenderStack(
	monitorDir string,
	data MonitorTemplateData,
	bootstrapUnits []ResolvedUnit,
	force bool,
) (StackRender, error) {
	files := []struct {
		name string
		tmpl string
	}{
		{ComposeFileName, ComposeTemplate},
		{OtelConfigFileName, OtelConfigTemplate},
		{PrometheusFileName, PrometheusTemplate},
	}

	result := StackRender{Written: nil, Skipped: nil, OtelConfigChanged: false}
	for _, f := range files {
		rendered, renderErr := RenderTemplate(f.name, f.tmpl, data)
		if renderErr != nil {
			return StackRender{}, fmt.Errorf("render %s: %w", f.name, renderErr)
		}
		filePath := filepath.Join(monitorDir, f.name)
		existing, readErr := os.ReadFile(filePath)
		exists := readErr == nil
		if f.name == OtelConfigFileName {
			result.OtelConfigChanged = exists && !bytes.Equal(existing, []byte(rendered))
		}
		if exists && !force {
			result.Skipped = append(result.Skipped, f.name)
			continue
		}
		//nolint:gosec // generated, non-secret monitoring config; conventional world-readable perms
		if writeErr := os.WriteFile(filePath, []byte(rendered), 0o644); writeErr != nil {
			return StackRender{}, fmt.Errorf("write %s: %w", f.name, writeErr)
		}
		result.Written = append(result.Written, f.name)
	}

	bootstrapDir := filepath.Join(monitorDir, OpenSearchBootstrapDirName)
	if writeErr := WriteOpenSearchBootstrap(bootstrapDir, data, bootstrapUnits); writeErr != nil {
		return StackRender{}, fmt.Errorf("write opensearch bootstrap dir: %w", writeErr)
	}
	return result, nil
}
