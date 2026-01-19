// doc-gen is a standalone binary for generating CLI documentation.
// It provides documentation generation for clawker CLI in multiple formats
// (Markdown, man pages, YAML, reStructuredText) without the full clawker CLI.
package main

import (
	"os"

	"github.com/schmitthub/clawker/pkg/cmd/docs"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
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
		WorkDir:        wd,
		BuildOutputDir: wd, // Standalone binary defaults to CWD
		Version:        Version,
		Commit:         Commit,
	}

	// Create and execute the docs command
	cmd := docs.NewCmdDocs(f)
	cmd.Use = "doc-gen" // Override for standalone use
	cmd.Version = Version

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
