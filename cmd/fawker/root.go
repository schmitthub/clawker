package main

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/cmd/image"
	initcmd "github.com/schmitthub/clawker/internal/cmd/init"
	"github.com/schmitthub/clawker/internal/cmd/network"
	"github.com/schmitthub/clawker/internal/cmd/volume"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// newFawkerRoot builds the fawker command tree — management commands only,
// no top-level aliases. Internal devs don't need convenience shortcuts.
func newFawkerRoot(f *cmdutil.Factory, scenario *string, noPause *bool, step *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fawker",
		Short: "Demo CLI for visual UAT — mirrors clawker with faked deps",
		Long: `Fawker (fake + clawker) mirrors clawker's command tree but runs against
faked dependencies with recorded build scenarios. No Docker required.

Usage:
  fawker init                                  # Interactive init with prompts
  fawker image build                           # Default scenario (multi-stage)
  fawker image build --scenario error          # Error scenario
  fawker image build --progress plain          # Plain mode
  fawker container run -it --agent test @      # Interactive run with init tree
  fawker container run --detach --agent test @ # Detached run with init tree
  fawker container create --agent test @       # Create container
  fawker container ls                          # List fake containers
  fawker image ls                              # List fake images`,
		SilenceUsage: true,
		Version:      f.Version,
	}

	cmd.SetVersionTemplate(fmt.Sprintf("fawker %s (commit: %s)\n", f.Version, f.Commit))

	// Persistent flags — inherited by subcommands.
	cmd.PersistentFlags().StringVar(scenario, "scenario", "multi-stage",
		"Build scenario to use (simple, cached, multi-stage, error, large-log, many-steps, internal-only)")
	cmd.PersistentFlags().BoolVar(noPause, "no-pause", false,
		"Exit immediately without pausing for review")
	cmd.PersistentFlags().BoolVar(step, "step", false,
		"Pause at TUI lifecycle events for step-through review")

	// Management commands — same constructors as clawker, different Factory deps.
	cmd.AddCommand(image.NewCmdImage(f))
	cmd.AddCommand(newFawkerContainerCmd(f))
	cmd.AddCommand(volume.NewCmdVolume(f))
	cmd.AddCommand(network.NewCmdNetwork(f))
	cmd.AddCommand(initcmd.NewCmdInit(f, nil))

	return cmd
}
