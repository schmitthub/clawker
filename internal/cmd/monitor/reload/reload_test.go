package reload_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmd/monitor/reload"
	"github.com/schmitthub/clawker/internal/cmdutil"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/tui"
)

func TestNewCmdReload(t *testing.T) {
	ios, _, _, _ := iostreams.Test() //nolint:dogsled // only the streams handle matters here
	mgr := bundle.NewManager(configmocks.NewBlankConfig())
	f := &cmdutil.Factory{
		Version:         "",
		IOStreams:       ios,
		TUI:             tui.NewTUI(ios),
		Client:          nil,
		Config:          nil,
		Logger:          func() (*logger.Logger, error) { return logger.Nop(), nil },
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

	var gotOpts *reload.ReloadOptions
	cmd := reload.NewCmdReload(f, func(_ context.Context, opts *reload.ReloadOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	require.NotNil(t, gotOpts, "expected runF to be called")
	assert.Equal(t, ios, gotOpts.IOStreams, "IOStreams wired from factory")
	assert.NotNil(t, gotOpts.BundleManager, "BundleManager wired from factory")
	assert.NotNil(t, gotOpts.Logger, "Logger wired from factory")
}
