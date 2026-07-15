package remove_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundle/componentcheck"
	removecmd "github.com/schmitthub/clawker/internal/cmd/bundle/remove"
	"github.com/schmitthub/clawker/internal/cmdutil"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testenv"
)

func newFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	mgr := bundle.NewManager(configmocks.NewBlankConfig(), componentcheck.Validate)
	f := &cmdutil.Factory{
		Version:         "",
		IOStreams:       ios,
		TUI:             nil,
		Client:          nil,
		Config:          nil,
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
		BundleManager:   func() (*bundle.Manager, error) { return mgr, nil },
	}
	return f, out, errOut
}

func run(t *testing.T, f *cmdutil.Factory, args ...string) error {
	t.Helper()
	cmd := removecmd.NewCmdRemove(f, nil)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestRemove_PurgesCache(t *testing.T) {
	testenv.New(t)
	root, err := consts.BundlesSubdir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "acme", "tools", "1.0.0"), 0o755))

	f, out, _ := newFactory(t)
	require.NoError(t, run(t, f, "acme.tools"))

	assert.Contains(t, out.String(), "Removed cached bundle acme.tools")
	_, statErr := os.Stat(filepath.Join(root, "acme", "tools"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestRemove_NotCached(t *testing.T) {
	testenv.New(t)
	f, _, errOut := newFactory(t)
	require.NoError(t, run(t, f, "acme.tools"))
	assert.Contains(t, errOut.String(), "no cached bundle acme.tools")
}

func TestRemove_InvalidIdentity(t *testing.T) {
	testenv.New(t)
	f, _, _ := newFactory(t)
	err := run(t, f, "acme.tools.node") // 3 segments is a component address, not an identity
	require.Error(t, err)
}
