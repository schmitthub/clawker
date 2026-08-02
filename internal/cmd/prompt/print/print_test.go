package printcmd_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundler"
	printcmd "github.com/schmitthub/clawker/internal/cmd/prompt/print"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

func newTestFactory(ios *iostreams.IOStreams) *cmdutil.Factory {
	return &cmdutil.Factory{
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
		BundleManager:   nil,
	}
}

func TestPromptPrint_WritesBriefingToStdout(t *testing.T) {
	ios, _, out, errOut := iostreams.Test()

	cmd := printcmd.NewCmdPrint(newTestFactory(ios), nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, bundler.AgentPromptContent, out.String())
	assert.Empty(t, errOut.String())
}
