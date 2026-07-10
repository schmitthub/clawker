package install_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	installcmd "github.com/schmitthub/clawker/internal/cmd/bundle/install"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/internal/tui"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

// newFactory builds a Factory whose Config and BundleManager load a fresh
// config each call — mirroring one CLI invocation per Execute, so a file
// written by one run is discovered by the next.
func newFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		Version:   "",
		IOStreams: ios,
		TUI:       tui.NewTUI(ios),
		Client:    nil,
		Config: func() (config.Config, error) {
			return config.NewConfig()
		},
		Logger:          nil,
		CLIState:        nil,
		ProjectRegistry: nil,
		ProjectManager:  nil,
		GitManager:      nil,
		HostProxy:       nil,
		SocketBridge:    nil,
		Prompter:        nil,
		AdminClient:     nil,
		ControlPlane:    nil,
		HttpClient:      nil,
		BundleManager: func() (*bundle.Manager, error) {
			cfg, err := config.NewConfig()
			if err != nil {
				return nil, err
			}
			return bundle.NewManager(cfg), nil
		},
	}
	return f, out, errOut
}

func run(t *testing.T, f *cmdutil.Factory, args ...string) error {
	t.Helper()
	cmd := installcmd.NewCmdInstall(f, nil)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestInstall_WritesUserLayer(t *testing.T) {
	testenv.New(t)
	f, out, _ := newFactory(t)

	require.NoError(t, run(t, f, "https://github.com/acme/tools.git", "--ref", "v1.2.0"))

	assert.Contains(t, out.String(), "Declared bundle source")

	path, err := consts.UserProjectConfigFilePath()
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, "https://github.com/acme/tools.git")
	assert.Contains(t, body, "v1.2.0")
}

func TestInstall_OwnerRepoExpands(t *testing.T) {
	testenv.New(t)
	f, _, _ := newFactory(t)

	require.NoError(t, run(t, f, "acme/tools", "--sha", testSHA))

	path, err := consts.UserProjectConfigFilePath()
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "https://github.com/acme/tools.git")
}

func TestInstall_Idempotent(t *testing.T) {
	testenv.New(t)
	f, _, errOut := newFactory(t)

	require.NoError(t, run(t, f, "https://github.com/acme/tools.git", "--ref", "v1"))
	require.NoError(t, run(t, f, "https://github.com/acme/tools.git", "--ref", "v1"))

	assert.Contains(t, errOut.String(), "already declared")

	// The source appears exactly once.
	cfg, err := config.NewConfig()
	require.NoError(t, err)
	count := 0
	for _, d := range cfg.BundleDeclarations() {
		if d.Source.URL == "https://github.com/acme/tools.git" {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

func TestInstall_RemoteWithoutRefOrSHA_Errors(t *testing.T) {
	testenv.New(t)
	f, _, _ := newFactory(t)

	err := run(t, f, "https://github.com/acme/tools.git")
	require.Error(t, err)

	// Nothing was written.
	path, pErr := consts.UserProjectConfigFilePath()
	require.NoError(t, pErr)
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr))
}
