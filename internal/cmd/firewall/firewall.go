// Package firewall is the parent command for the `clawker firewall` group.
// Each subcommand lives in its own subpackage; helpers they have in common
// live in the shared subpackage.
package firewall

import (
	"github.com/schmitthub/clawker/internal/cmd/firewall/add"
	"github.com/schmitthub/clawker/internal/cmd/firewall/bypass"
	"github.com/schmitthub/clawker/internal/cmd/firewall/disable"
	"github.com/schmitthub/clawker/internal/cmd/firewall/down"
	"github.com/schmitthub/clawker/internal/cmd/firewall/enable"
	"github.com/schmitthub/clawker/internal/cmd/firewall/list"
	"github.com/schmitthub/clawker/internal/cmd/firewall/prune"
	"github.com/schmitthub/clawker/internal/cmd/firewall/refresh"
	"github.com/schmitthub/clawker/internal/cmd/firewall/reload"
	"github.com/schmitthub/clawker/internal/cmd/firewall/remove"
	"github.com/schmitthub/clawker/internal/cmd/firewall/rotateca"
	"github.com/schmitthub/clawker/internal/cmd/firewall/status"
	"github.com/schmitthub/clawker/internal/cmd/firewall/up"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdFirewall creates the parent command for firewall management.
func NewCmdFirewall(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall <command>",
		Short: "Manage the egress firewall",
		Long: `Manage the Envoy+CoreDNS egress firewall that controls outbound traffic
from agent containers.

The firewall runs as shared infrastructure on the clawker Docker network,
enforcing domain-level egress rules via Envoy (TLS SNI filtering) and
CoreDNS (DNS-level allow/deny).`,
		Example: `  # Show firewall health and status
  clawker firewall status

  # List active egress rules
  clawker firewall list

  # Allow a new domain
  clawker firewall add registry.npmjs.org

  # Remove a domain
  clawker firewall remove registry.npmjs.org

  # Temporarily bypass firewall for an agent
  clawker firewall bypass 30s --agent dev`,
	}

	cmd.AddCommand(
		up.NewCmdUp(f, nil),
		down.NewCmdDown(f, nil),
		status.NewCmdStatus(f, nil),
		list.NewCmdList(f, nil),
		add.NewCmdAdd(f, nil),
		remove.NewCmdRemove(f, nil),
		reload.NewCmdReload(f, nil),
		refresh.NewCmdRefresh(f, nil),
		prune.NewCmdPrune(f, nil),
		disable.NewCmdDisable(f, nil),
		enable.NewCmdEnable(f, nil),
		bypass.NewCmdBypass(f, nil),
		rotateca.NewCmdRotateCA(f, nil),
	)

	return cmd
}
