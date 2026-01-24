// clawkergenerate is a standalone binary for generating versions.json.
// It provides the same functionality as 'clawkergenerate' but can be run
// independently without the full clawkerCLI.
package main

import (
	"os"

	"github.com/schmitthub/clawker/internal/cmd/generate"
	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
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

	// Create factory with working directory
	wd, err := os.Getwd()
	if err != nil {
		cmdutil2.PrintError("Failed to get working directory: %v", err)
		os.Exit(1)
	}

	f := &cmdutil2.Factory{
		WorkDir:        wd,
		BuildOutputDir: wd, // Standalone binary defaults to CWD
		Version:        Version,
	}

	// Create and execute the generate command
	cmd := generate.NewCmdGenerate(f)
	cmd.Use = "clawkergenerate" // Override for standalone use
	cmd.Version = Version

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
