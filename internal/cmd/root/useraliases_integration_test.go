package root

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newIntegrationFactory wires a real file-backed isolated config into a
// Factory so the full command plumbing runs against actual files.
func newIntegrationFactory(t *testing.T) (*cmdutil.Factory, config.Config) {
	t.Helper()
	cfg := configmocks.NewIsolatedTestConfig(t)
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Config:    func() (config.Config, error) { return cfg, nil },
	}
	return f, cfg
}

// execute builds a fresh root tree (re-running alias registration) and runs
// the given args through it, returning combined output. The Factory's
// IOStreams are replaced first — commands capture them at construction.
func execute(t *testing.T, f *cmdutil.Factory, args ...string) (string, error) {
	t.Helper()
	tio, _, stdout, _ := iostreams.Test()
	f.IOStreams = tio
	f.TUI = tui.NewTUI(tio)
	root, err := NewCmdRoot(f, "9.9.9-test", "2026-01-01")
	require.NoError(t, err)
	root.SetOut(stdout)
	root.SetErr(stdout)
	root.SetArgs(args)
	err = root.Execute()
	return stdout.String(), err
}

func TestAliasSetThenDispatch_Integration(t *testing.T) {
	f, cfg := newIntegrationFactory(t)

	// set persists through real command plumbing to the settings file.
	out, err := execute(t, f, "alias", "set", "ver", "version")
	require.NoError(t, err)
	assert.Contains(t, out, `Added alias "ver"`)

	// Reload from disk and rebuild the tree: the alias must register and
	// dispatch through expansion → root re-execution.
	require.NoError(t, cfg.SettingsStore().Refresh())
	out, err = execute(t, f, "ver")
	require.NoError(t, err)
	assert.Contains(t, out, "9.9.9-test", "alias should expand to the version command")
}

func TestAliasDeleteDefaultThenGone_Integration(t *testing.T) {
	f, cfg := newIntegrationFactory(t)

	// The shipped default registers out of the box.
	root, err := NewCmdRoot(f, "", "")
	require.NoError(t, err)
	require.NotNil(t, findOwnCommand(root, "dev"))

	_, err = execute(t, f, "alias", "delete", "dev")
	require.NoError(t, err)

	require.NoError(t, cfg.SettingsStore().Refresh())
	root, err = NewCmdRoot(f, "", "")
	require.NoError(t, err)
	assert.Nil(t, findOwnCommand(root, "dev"), "disabled default must not register")
}

func TestAliasPlaceholderError_Integration(t *testing.T) {
	f, cfg := newIntegrationFactory(t)

	_, err := execute(t, f, "alias", "set", "lg", "logs $1")
	require.NoError(t, err)
	require.NoError(t, cfg.SettingsStore().Refresh())

	_, err = execute(t, f, "lg")
	require.ErrorContains(t, err, "not enough arguments")
	require.ErrorContains(t, err, `alias "lg"`)
}
