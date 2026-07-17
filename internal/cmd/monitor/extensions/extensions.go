// Package extensions provides the `clawker monitor extensions` command.
package extensions

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmdutil"
)

// NewCmdExtensions creates the monitor extensions command.
func NewCmdExtensions(f *cmdutil.Factory, runF func(context.Context, *cmdutil.InventoryOptions) error) *cobra.Command {
	return cmdutil.NewInventoryListCommand(f, runF, cmdutil.InventorySpec{
		Use:     "extensions",
		Aliases: []string{"ext"},
		Short:   "List resolvable monitoring extensions and their provenance",
		Long: `Lists every monitoring extension a project can select in
'monitor.extensions', across all three tiers: the embedded floor, loose
convention directories, and installed bundles.

Each row shows the selectable name (bare, or qualified
namespace.bundle.extension for a bundle extension), the owning bundle's
version where applicable, and the resolution source — a bundle extension names
its bundle so it traces back to a 'clawker bundle list' row. An extension that
shadows a farther tier marks the shadowed sources with '!'.

Selected extensions are seeded onto the stack by 'clawker monitor up' (or
applied to a running stack by 'clawker monitor reload').`,
		Example: `  # List all monitoring extensions
  clawker monitor extensions

  # Machine-readable output
  clawker monitor extensions --json`,
		Type:  bundle.ComponentMonitoring,
		Empty: "No monitoring extensions resolvable.",
	})
}
