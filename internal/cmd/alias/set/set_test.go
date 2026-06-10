package set

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSetCmd(t *testing.T, cfg config.Config, validCommand func(string) bool) (*cobra.Command, *iostreams.IOStreams) {
	t.Helper()
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Config:    func() (config.Config, error) { return cfg, nil },
	}
	return NewCmdSet(f, validCommand, nil), tio
}

func executeSet(t *testing.T, cfg config.Config, validCommand func(string) bool, args ...string) error {
	t.Helper()
	cmd, _ := newSetCmd(t, cfg, validCommand)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestSetRun(t *testing.T) {
	isVersion := func(name string) bool { return name == "version" }

	t.Run("persists alias to settings file", func(t *testing.T) {
		cfg := configmocks.NewIsolatedTestConfig(t)
		require.NoError(t, executeSet(t, cfg, isVersion, "v", "version --short"))

		require.NoError(t, cfg.SettingsStore().Refresh())
		assert.Equal(t, "version --short", cfg.Settings().Aliases["v"])
	})

	t.Run("shadowing a builtin command is rejected", func(t *testing.T) {
		cfg := configmocks.NewIsolatedTestConfig(t)
		err := executeSet(t, cfg, isVersion, "version", "version")
		assert.ErrorContains(t, err, "cannot shadow")
	})

	t.Run("existing alias requires clobber", func(t *testing.T) {
		cfg := configmocks.NewIsolatedTestConfig(t)
		require.NoError(t, executeSet(t, cfg, isVersion, "v", "version"))

		err := executeSet(t, cfg, isVersion, "v", "version --short")
		assert.ErrorContains(t, err, "--clobber")

		require.NoError(t, executeSet(t, cfg, isVersion, "v", "version --short", "--clobber"))
		require.NoError(t, cfg.SettingsStore().Refresh())
		assert.Equal(t, "version --short", cfg.Settings().Aliases["v"])
	})

	t.Run("expansion must target a command or alias", func(t *testing.T) {
		cfg := configmocks.NewIsolatedTestConfig(t)
		err := executeSet(t, cfg, isVersion, "v", "nosuchcmd --flag")
		assert.ErrorContains(t, err, "not a clawker command")
	})

	t.Run("default dev alias can be overridden with clobber", func(t *testing.T) {
		cfg := configmocks.NewIsolatedTestConfig(t)
		// dev exists via the defaults layer.
		err := executeSet(t, cfg, isVersion, "dev", "version")
		assert.ErrorContains(t, err, "--clobber")

		require.NoError(t, executeSet(t, cfg, isVersion, "dev", "version", "--clobber"))
		require.NoError(t, cfg.SettingsStore().Refresh())
		assert.Equal(t, "version", cfg.Settings().Aliases["dev"])
	})
}
