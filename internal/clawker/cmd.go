package clawker

import (
	"github.com/schmitthub/clawker/internal/cmd/factory"
	"github.com/schmitthub/clawker/internal/cmd/root"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/logger"
)

// Build-time variables injected via ldflags
var (
	Version = "dev"
	Commit  = "none"
)

// Main is the entry point for the clawker CLI.
// It initializes the Factory, creates the root command, and executes it.
func Main() int {
	// Ensure logs are flushed on exit
	defer logger.CloseFileWriter()

	// Create factory with version info
	f := factory.New(Version, Commit)

	// Create root command
	rootCmd := root.NewCmdRoot(f)

	// Execute - use ExecuteC to get the executed command for contextual hint
	cmd, err := rootCmd.ExecuteC()
	if err != nil {
		// Print contextual help hint (Cobra already printed "Error: ...")
		cmdutil.PrintHelpHint(f.IOStreams, cmd.CommandPath())
		f.CloseClient()
		return 1
	}

	// Cleanup
	f.CloseClient()

	return 0
}
