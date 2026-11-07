package register

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	"github.com/schmitthub/clawker/internal/prompter"
)

// bareDirEnv puts cwd in a directory without a project config and isolates
// the config dir. It returns the user-level config path.
func bareDirEnv(t *testing.T) string {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv(consts.EnvConfigDir, configDir)
	t.Chdir(t.TempDir())
	return filepath.Join(configDir, consts.ProjectConfigFile)
}

func registerOpts(
	t *testing.T, input string, yes bool,
) (*RegisterOptions, *projectmocks.ProjectManagerMock, func() string) {
	t.Helper()
	ios, inBuf, _, errBuf := iostreams.Test()
	ios.SetStdinTTY(true)
	ios.SetStdoutTTY(true)
	inBuf.WriteString(input)
	mgr := projectmocks.NewMockProjectManager()
	mgr.RegisterFunc = func(_ context.Context, name, repoPath string) (project.Project, error) {
		return projectmocks.NewMockProject(name, repoPath), nil
	}
	return &RegisterOptions{
		IOStreams:      ios,
		Prompter:       func() *prompter.Prompter { return prompter.NewPrompter(ios) },
		Config:         func() (config.Config, error) { return configmocks.NewBlankConfig(), nil },
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Name:           "demo",
		Yes:            yes,
	}, mgr, errBuf.String
}

func TestProjectRegisterRun_BareDir(t *testing.T) {
	t.Run("refuses without a user-level config", func(t *testing.T) {
		userPath := bareDirEnv(t)
		opts, mgr, _ := registerOpts(t, "", true)

		err := projectRegisterRun(context.Background(), opts)
		require.ErrorContains(t, err, userPath)
		require.Empty(t, mgr.RegisterCalls())
	})

	t.Run("registers with --yes and prints the notice", func(t *testing.T) {
		userPath := bareDirEnv(t)
		require.NoError(t, os.WriteFile(userPath, []byte("{}\n"), 0o644))
		opts, mgr, stderr := registerOpts(t, "", true)

		require.NoError(t, projectRegisterRun(context.Background(), opts))
		require.Len(t, mgr.RegisterCalls(), 1)
		require.Contains(t, stderr(), userPath)
	})

	t.Run("declined confirmation aborts", func(t *testing.T) {
		userPath := bareDirEnv(t)
		require.NoError(t, os.WriteFile(userPath, []byte("{}\n"), 0o644))
		opts, mgr, _ := registerOpts(t, "n\n", false)

		err := projectRegisterRun(context.Background(), opts)
		require.ErrorIs(t, err, cmdutil.ErrAborted)
		require.Empty(t, mgr.RegisterCalls())
	})
}
