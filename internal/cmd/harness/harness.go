// Package harness provides the `clawker harness` command group: read-only
// inventory over the harnesses a build can target. Harnesses are added by
// convention (loose .clawker/harnesses/ dirs) or distributed in bundles —
// there are no register/enable verbs; the harness is picked at build time
// (`clawker build -t <harness>`) and at run time (the `@:<harness>` selector).
package harness

import (
	"github.com/spf13/cobra"

	listcmd "github.com/schmitthub/clawker/internal/cmd/harness/list"
	"github.com/schmitthub/clawker/internal/cmdutil"
)

// NewCmdHarness creates the harness parent command and registers its
// subcommands.
func NewCmdHarness(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "harness",
		Short: "Inspect resolvable harnesses",
		Long: `Commands for inspecting the coding-agent harnesses a build can target.

A harness is the agent runtime an image is built for, picked at build time with
'clawker build -t <harness>' and at run time with the '@:<harness>' selector.
Bare names resolve from the embedded floor and loose convention directories
(.clawker/harnesses/<name>/ in a project, or the same path under the user
config directory); qualified namespace.bundle.harness names resolve from
installed bundles.`,
		Example: `  # List every resolvable harness and where it comes from
  clawker harness list`,
	}

	cmd.AddCommand(listcmd.NewCmdList(f, nil))

	return cmd
}
