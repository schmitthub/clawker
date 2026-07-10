package update_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	updatecmd "github.com/schmitthub/clawker/internal/cmd/bundle/update"
	"github.com/schmitthub/clawker/internal/cmdutil"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testenv"
)

func newFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	testenv.New(t)
	ios, _, _, errOut := iostreams.Test()
	mgr := bundle.NewManager(configmocks.NewBlankConfig())
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
	return f, errOut
}

func run(t *testing.T, f *cmdutil.Factory, args ...string) error {
	t.Helper()
	cmd := updatecmd.NewCmdUpdate(f, nil)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestUpdate_NoBundlesIsANoOp(t *testing.T) {
	// No declared or cached bundles: the update pass finds nothing and succeeds
	// without error (the real refetch pipeline is covered in the bundle package
	// integration tests).
	f, errOut := newFactory(t)
	require.NoError(t, run(t, f))
	assert.Empty(t, errOut.String())
}

func TestUpdate_NamedNotCachedErrors(t *testing.T) {
	f, _ := newFactory(t)
	err := run(t, f, "acme.tools")
	require.Error(t, err)
	assert.ErrorIs(t, err, bundle.ErrNotCached)
}

func TestUpdate_InvalidIdentity(t *testing.T) {
	f, _ := newFactory(t)
	err := run(t, f, "acme.tools.node")
	require.Error(t, err)
	assert.NotErrorIs(t, err, cmdutil.SilentError)
}
