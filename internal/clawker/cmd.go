package clawker

import (
	"github.com/schmitthub/clawker/pkg/cmd/root"
	"github.com/schmitthub/clawker/pkg/cmdutil"
)

// Build-time variables injected via ldflags
var (
	Version = "dev"
	Commit  = "none"
)

// Main is the entry point for the clawker CLI.
// It initializes the Factory, creates the root command, and executes it.
func Main() int {
	// Create factory with version info
	f := cmdutil.New(Version, Commit)

	// Create root command
	rootCmd := root.NewCmdRoot(f)

	// Execute
	if err := rootCmd.Execute(); err != nil {
		return 1
	}

	// Cleanup
	f.CloseEngine()

	return 0
}
