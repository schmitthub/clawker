package importcmd

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func executeImport(t *testing.T, cfg config.Config, validCommand func(string) bool, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	tio, _, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Config:    func() (config.Config, error) { return cfg, nil },
	}
	cmd := NewCmdImport(f, validCommand, nil)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestImportRun(t *testing.T) {
	isRun := func(name string) bool { return name == "run" }

	seedProjectAliases := func(t *testing.T, cfg config.Config, aliases map[string]string) {
		t.Helper()
		require.NoError(t, cfg.ProjectStore().Set(func(p *config.Project) {
			p.Aliases = aliases
		}))
	}

	t.Run("imports valid entries, skips invalid and shadowing ones", func(t *testing.T) {
		cfg := configmocks.NewIsolatedTestConfig(t)
		seedProjectAliases(t, cfg, map[string]string{
			"team":      "ps --quiet",
			"run":       "version",      // shadows builtin
			"bad alias": "version",      // invalid name
			"broken":    `version "oop`, // unparseable expansion
		})

		stdout, stderr, err := executeImport(t, cfg, isRun)
		require.NoError(t, err)
		assert.Contains(t, stdout, "1 added, 0 overwritten, 3 skipped")
		assert.Contains(t, stderr, "shadows an existing clawker command")

		require.NoError(t, cfg.SettingsStore().Refresh())
		aliases := cfg.Settings().Aliases
		assert.Equal(t, "ps --quiet", aliases["team"])
		_, hasRun := aliases["run"]
		assert.False(t, hasRun)
	})

	t.Run("existing alias kept unless clobber", func(t *testing.T) {
		cfg := configmocks.NewIsolatedTestConfig(t)
		require.NoError(t, cfg.SettingsStore().Set(func(s *config.Settings) {
			s.Aliases = map[string]string{"team": "mine"}
		}))
		require.NoError(t, cfg.SettingsStore().Write())
		seedProjectAliases(t, cfg, map[string]string{"team": "theirs"})

		_, stderr, err := executeImport(t, cfg, isRun)
		require.NoError(t, err)
		assert.Contains(t, stderr, "--clobber")
		require.NoError(t, cfg.SettingsStore().Refresh())
		assert.Equal(t, "mine", cfg.Settings().Aliases["team"])

		_, _, err = executeImport(t, cfg, isRun, "--clobber")
		require.NoError(t, err)
		require.NoError(t, cfg.SettingsStore().Refresh())
		assert.Equal(t, "theirs", cfg.Settings().Aliases["team"])
	})

	t.Run("no project aliases is a no-op", func(t *testing.T) {
		cfg := configmocks.NewIsolatedTestConfig(t)
		_, stderr, err := executeImport(t, cfg, isRun)
		require.NoError(t, err)
		assert.Contains(t, stderr, "No aliases found")
	})
}
