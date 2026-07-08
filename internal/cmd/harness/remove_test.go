package harness_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	harnesscmd "github.com/schmitthub/clawker/internal/cmd/harness"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
)

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
