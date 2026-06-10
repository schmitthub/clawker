package delete

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// executeDelete runs one alias delete invocation with a prod-shaped
// factory: the Config closure loads a fresh config per call, so consecutive
// invocations see each other's writes exactly like consecutive CLI runs.
func executeDelete(t *testing.T, name string) (stdout string, err error) {
	t.Helper()
	tio, _, out, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Config:    func() (config.Config, error) { return config.NewConfig() },
	}
	cmd := NewCmdDelete(f, nil)
	cmd.SetArgs([]string{name})
	cmd.SetOut(out)
	err = cmd.Execute()
	return out.String(), err
}

// seedUserAliases writes an aliases map into the user config-dir
// clawker.yaml — the file alias set maintains.
func seedUserAliases(t *testing.T, env *testenv.Env, yaml string) string {
	t.Helper()
	path := filepath.Join(env.Dirs.Config, "clawker.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))
	return path
}

func loadAliases(t *testing.T) map[string]string {
	t.Helper()
	cfg, err := config.NewConfig()
	require.NoError(t, err)
	return cfg.Project().Aliases
}

func TestDeleteRun(t *testing.T) {
	t.Run("user alias is removed from the user config file", func(t *testing.T) {
		env := testenv.New(t)
		path := seedUserAliases(t, env, "aliases:\n  v: version\n")

		stdout, err := executeDelete(t, "v")
		require.NoError(t, err)
		assert.Contains(t, stdout, "Wrote "+path)

		_, exists := loadAliases(t)["v"]
		assert.False(t, exists, "user alias should be gone after delete")
	})

	t.Run("unknown alias errors", func(t *testing.T) {
		testenv.New(t)
		_, err := executeDelete(t, "nope")
		assert.ErrorContains(t, err, "no alias")
	})

	t.Run("default alias is disabled not removed, second delete errors", func(t *testing.T) {
		testenv.New(t)
		stdout, err := executeDelete(t, "go")
		require.NoError(t, err)
		assert.Contains(t, stdout, "Disabled default alias")

		expansion, exists := loadAliases(t)["go"]
		assert.True(t, exists, "default alias key must survive (union merge)")
		assert.Empty(t, expansion, "default alias should be disabled via empty expansion")

		_, err = executeDelete(t, "go")
		assert.ErrorContains(t, err, "already disabled")
	})

	t.Run("overridden default is cleared from the file and disabled", func(t *testing.T) {
		env := testenv.New(t)
		path := seedUserAliases(t, env, "aliases:\n  go: version\n")

		stdout, err := executeDelete(t, "go")
		require.NoError(t, err)
		assert.Contains(t, stdout, "Wrote "+path)
		assert.Contains(t, stdout, "Disabled default alias")

		expansion, exists := loadAliases(t)["go"]
		assert.True(t, exists, "defaults-layer key survives")
		assert.Empty(t, expansion, "override replaced by the disabling empty expansion")
	})
}
