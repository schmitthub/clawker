package harness

import (
	"github.com/schmitthub/clawker/internal/cmd/root"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewRootCmd creates a root command for integration tests using the
// provided factory. This allows tests to invoke subcommands through the
// full command hierarchy, inheriting settings like SilenceUsage from root.
func (h *Harness) NewRootCmd(f *cmdutil.Factory) (*cobra.Command, error) {
	return root.NewCmdRoot(f, "test", "test")
}
