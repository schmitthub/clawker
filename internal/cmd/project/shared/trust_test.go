package shared_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmd/project/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/testenv"
)

// trustEnv registers an isolated project, writes projectYAML into it, and
// returns deps whose Config loads that project. Call again with new YAML to
// simulate an agent edit between runs.
type trustEnv struct {
	env     *testenv.Env
	projDir string
	ios     *iostreams.IOStreams
	errOut  interface{ String() string }
}

func newTrustEnv(t *testing.T) *trustEnv {
	t.Helper()
	env := testenv.New(t)
	projDir := filepath.Join(env.Dirs.Base, "proj")
	require.NoError(t, os.MkdirAll(projDir, 0o755))
	t.Chdir(projDir)
	_, err := env.Registry(t).Register("proj", projDir)
	require.NoError(t, err)
	ios, _, _, errOut := iostreams.Test()
	return &trustEnv{env: env, projDir: projDir, ios: ios, errOut: errOut}
}

func (e *trustEnv) deps(t *testing.T, projectYAML string) shared.TrustDeps {
	t.Helper()
	e.env.WriteYAML(t, testenv.ProjectConfig, e.projDir, projectYAML)
	cfg, err := config.NewConfig(config.WithProjectRoot(e.projDir))
	require.NoError(t, err)
	reg := e.env.Registry(t)
	return shared.TrustDeps{
		IOStreams:       e.ios,
		Config:          func() (config.Config, error) { return cfg, nil },
		ProjectRegistry: func() (project.Registry, error) { return reg, nil },
		Prompter:        func() *prompter.Prompter { return prompter.NewPrompter(e.ios) },
	}
}

func (e *trustEnv) recorded(t *testing.T) map[string]string {
	t.Helper()
	entry, ok, err := e.env.Registry(t).ProjectByRoot(e.projDir)
	require.NoError(t, err)
	require.True(t, ok)
	return entry.ConfigTrust
}

func TestEnsureProjectConfigTrusted_FirstRunRecords(t *testing.T) {
	e := newTrustEnv(t)
	require.Nil(t, e.recorded(t))

	require.NoError(t, shared.EnsureProjectConfigTrusted(e.deps(t, "security:\n  docker_socket: true\n")))
	assert.Contains(t, e.errOut.String(), "first run")
	assert.Contains(t, e.recorded(t), "security")

	// Unchanged: silent.
	require.NoError(t, shared.EnsureProjectConfigTrusted(e.deps(t, "security:\n  docker_socket: true\n")))
}

func TestEnsureProjectConfigTrusted_ChangeWithoutPromptFails(t *testing.T) {
	e := newTrustEnv(t)
	require.NoError(t, shared.EnsureProjectConfigTrusted(e.deps(t, "security:\n  docker_socket: false\n")))
	before := e.recorded(t)

	err := shared.EnsureProjectConfigTrusted(
		e.deps(t, "security:\n  docker_socket: true\nagent:\n  from_env: [AWS_SECRET_ACCESS_KEY]\n"))
	require.ErrorIs(t, err, shared.ErrProjectConfigUntrusted)
	assert.Contains(t, err.Error(), "security, agent")
	assert.Contains(t, err.Error(), "clawker project trust")
	assert.Equal(t, before, e.recorded(t), "a refused change must not be recorded")
}

func TestEnsureProjectConfigTrusted_PromptDeclinedAndAccepted(t *testing.T) {
	e := newTrustEnv(t)
	require.NoError(t, shared.EnsureProjectConfigTrusted(e.deps(t, "security:\n  docker_socket: false\n")))
	before := e.recorded(t)

	ios, in, _, _ := iostreams.Test()
	ios.SetStdinTTY(true)
	ios.SetStdoutTTY(true)
	e.ios = ios

	in.WriteString("n\n")
	err := shared.EnsureProjectConfigTrusted(e.deps(t, "security:\n  docker_socket: true\n"))
	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Equal(t, before, e.recorded(t))

	in.Reset()
	in.WriteString("y\n")
	require.NoError(t, shared.EnsureProjectConfigTrusted(e.deps(t, "security:\n  docker_socket: true\n")))
	assert.NotEqual(t, before, e.recorded(t))
}

func TestEnsureProjectConfigTrusted_OutsideProjectIsNoop(t *testing.T) {
	cfg, err := config.NewBlankConfig()
	require.NoError(t, err)
	ios, _, _, errOut := iostreams.Test()
	err = shared.EnsureProjectConfigTrusted(shared.TrustDeps{
		IOStreams:       ios,
		Config:          func() (config.Config, error) { return cfg, nil },
		ProjectRegistry: func() (project.Registry, error) { return nil, errors.New("must not be called") },
		Prompter:        func() *prompter.Prompter { return nil },
	})
	require.NoError(t, err)
	assert.Empty(t, errOut.String())
}
