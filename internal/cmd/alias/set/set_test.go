package set

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// executeSet runs one alias set invocation with a prod-shaped factory: the
// Config closure loads a fresh config per call, so consecutive invocations
// see each other's writes exactly like consecutive CLI runs.
func executeSet(t *testing.T, validCommand func(string) bool, args ...string) (stdout string, err error) {
	t.Helper()
	tio, _, out, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Config:    func() (config.Config, error) { return config.NewConfig() },
	}
	cmd := NewCmdSet(f, validCommand, nil)
	cmd.SetArgs(args)
	cmd.SetOut(out)
	err = cmd.Execute()
	return out.String(), err
}

// loadAliases re-reads the merged alias map the way a fresh CLI run would.
func loadAliases(t *testing.T) map[string]string {
	t.Helper()
	cfg, err := config.NewConfig()
	require.NoError(t, err)
	return cfg.Project().Aliases
}

func TestSetRun(t *testing.T) {
	isVersion := func(name string) bool { return name == "version" }

	t.Run("persists alias to the user config file and reports the path", func(t *testing.T) {
		env := testenv.New(t)
		stdout, err := executeSet(t, isVersion, "v", "version --short")
		require.NoError(t, err)
		assert.Contains(t, stdout, `Added alias "v"`)
		assert.Contains(t, stdout, "Wrote "+env.Dirs.Config)

		assert.Equal(t, "version --short", loadAliases(t)["v"])
	})

	t.Run("shadowing a builtin command is rejected", func(t *testing.T) {
		testenv.New(t)
		_, err := executeSet(t, isVersion, "version", "version")
		assert.ErrorContains(t, err, "cannot shadow")
	})

	t.Run("existing alias requires clobber", func(t *testing.T) {
		testenv.New(t)
		_, err := executeSet(t, isVersion, "v", "version")
		require.NoError(t, err)

		_, err = executeSet(t, isVersion, "v", "version --short")
		assert.ErrorContains(t, err, "--clobber")

		_, err = executeSet(t, isVersion, "v", "version --short", "--clobber")
		require.NoError(t, err)
		assert.Equal(t, "version --short", loadAliases(t)["v"])
	})

	t.Run("expansion must target a command or alias", func(t *testing.T) {
		testenv.New(t)
		_, err := executeSet(t, isVersion, "v", "nosuchcmd --flag")
		assert.ErrorContains(t, err, "not a clawker command")
	})

	t.Run("default go alias can be overridden with clobber", func(t *testing.T) {
		testenv.New(t)
		// go exists via the defaults layer.
		_, err := executeSet(t, isVersion, "go", "version")
		assert.ErrorContains(t, err, "--clobber")

		_, err = executeSet(t, isVersion, "go", "version", "--clobber")
		require.NoError(t, err)
		assert.Equal(t, "version", loadAliases(t)["go"])
	})
}
