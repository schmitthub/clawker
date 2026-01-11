package root

import (
	"fmt"
	"os"

	internalconfig "github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/pkg/cmd/build"
	"github.com/schmitthub/claucker/pkg/cmd/config"
	"github.com/schmitthub/claucker/pkg/cmd/generate"
	initcmd "github.com/schmitthub/claucker/pkg/cmd/init"
	"github.com/schmitthub/claucker/pkg/cmd/logs"
	"github.com/schmitthub/claucker/pkg/cmd/ls"
	"github.com/schmitthub/claucker/pkg/cmd/monitor"
	"github.com/schmitthub/claucker/pkg/cmd/prune"
	"github.com/schmitthub/claucker/pkg/cmd/restart"
	"github.com/schmitthub/claucker/pkg/cmd/rm"
	"github.com/schmitthub/claucker/pkg/cmd/run"
	"github.com/schmitthub/claucker/pkg/cmd/sh"
	"github.com/schmitthub/claucker/pkg/cmd/start"
	"github.com/schmitthub/claucker/pkg/cmd/stop"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

// NewCmdRoot creates the root command for the claucker CLI.
func NewCmdRoot(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claucker",
		Short: "Manage Claude Code in secure Docker containers with claucker",
		Long: `Claucker (claude + docker) wraps Claude Code in safe, reproducible, monitored, isolated Docker containers.

Quick start:
  claucker init        # Create claucker.yaml in current directory
  claucker start       # Build and use Claude Code seamlessly in a container
  claucker stop        # Stop the container

Workspace modes:
  --mode=bind          Live sync with host (default)
  --mode=snapshot      Isolated copy in Docker volume`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Initialize logger with debug flag
			logger.Init(f.Debug)

			// Set working directory
			if f.WorkDir == "" {
				var err error
				f.WorkDir, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("failed to get working directory: %w", err)
				}
			}

			// Set build output directory to CLAUCKER_HOME/build
			if f.BuildOutputDir == "" {
				var err error
				f.BuildOutputDir, err = internalconfig.BuildDir()
				if err != nil {
					return fmt.Errorf("failed to determine build directory: %w", err)
				}
			}

			logger.Debug().
				Str("version", f.Version).
				Str("workdir", f.WorkDir).
				Str("build-output-dir", f.BuildOutputDir).
				Bool("debug", f.Debug).
				Msg("claucker starting")

			return nil
		},
		Version: f.Version,
	}

	// Global flags bound to Factory
	cmd.PersistentFlags().BoolVarP(&f.Debug, "debug", "d", false, "Enable debug logging")
	cmd.PersistentFlags().StringVarP(&f.WorkDir, "workdir", "w", "", "Working directory (default: current directory)")

	// Version template
	cmd.SetVersionTemplate(fmt.Sprintf("claucker %s (commit: %s)\n", f.Version, f.Commit))

	// Add subcommands
	cmd.AddCommand(initcmd.NewCmdInit(f))
	cmd.AddCommand(build.NewCmdBuild(f))
	cmd.AddCommand(start.NewCmdStart(f))
	cmd.AddCommand(run.NewCmdRun(f))
	cmd.AddCommand(stop.NewCmdStop(f))
	cmd.AddCommand(restart.NewCmdRestart(f))
	cmd.AddCommand(sh.NewCmdSh(f))
	cmd.AddCommand(logs.NewCmdLogs(f))
	cmd.AddCommand(ls.NewCmdLs(f))
	cmd.AddCommand(rm.NewCmdRm(f))
	cmd.AddCommand(config.NewCmdConfig(f))
	cmd.AddCommand(monitor.NewCmdMonitor(f))
	cmd.AddCommand(prune.NewCmdPrune(f))
	cmd.AddCommand(generate.NewCmdGenerate(f))

	return cmd
}
