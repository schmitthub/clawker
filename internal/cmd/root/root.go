package root

import (
	aliascmd "github.com/schmitthub/clawker/internal/cmd/alias"
	authcmd "github.com/schmitthub/clawker/internal/cmd/auth"
	bridgecmd "github.com/schmitthub/clawker/internal/cmd/bridge"
	"github.com/schmitthub/clawker/internal/cmd/container"
	controlplanecmd "github.com/schmitthub/clawker/internal/cmd/controlplane"
	firewallcmd "github.com/schmitthub/clawker/internal/cmd/firewall"
	"github.com/schmitthub/clawker/internal/cmd/generate"
	hostproxycmd "github.com/schmitthub/clawker/internal/cmd/hostproxy"
	"github.com/schmitthub/clawker/internal/cmd/image"
	initcmd "github.com/schmitthub/clawker/internal/cmd/init"
	"github.com/schmitthub/clawker/internal/cmd/monitor"
	"github.com/schmitthub/clawker/internal/cmd/network"
	"github.com/schmitthub/clawker/internal/cmd/project"
	"github.com/schmitthub/clawker/internal/cmd/settings"
	"github.com/schmitthub/clawker/internal/cmd/skill"
	versioncmd "github.com/schmitthub/clawker/internal/cmd/version"
	"github.com/schmitthub/clawker/internal/cmd/volume"
	"github.com/schmitthub/clawker/internal/cmd/worktree"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdRoot creates the root command for the clawker CLI.
func NewCmdRoot(f *cmdutil.Factory, version, buildDate string) (*cobra.Command, error) {
	var debug bool

	cmd := &cobra.Command{
		Use:   "clawker",
		Short: "Run coding agents in secure Docker containers with clawker",
		Long: `Clawker wraps coding agent harnesses (Claude Code and others) in safe, reproducible, monitored, isolated Docker containers.

Quick start:
  clawker init           # Initialize project in current directory
  clawker build          # Build the container image
  clawker run            # Start the agent in a container
  clawker stop           # Stop the container

Workspace modes:
  --mode=bind          Live sync with host (default)
  --mode=snapshot      Isolated copy in Docker volume`,
		SilenceUsage: true,
		Annotations: map[string]string{
			"versionInfo": versioncmd.Format(version, buildDate),
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
		Version: f.Version,
	}

	// Global flags
	cmd.PersistentFlags().BoolVarP(&debug, "debug", "D", false, "Enable debug logging")

	// Silence Cobra's default error and usage output — we handle this in Main. It's obnoxious
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	// Register built-in top-level aliases (shortcuts to subcommands)
	registerBuiltinAliases(cmd, f)

	// Add non-alias top-level commands
	cmd.AddCommand(initcmd.NewCmdInit(f, nil))
	cmd.AddCommand(project.NewCmdProject(f))
	cmd.AddCommand(settings.NewCmdSettings(f))
	cmd.AddCommand(skill.NewCmdSkill(f))
	cmd.AddCommand(monitor.NewCmdMonitor(f))
	cmd.AddCommand(generate.NewCmdGenerate(f, nil))

	// Add management commands
	cmd.AddCommand(aliascmd.NewCmdAlias(f, func(name string) bool { return builtinCommandExists(cmd, name) }))
	cmd.AddCommand(authcmd.NewCmdAuth(f))
	cmd.AddCommand(container.NewCmdContainer(f))
	cmd.AddCommand(controlplanecmd.NewCmdControlPlane(f))
	cmd.AddCommand(firewallcmd.NewCmdFirewall(f))
	cmd.AddCommand(image.NewCmdImage(f))
	cmd.AddCommand(volume.NewCmdVolume(f))
	cmd.AddCommand(network.NewCmdNetwork(f))
	cmd.AddCommand(worktree.NewCmdWorktree(f))

	// Add hidden internal commands
	cmd.AddCommand(hostproxycmd.NewCmdHostProxy())
	cmd.AddCommand(bridgecmd.NewCmdBridge())

	// Add version subcommand
	cmd.AddCommand(versioncmd.NewCmdVersion(f, version, buildDate))

	// Register user-configured aliases last — existing commands win collisions
	registerUserAliases(cmd, f)

	return cmd, nil
}
