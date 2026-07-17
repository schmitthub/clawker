package monitor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/monitor"
)

const renderSettings = `
monitoring:
  otel_collector_port: 4318
  otel_grpc_port: 4317
  otel_infra_port: 4319
  prometheus_port: 9090
  prometheus_metrics_port: 8889
  opensearch_port: 9200
  opensearch_dashboards_port: 5601
  opensearch_heap_mb: 512
`

// TestRenderStack_OtelChangeDetection exercises the signal `monitor up` uses to
// decide whether to force-recreate the running collector: OtelConfigChanged is
// false on a first render (no prior file → fresh collector reads the new config
// on creation) and false on an identical re-render, but true when the rendered
// otel-config bytes differ from what is on disk.
func TestRenderStack_OtelChangeDetection(t *testing.T) {
	dir := t.TempDir()

	data, err := monitor.NewMonitorTemplateData(
		configmocks.NewFromString("", renderSettings).SettingsStore().Read(), nil)
	require.NoError(t, err)

	// First render: no prior otel-config → not "changed".
	r1, err := monitor.RenderStack(dir, data, nil, true)
	require.NoError(t, err)
	assert.False(t, r1.OtelConfigChanged, "a first render has no prior config to differ from")
	assert.Contains(t, r1.Written, monitor.OtelConfigFileName)
	_, statErr := os.Stat(filepath.Join(dir, monitor.OpenSearchBootstrapDirName, "bootstrap.sh"))
	require.NoError(t, statErr, "bootstrap tree always renders")

	// Identical re-render: bytes match → not "changed".
	r2, err := monitor.RenderStack(dir, data, nil, true)
	require.NoError(t, err)
	assert.False(t, r2.OtelConfigChanged, "an identical re-render leaves the collector alone")

	// Changed data → different otel-config bytes → "changed".
	changed, err := monitor.NewMonitorTemplateData(
		configmocks.NewFromString("", `
monitoring:
  otel_collector_port: 4318
  otel_grpc_port: 4317
  otel_infra_port: 4319
  prometheus_port: 9090
  prometheus_metrics_port: 9999
  opensearch_port: 9200
  opensearch_dashboards_port: 5601
  opensearch_heap_mb: 512
`).SettingsStore().Read(), nil)
	require.NoError(t, err)
	r3, err := monitor.RenderStack(dir, changed, nil, true)
	require.NoError(t, err)
	assert.True(t, r3.OtelConfigChanged, "a changed otel-config must force a collector recreate")
}

// TestRenderStack_SkipIfExists pins the init skip-if-exists ergonomic: with
// force=false, existing top-level files are left in place while the bootstrap
// tree still re-renders.
func TestRenderStack_SkipIfExists(t *testing.T) {
	dir := t.TempDir()
	data, err := monitor.NewMonitorTemplateData(
		configmocks.NewFromString("", renderSettings).SettingsStore().Read(), nil)
	require.NoError(t, err)

	_, err = monitor.RenderStack(dir, data, nil, true)
	require.NoError(t, err)

	r, err := monitor.RenderStack(dir, data, nil, false)
	require.NoError(t, err)
	assert.Empty(t, r.Written, "nothing rewritten when files exist and force is false")
	assert.ElementsMatch(t,
		[]string{monitor.ComposeFileName, monitor.OtelConfigFileName, monitor.PrometheusFileName},
		r.Skipped,
	)
}
