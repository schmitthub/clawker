package controlplane

import (
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdControlPlane creates the parent command for control plane break-glass
// management. Day-to-day callers do not need these verbs: `f.AdminClient`
// transparently invokes `cpboot.EnsureRunning` on first use, so the
// first `clawker firewall status` (or any other admin RPC) auto-boots the
// CP. These commands exist for explicit lifecycle control during debugging
// and recovery.
func NewCmdControlPlane(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "controlplane <command>",
		Short: "Break-glass control plane lifecycle",
		Long: `Explicit lifecycle control for the clawker control plane container.

The control plane is normally bootstrapped on demand the first time any
command needs to talk to it (for example, ` + "`clawker firewall status`" + `).
These subcommands exist for debugging, upgrades, and recovery when you
need to observe or manipulate the CP directly.`,
		Example: `  # Start the control plane (idempotent)
  clawker controlplane up

  # Show CP health
  clawker controlplane status

  # Stop the control plane
  clawker controlplane down`,
	}

	cmd.AddCommand(
		NewCmdUp(f, nil),
		NewCmdDown(f, nil),
		NewCmdStatus(f, nil),
		NewCmdAgents(f, nil),
	)

	return cmd
}
