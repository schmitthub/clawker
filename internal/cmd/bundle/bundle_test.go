package bundle_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	bundlecmd "github.com/schmitthub/clawker/internal/cmd/bundle"
	"github.com/schmitthub/clawker/internal/cmdutil"
)

func TestNewCmdBundle_RegistersSubcommands(t *testing.T) {
	f := &cmdutil.Factory{
		Version:         "",
		IOStreams:       nil,
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

	cmd := bundlecmd.NewCmdBundle(f)

	got := map[string]bool{}
	for _, sub := range cmd.Commands() {
		got[sub.Name()] = true
	}
	for _, name := range []string{"install", "list", "remove", "update", "validate"} {
		assert.True(t, got[name], "expected subcommand %q", name)
	}
}
