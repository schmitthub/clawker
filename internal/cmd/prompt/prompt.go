// Package prompt provides the `clawker prompt` command group: access to
// clawker's managed agent briefing, the clawker-owned document a harness
// image bakes at its manifest's managed_prompt destination. The group
// exists so deployments whose agent reads context from runtime-mounted
// state (where baked files are shadowed) can obtain the briefing host-side.
package prompt

import (
	"github.com/spf13/cobra"

	printcmd "github.com/schmitthub/clawker/internal/cmd/prompt/print"
	"github.com/schmitthub/clawker/internal/cmdutil"
)

// NewCmdPrompt creates the prompt parent command and registers its
// subcommands.
func NewCmdPrompt(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prompt",
		Short: "Access clawker's managed agent briefing",
		Long: `Commands for the managed agent briefing clawker provides to coding agents
running in its containers.

The briefing is clawker-owned and harness-agnostic: it explains the container
environment, the egress firewall, and how the agent should surface blocked
connections. A harness whose agent reads managed context from a fixed image
path declares that path in its manifest (managed_prompt) and gets the file
baked at build time; deployments whose agent discovers context in
runtime-mounted state can print it host-side and place it themselves.`,
		Example: `  # Print the briefing
  clawker prompt print`,
	}

	cmd.AddCommand(printcmd.NewCmdPrint(f, nil))

	return cmd
}
