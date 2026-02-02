package root

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/cmd/config"
	"github.com/schmitthub/clawker/internal/cmd/container"
	"github.com/schmitthub/clawker/internal/cmd/generate"
	"github.com/schmitthub/clawker/internal/cmd/image"
	initcmd "github.com/schmitthub/clawker/internal/cmd/init"
	"github.com/schmitthub/clawker/internal/cmd/monitor"
	"github.com/schmitthub/clawker/internal/cmd/network"
	"github.com/schmitthub/clawker/internal/cmd/project"
	"github.com/schmitthub/clawker/internal/cmd/ralph"
	"github.com/schmitthub/clawker/internal/cmd/volume"
	"github.com/schmitthub/clawker/internal/cmdutil"
	internalconfig "github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/spf13/cobra"
)

// NewCmdRoot creates the root command for the clawker CLI.
func NewCmdRoot(f *cmdutil.Factory) *cobra.Command {
	var debug bool
	var workDirFlag string

	cmd := &cobra.Command{
		Use:   "clawker",
		Short: "Manage Claude Code in secure Docker containers with clawker",
		Long: `Clawker (claude + docker) wraps Claude Code in safe, reproducible, monitored, isolated Docker containers.

Quick start:
  clawker init           # Set up user settings (~/.local/clawker/settings.yaml)
  clawker project init   # Initialize project in current directory (clawker.yaml)
  clawker start          # Build and start Claude Code in a container
  clawker stop           # Stop the container

Workspace modes:
  --mode=bind          Live sync with host (default)
  --mode=snapshot      Isolated copy in Docker volume`,
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Initialize logger with file logging if possible
			initializeLogger(debug)

			logger.Debug().
				Str("version", f.Version).
				Str("workdir", f.WorkDir()).
				Bool("debug", debug).
				Msg("clawker starting")

			return nil
		},
		Version: f.Version,
	}

	// Global flags
	cmd.PersistentFlags().BoolVarP(&debug, "debug", "D", false, "Enable debug logging")
	cmd.PersistentFlags().StringVarP(&workDirFlag, "workdir", "w", "", "Working directory (default: current directory)")

	// Override factory default with flag-aware closure
	origWorkDir := f.WorkDir
	f.WorkDir = func() string {
		if workDirFlag != "" {
			return workDirFlag
		}
		return origWorkDir()
	}

	// Version template
	cmd.SetVersionTemplate(fmt.Sprintf("clawker %s (commit: %s)\n", f.Version, f.Commit))

	// Register top-level aliases (shortcuts to subcommands)
	registerAliases(cmd, f)

	// Add non-alias top-level commands
	cmd.AddCommand(initcmd.NewCmdInit(f, nil))
	cmd.AddCommand(project.NewCmdProject(f))
	cmd.AddCommand(config.NewCmdConfig(f))
	cmd.AddCommand(monitor.NewCmdMonitor(f))
	cmd.AddCommand(generate.NewCmdGenerate(f, nil))
	cmd.AddCommand(ralph.NewCmdRalph(f))

	// Add management commands
	cmd.AddCommand(container.NewCmdContainer(f))
	cmd.AddCommand(image.NewCmdImage(f))
	cmd.AddCommand(volume.NewCmdVolume(f))
	cmd.AddCommand(network.NewCmdNetwork(f))

	return cmd
}

// initializeLogger sets up the logger with file logging if possible.
// Falls back to console-only logging on any errors.
func initializeLogger(debug bool) {
	// Try to load settings for logging config
	loader, err := internalconfig.NewSettingsLoader()
	if err != nil {
		// Fall back to console-only logging
		logger.Init(debug)
		logger.Warn().Err(err).Msg("file logging unavailable: failed to create settings loader")
		return
	}

	settings, err := loader.Load()
	if err != nil {
		// Fall back to console-only logging
		logger.Init(debug)
		logger.Warn().Err(err).Msg("file logging unavailable: failed to load settings")
		return
	}

	// Get logs directory
	logsDir, err := internalconfig.LogsDir()
	if err != nil {
		// Fall back to console-only logging
		logger.Init(debug)
		logger.Warn().Err(err).Msg("file logging unavailable: failed to get logs directory")
		return
	}

	// Convert settings.Logging to logger.LoggingConfig
	logCfg := &logger.LoggingConfig{
		FileEnabled: settings.Logging.FileEnabled,
		MaxSizeMB:   settings.Logging.MaxSizeMB,
		MaxAgeDays:  settings.Logging.MaxAgeDays,
		MaxBackups:  settings.Logging.MaxBackups,
	}

	// Initialize with file logging
	if err := logger.InitWithFile(debug, logsDir, logCfg); err != nil {
		// Fall back to console-only on error
		logger.Init(debug)
		logger.Warn().Err(err).Msg("file logging unavailable: failed to initialize file writer")
	}
}
