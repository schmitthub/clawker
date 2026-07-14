// Package bundle provides the `clawker bundle` command group: the install,
// list, remove, update, and validate verbs for the bundle-distribution model.
// It replaces the register-era stack/harness/monitoring-unit commands — a
// bundle is the single distribution unit, and its components (harnesses,
// stacks, monitoring extensions) are listed together with their resolution
// provenance.
package bundle

import (
	"github.com/spf13/cobra"

	installcmd "github.com/schmitthub/clawker/internal/cmd/bundle/install"
	listcmd "github.com/schmitthub/clawker/internal/cmd/bundle/list"
	prunecmd "github.com/schmitthub/clawker/internal/cmd/bundle/prune"
	removecmd "github.com/schmitthub/clawker/internal/cmd/bundle/remove"
	updatecmd "github.com/schmitthub/clawker/internal/cmd/bundle/update"
	validatecmd "github.com/schmitthub/clawker/internal/cmd/bundle/validate"
	"github.com/schmitthub/clawker/internal/cmdutil"
)

// NewCmdBundle creates the bundle parent command and registers its subcommands.
func NewCmdBundle(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Manage distributed bundles of harnesses, stacks, and monitoring extensions",
		Long: `Manage clawker bundles — distribution units that ship one or more
harnesses, stacks, or monitoring extensions.

A bundle is declared in a clawker.yaml 'bundles:' entry (a git source or a local
path) and its content is fetched into a host-global cache. Bundled components are
addressed by their qualified namespace.bundle.component name; the embedded floor
and loose convention directories provide bare-named components.`,
		Example: `  # List every resolvable component and its provenance
  clawker bundle list

  # Validate a bundle directory before publishing
  clawker bundle validate ./my-bundle

  # Declare and fetch a bundle
  clawker bundle install https://github.com/acme/tools.git --ref v1.2.0`,
	}

	cmd.AddCommand(installcmd.NewCmdInstall(f, nil))
	cmd.AddCommand(listcmd.NewCmdList(f, nil))
	cmd.AddCommand(prunecmd.NewCmdPrune(f, nil))
	cmd.AddCommand(removecmd.NewCmdRemove(f, nil))
	cmd.AddCommand(updatecmd.NewCmdUpdate(f, nil))
	cmd.AddCommand(validatecmd.NewCmdValidate(f, nil))

	return cmd
}
