// Package harness provides the `clawker harness` command group: register,
// list, and remove harness bundles in the project's clawker.yaml registry.
package harness

import (
	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
)

// NewCmdHarness creates the parent command for harness registry management.
func NewCmdHarness(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "harness <command>",
		Short: "Manage harness bundles",
		Long: `Manage harness bundles in the project's clawker.yaml registry.

A harness bundle packages a coding-agent runtime (its Dockerfile fragment,
egress floor, volumes, seeds, and optionally its own stack definitions).
Registration points a name at a bundle directory on disk; the name then
selects that harness when building and running containers.

Clawker ships built-in harnesses (e.g. claude) that resolve without
registration; a project registration under the same name shadows the shipped
bundle.`,
		Example: `  # Register a harness bundle directory
  clawker harness register ./tools/codex-bundle

  # Register under an explicit name
  clawker harness register ./vendor/codex --name codex

  # List registered and built-in harnesses
  clawker harness list

  # Remove a project registration
  clawker harness remove codex`,
	}

	cmd.AddCommand(
		NewCmdHarnessRegister(f, nil),
		NewCmdHarnessList(f, nil),
		NewCmdHarnessRemove(f, nil),
	)

	return cmd
}
