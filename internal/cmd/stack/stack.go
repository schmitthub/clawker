// Package stack provides the `clawker stack` command group: read-only
// inventory over the stacks the build can select. Stacks are added by
// convention (loose .clawker/stacks/ dirs) or distributed in bundles — there
// are no register/enable verbs; selection lives in `build.stacks`.
package stack

import (
	"github.com/spf13/cobra"

	listcmd "github.com/schmitthub/clawker/internal/cmd/stack/list"
	"github.com/schmitthub/clawker/internal/cmdutil"
)

// NewCmdStack creates the stack parent command and registers its subcommands.
func NewCmdStack(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stack",
		Short: "Inspect resolvable stacks",
		Long: `Commands for inspecting the stacks a build can select.

A stack is a reusable image fragment selected by name in 'build.stacks'. Bare
names resolve from the embedded floor and loose convention directories
(.clawker/stacks/<name>/ in a project, or the same path under the user config
directory); qualified namespace.bundle.stack names resolve from installed
bundles.`,
		Example: `  # List every resolvable stack and where it comes from
  clawker stack list`,
	}

	cmd.AddCommand(listcmd.NewCmdList(f, nil))

	return cmd
}
