package delete

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func executeDelete(t *testing.T, cfg config.Config, name string) error {
	t.Helper()
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Config:    func() (config.Config, error) { return cfg, nil },
	}
	cmd := NewCmdDelete(f, nil)
	cmd.SetArgs([]string{name})
	return cmd.Execute()
}

func TestDeleteRun(t *testing.T) {
	t.Run("user alias is removed from settings", func(t *testing.T) {
		cfg := configmocks.NewIsolatedTestConfig(t)
		require.NoError(t, cfg.SettingsStore().Set(func(s *config.Settings) {
			s.Aliases = map[string]string{"v": "version"}
		}))
		require.NoError(t, cfg.SettingsStore().Write())

		require.NoError(t, executeDelete(t, cfg, "v"))
		require.NoError(t, cfg.SettingsStore().Refresh())
		_, exists := cfg.Settings().Aliases["v"]
		assert.False(t, exists, "user alias should be gone after delete")
	})

	t.Run("default alias is disabled, not removed", func(t *testing.T) {
		cfg := configmocks.NewIsolatedTestConfig(t)
		require.NoError(t, executeDelete(t, cfg, "dev"))

		require.NoError(t, cfg.SettingsStore().Refresh())
		expansion, exists := cfg.Settings().Aliases["dev"]
		assert.True(t, exists, "default alias key must survive (union merge)")
		assert.Empty(t, expansion, "default alias should be disabled via empty expansion")
	})

	t.Run("unknown alias errors", func(t *testing.T) {
		cfg := configmocks.NewIsolatedTestConfig(t)
		assert.ErrorContains(t, executeDelete(t, cfg, "nope"), "no alias")
	})

	t.Run("already-disabled default errors", func(t *testing.T) {
		cfg := configmocks.NewIsolatedTestConfig(t)
		require.NoError(t, executeDelete(t, cfg, "dev"))
		assert.ErrorContains(t, executeDelete(t, cfg, "dev"), "already disabled")
	})
}
