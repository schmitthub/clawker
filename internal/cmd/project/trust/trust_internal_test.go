package trust

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/testenv"
)

// testFactory is the minimal Factory the trust command needs for flag parsing.
func testFactory(ios *iostreams.IOStreams) *cmdutil.Factory {
	return &cmdutil.Factory{
		Version: "", IOStreams: ios, TUI: nil, Client: nil, Config: nil, Logger: nil, CLIState: nil,
		ProjectRegistry: nil, ProjectManager: nil, GitManager: nil, HostProxy: nil, SocketBridge: nil,
		Prompter: nil, AdminClient: nil, ControlPlane: nil, HttpClient: nil, BundleManager: nil, Session: nil,
	}
}

// --- Tier 1: Flag parsing tests ---

func TestNewCmdTrust_RunFReceivesFlags(t *testing.T) {
	ios, _, out, _ := iostreams.Test()
	f := testFactory(ios)

	called := false
	cmd := NewCmdTrust(f, func(_ context.Context, opts *TrustOptions) error {
		called = true
		assert.True(t, opts.Check)
		return nil
	})
	cmd.SetArgs([]string{"--check"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	require.NoError(t, cmd.Execute())
	assert.True(t, called)
	assert.Empty(t, out.String())
}

func TestNewCmdTrust_RejectsArgs(t *testing.T) {
	ios, _, out, _ := iostreams.Test()
	f := testFactory(ios)

	cmd := NewCmdTrust(f, func(_ context.Context, _ *TrustOptions) error { return nil })
	cmd.SetArgs([]string{"extra"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	require.Error(t, cmd.Execute())
	assert.Empty(t, out.String())
}

// --- Tier 2: Run function tests ---

// registeredProject returns options bound to an isolated registered project
// whose config file holds projectYAML.
func registeredProject(t *testing.T, env *testenv.Env, projDir, projectYAML string) (*TrustOptions, *bytes.Buffer) {
	t.Helper()
	env.WriteYAML(t, testenv.ProjectConfig, projDir, projectYAML)
	cfg, err := config.NewConfig(config.WithProjectRoot(projDir))
	require.NoError(t, err)
	reg := env.Registry(t)
	ios, _, out, _ := iostreams.Test()
	return &TrustOptions{
		IOStreams:       ios,
		Config:          func() (config.Config, error) { return cfg, nil },
		ProjectRegistry: func() (project.Registry, error) { return reg, nil },
		Check:           false,
	}, out
}

func TestTrustRun_AcceptsAndChecks(t *testing.T) {
	env := testenv.New(t)
	projDir := filepath.Join(env.Dirs.Base, "proj")
	require.NoError(t, os.MkdirAll(projDir, 0o755))
	t.Chdir(projDir)
	_, err := env.Registry(t).Register("proj", projDir)
	require.NoError(t, err)

	opts, out := registeredProject(t, env, projDir, "security:\n  docker_socket: true\n")
	opts.Check = true
	require.ErrorIs(t, trustRun(context.Background(), opts), cmdutil.SilentError)
	assert.Contains(t, out.String(), "never accepted")

	opts, out = registeredProject(t, env, projDir, "security:\n  docker_socket: true\n")
	require.NoError(t, trustRun(context.Background(), opts))
	assert.Contains(t, out.String(), "Accepted")
	entry, ok, err := env.Registry(t).ProjectByRoot(projDir)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, entry.ConfigTrust, "security")

	opts, out = registeredProject(t, env, projDir, "security:\n  docker_socket: true\n")
	opts.Check = true
	require.NoError(t, trustRun(context.Background(), opts))
	assert.Contains(t, out.String(), "unchanged")

	opts, out = registeredProject(t, env, projDir, "security:\n  docker_socket: false\n")
	opts.Check = true
	require.ErrorIs(t, trustRun(context.Background(), opts), cmdutil.SilentError)
	assert.Contains(t, out.String(), "changed: security")
}

func TestTrustRun_OutsideProject(t *testing.T) {
	cfg, err := config.NewBlankConfig()
	require.NoError(t, err)
	ios, _, out, _ := iostreams.Test()
	opts := &TrustOptions{
		IOStreams:       ios,
		Config:          func() (config.Config, error) { return cfg, nil },
		ProjectRegistry: func() (project.Registry, error) { return nil, errors.New("must not be called") },
		Check:           false,
	}
	err = trustRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not inside a registered project")
	assert.Empty(t, out.String())
}
