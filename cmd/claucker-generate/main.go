// claucker-generate is a standalone binary for generating versions.json.
// It provides the same functionality as 'claucker generate' but can be run
// independently without the full claucker CLI.
package main

import (
	"os"

	"github.com/schmitthub/claucker/pkg/cmd/generate"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
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
		cmdutil.PrintError("Failed to get working directory: %v", err)
		os.Exit(1)
	}

	f := &cmdutil.Factory{
		WorkDir: wd,
		Version: Version,
	}

	// Create and execute the generate command
	cmd := generate.NewCmdGenerate(f)
	cmd.Use = "claucker-generate" // Override for standalone use
	cmd.Version = Version

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
