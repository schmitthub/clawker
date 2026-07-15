package list_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundle/componentcheck"
	listcmd "github.com/schmitthub/clawker/internal/cmd/harness/list"
	"github.com/schmitthub/clawker/internal/cmdutil"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/internal/tui"
)

// The command inventories harnesses only.
func TestHarnessList_FloorHarnesses(t *testing.T) {
	testenv.New(t)
	ios, _, out, _ := iostreams.Test()
	mgr := bundle.NewManager(configmocks.NewBlankConfig(), componentcheck.Validate)
	f := &cmdutil.Factory{
		Version:         "",
		IOStreams:       ios,
		TUI:             tui.NewTUI(ios),
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

	cmd := listcmd.NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	s := out.String()
	assert.Contains(t, s, "claude")
	assert.Contains(t, s, "built-in")
	// Harnesses only — no stack rows.
	assert.NotContains(t, s, "node")
}
