// Package list provides the `clawker harness list` command.
package list

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmdutil"
)

// NewCmdList creates the harness list command.
func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *cmdutil.InventoryOptions) error) *cobra.Command {
	return cmdutil.NewInventoryListCommand(f, runF, cmdutil.InventorySpec{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List resolvable harnesses and their provenance",
		Long: `Lists every harness a build can target with 'clawker build -t <harness>',
across all three tiers: the embedded floor, loose convention directories, and
installed bundles.

Each row shows the selectable name (bare, or qualified
namespace.bundle.harness for a bundle harness), the owning bundle's version
where applicable, and the resolution source — a bundle harness names its
bundle so it traces back to a 'clawker bundle list' row. A harness that
shadows a farther tier marks the shadowed sources with '!'.`,
		Example: `  # List all harnesses
  clawker harness list

  # Machine-readable output
  clawker harness list --json`,
		Type:  bundle.ComponentHarness,
		Empty: "No harnesses resolvable.",
	})
}
