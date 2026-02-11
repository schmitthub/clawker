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

func main() {
	// Initialize logger
	logger.Init()

	f := &cmdutil.Factory{
		IOStreams: iostreams.System(),
	}

	// Create and execute the generate command
	cmd := generate.NewCmdGenerate(f, nil)
	cmd.Use = "clawkergenerate" // Override for standalone use

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
