package cmd

import (
	"fmt"
	"os"

	"github.com/claucker/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

var (
	// Version is set at build time
	Version = "dev"
	// Commit is set at build time
	Commit = "none"

	// Global flags
	debug   bool
	workDir string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "claucker",
	Short: "Claude Container Orchestration",
	Long: `Claucker wraps the Claude Code agent in secure, reproducible Docker containers.

Core philosophy: "Safe Autonomy" - host system is read-only by default.

Quick start:
  claucker init        # Create claucker.yaml in current directory
  claucker up          # Build and run Claude in a container
  claucker down        # Stop the container

Workspace modes:
  --mode=bind          Live sync with host (default)
  --mode=snapshot      Isolated copy in Docker volume`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Initialize logger with debug flag
		logger.Init(debug)

		// Set working directory
		if workDir == "" {
			var err error
			workDir, err = os.Getwd()
			if err != nil {
				logger.Fatal().Err(err).Msg("failed to get working directory")
			}
		}

		logger.Debug().
			Str("version", Version).
			Str("workdir", workDir).
			Bool("debug", debug).
			Msg("claucker starting")
	},
	Version: Version,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable debug logging")
	rootCmd.PersistentFlags().StringVarP(&workDir, "workdir", "w", "", "Working directory (default: current directory)")

	// Version template
	rootCmd.SetVersionTemplate(fmt.Sprintf("claucker %s (commit: %s)\n", Version, Commit))
}

// GetWorkDir returns the current working directory
func GetWorkDir() string {
	return workDir
}
