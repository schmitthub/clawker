// clawkergenerate is a standalone binary for generating versions.json.
// It provides the same functionality as 'clawkergenerate' but can be run
// independently without the full clawkerCLI.
package main

import (
	"fmt"
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

	// Create factory with working directory
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to get working directory: %v\n", err)
		os.Exit(1)
	}

	f := &cmdutil.Factory{
		WorkDir:  func() (string, error) { return wd, nil },
		Version:  Version,
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
