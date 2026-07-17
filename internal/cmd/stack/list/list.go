// Package list provides the `clawker stack list` command.
package list

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmdutil"
)

// NewCmdList creates the stack list command.
func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *cmdutil.InventoryOptions) error) *cobra.Command {
	return cmdutil.NewInventoryListCommand(f, runF, cmdutil.InventorySpec{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List resolvable stacks and their provenance",
		Long: `Lists every stack a build can select in 'build.stacks', across all three
tiers: the embedded floor, loose convention directories, and installed bundles.

Each row shows the selectable name (bare, or qualified
namespace.bundle.stack for a bundle stack), the owning bundle's version where
applicable, and the resolution source — a bundle stack names its bundle so it
traces back to a 'clawker bundle list' row. A stack that shadows a
farther tier marks the shadowed sources with '!'.`,
		Example: `  # List all stacks
  clawker stack list

  # Machine-readable output
  clawker stack list --json`,
		Type:  bundle.ComponentStack,
		Empty: "No stacks resolvable.",
	})
}
