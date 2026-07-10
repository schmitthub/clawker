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
)

func newFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
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

func TestUpdate_NotWired(t *testing.T) {
	cases := map[string][]string{
		"no arg":         nil,
		"named identity": {"acme.tools"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			f, errOut := newFactory(t)
			err := run(t, f, args...)
			require.ErrorIs(t, err, cmdutil.SilentError)
			assert.Contains(t, errOut.String(), "not yet available")
		})
	}
}

func TestUpdate_InvalidIdentity(t *testing.T) {
	f, _ := newFactory(t)
	err := run(t, f, "acme.tools.node")
	require.Error(t, err)
	assert.NotErrorIs(t, err, cmdutil.SilentError)
}
