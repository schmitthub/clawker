// clawkergenerate is a standalone binary for generating versions.json.
// It provides the same functionality as 'clawkergenerate' but can be run
// independently without the full clawkerCLI.
package main

import (
	"os"

	"github.com/schmitthub/clawker/internal/cmd/generate"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
)

// Build-time variables set by ldflags.
var (
	Version = "dev"
	Commit  = "none"
)

func main() {
	// Initialize logger
	logger.Init(false) // Debug mode controlled by --debug flag

	f := &cmdutil.Factory{
		Version:   Version,
		IOStreams: iostreams.System(),
	}

	// Create and execute the generate command
	cmd := generate.NewCmdGenerate(f, nil)
	cmd.Use = "clawkergenerate" // Override for standalone use
	cmd.Version = Version

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
