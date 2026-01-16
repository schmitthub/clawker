package root

import (
	"fmt"
	"os"

	internalconfig "github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/pkg/cmd/build"
	"github.com/schmitthub/clawker/pkg/cmd/config"
	"github.com/schmitthub/clawker/pkg/cmd/container"
	"github.com/schmitthub/clawker/pkg/cmd/generate"
	"github.com/schmitthub/clawker/pkg/cmd/image"
	initcmd "github.com/schmitthub/clawker/pkg/cmd/init"
	"github.com/schmitthub/clawker/pkg/cmd/list"
	"github.com/schmitthub/clawker/pkg/cmd/logs"
	"github.com/schmitthub/clawker/pkg/cmd/monitor"
	"github.com/schmitthub/clawker/pkg/cmd/network"
	"github.com/schmitthub/clawker/pkg/cmd/prune"
	"github.com/schmitthub/clawker/pkg/cmd/remove"
	"github.com/schmitthub/clawker/pkg/cmd/restart"
	"github.com/schmitthub/clawker/pkg/cmd/run"
	"github.com/schmitthub/clawker/pkg/cmd/start"
	"github.com/schmitthub/clawker/pkg/cmd/stop"
	"github.com/schmitthub/clawker/pkg/cmd/volume"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
	"github.com/spf13/cobra"
)

// NewCmdRoot creates the root command for the clawker CLI.
func NewCmdRoot(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clawker",
		Short: "Manage Claude Code in secure Docker containers with clawker",
		Long: `Clawker (claude + docker) wraps Claude Code in safe, reproducible, monitored, isolated Docker containers.

Quick start:
  clawker init        # Create clawker.yaml in current directory
  clawker start       # Build and use Claude Code seamlessly in a container
  clawker stop        # Stop the container

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

			// Set build output directory to CLAWKER_HOME/build
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
				Msg("clawker starting")

			return nil
		},
		Version: f.Version,
	}

	// Global flags bound to Factory
	cmd.PersistentFlags().BoolVarP(&f.Debug, "debug", "d", false, "Enable debug logging")
	cmd.PersistentFlags().StringVarP(&f.WorkDir, "workdir", "w", "", "Working directory (default: current directory)")

	// Version template
	cmd.SetVersionTemplate(fmt.Sprintf("clawker %s (commit: %s)\n", f.Version, f.Commit))

	// Add top-level commands (shortcuts)
	cmd.AddCommand(initcmd.NewCmdInit(f))
	cmd.AddCommand(build.NewCmdBuild(f))
	cmd.AddCommand(run.NewCmdRun(f))   // Alias for "container run"
	cmd.AddCommand(start.NewCmdStart(f)) // Alias for "container start"
	cmd.AddCommand(stop.NewCmdStop(f))
	cmd.AddCommand(restart.NewCmdRestart(f))
	cmd.AddCommand(logs.NewCmdLogs(f))
	cmd.AddCommand(list.NewCmdList(f))
	cmd.AddCommand(remove.NewCmdRemove(f))
	cmd.AddCommand(config.NewCmdConfig(f))
	cmd.AddCommand(monitor.NewCmdMonitor(f))
	cmd.AddCommand(prune.NewCmdPrune(f))
	cmd.AddCommand(generate.NewCmdGenerate(f))

	// Add management commands
	cmd.AddCommand(container.NewCmdContainer(f))
	cmd.AddCommand(image.NewCmdImage(f))
	cmd.AddCommand(volume.NewCmdVolume(f))
	cmd.AddCommand(network.NewCmdNetwork(f))

	return cmd
}
