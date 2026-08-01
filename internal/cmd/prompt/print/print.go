// Package printcmd provides the `clawker prompt print` command.
package printcmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// Options holds the inputs for the prompt print command.
type Options struct {
	IO *iostreams.IOStreams
}

// NewCmdPrint creates the prompt print command.
func NewCmdPrint(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams}

	cmd := &cobra.Command{
		Use:   "print",
		Short: "Print the managed agent briefing to stdout",
		Long: `Prints clawker's managed agent briefing — the document that tells a coding
agent it is running inside a clawker container and how to work with the
egress firewall, credential forwarding, and workspace modes.

Harness images whose agent reads managed context from a fixed image path get
this file baked in at build time. For agents that read context from
runtime-mounted state instead, pipe this output to wherever the agent
discovers it.`,
		Example: `  # Show the briefing
  clawker prompt print

  # Place it where an agent reads bootstrap context from a bind-mounted tree
  clawker prompt print > home/.openclaw/workspace/clawker/AGENTS.md`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(opts)
			}
			return printRun(opts)
		},
	}

	return cmd
}

func printRun(opts *Options) error {
	if _, err := fmt.Fprint(opts.IO.Out, bundler.AgentPromptContent); err != nil {
		return fmt.Errorf("writing briefing: %w", err)
	}
	return nil
}
