package harness_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	harnesscmd "github.com/schmitthub/clawker/internal/cmd/harness"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// Conformance: E17 — remove only deletes entries present in a writable project layer.
func TestHarnessRemove_Registered(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	dir := writeHarnessDir(t, t.TempDir(), "codex")

	f, _ := newTestFactory(t, cfg)
	reg := harnesscmd.NewCmdHarnessRegister(f, nil)
	reg.SetArgs([]string{dir})
	require.NoError(t, reg.Execute())

	rm := harnesscmd.NewCmdHarnessRemove(f, nil)
	rm.SetArgs([]string{"codex"})
	require.NoError(t, rm.Execute())

	assert.Empty(t, cfg.Project().Harnesses["codex"].Path)
	_, stillThere := cfg.Project().Harnesses["codex"]
	assert.False(t, stillThere, "entry with no init config should be dropped entirely")
}

func TestHarnessRemove_PreservesInitConfig(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	require.NoError(t, cfg.ProjectStore().Set("harnesses.codex.post_init", "echo hi"))
	require.NoError(t, cfg.ProjectStore().Write())

	dir := writeHarnessDir(t, t.TempDir(), "codex-src")
	f, _ := newTestFactory(t, cfg)
	reg := harnesscmd.NewCmdHarnessRegister(f, nil)
	reg.SetArgs([]string{dir, "--name", "codex"})
	require.NoError(t, reg.Execute())

	rm := harnesscmd.NewCmdHarnessRemove(f, nil)
	rm.SetArgs([]string{"codex"})
	require.NoError(t, rm.Execute())

	entry, stillThere := cfg.Project().Harnesses["codex"]
	require.True(t, stillThere, "entry kept because it carries init config")
	assert.Empty(t, entry.Path, "registration path removed")
	assert.Equal(t, "echo hi", entry.PostInit, "init config preserved")
}

// Conformance: E17 — shipped harnesses are an immutable virtual base layer; remove rejects a shipped name.
func TestHarnessRemove_ShippedRejected(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	f, _ := newTestFactory(t, cfg)

	rm := harnesscmd.NewCmdHarnessRemove(f, nil)
	rm.SetArgs([]string{"claude"})
	err := rm.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "built-in")
}

func TestHarnessRemove_Unknown(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	f, _ := newTestFactory(t, cfg)

	rm := harnesscmd.NewCmdHarnessRemove(f, nil)
	rm.SetArgs([]string{"nonexistent"})
	err := rm.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")
}

// TestHarnessRemove_MonitoringUnitHint pins the deliberate asymmetry:
// removing a harness leaves the host-global monitoring registry untouched
// (another project may register the same bundle) and instead names any
// registered unit living under the removed bundle's path.
func TestHarnessRemove_MonitoringUnitHint(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	dir := writeHarnessDir(t, t.TempDir(), "codex")
	writeMonitoringUnit(t, dir, "codex-usage")

	ios, _, _, errOut := iostreams.Test()
	//nolint:exhaustruct // test factory: only the fields the commands read are wired
	f := &cmdutil.Factory{
		IOStreams: ios,
		TUI:       tui.NewTUI(ios),
		Config:    func() (config.Config, error) { return cfg, nil },
	}

	reg := harnesscmd.NewCmdHarnessRegister(f, nil)
	reg.SetArgs([]string{dir, "--name", "codex"})
	require.NoError(t, reg.Execute())

	// Promote the bundle's unit into the host-global registry, as the
	// register hint instructs.
	unitPath := filepath.Join(dir, "monitoring", "codex-usage")
	require.NoError(t, cfg.SettingsStore().Set("monitoring.units.codex-usage.path", unitPath))
	require.NoError(t, cfg.SettingsStore().Write())

	rm := harnesscmd.NewCmdHarnessRemove(f, nil)
	rm.SetArgs([]string{"codex"})
	require.NoError(t, rm.Execute())

	assert.Contains(t, errOut.String(), "Monitoring unit 'codex-usage'")
	assert.Contains(t, errOut.String(), "clawker monitor remove codex-usage")
	assert.Contains(t, cfg.Settings().Monitoring.Units, "codex-usage",
		"harness remove must not touch the host-global monitoring registry")
}
