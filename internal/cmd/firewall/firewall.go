package firewall

import (
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
		NewCmdUp(f, nil),
		NewCmdDown(f, nil),
		NewCmdStatus(f, nil),
		NewCmdList(f, nil),
		NewCmdAdd(f, nil),
		NewCmdRemove(f, nil),
		NewCmdReload(f, nil),
		NewCmdDisable(f, nil),
		NewCmdEnable(f, nil),
		NewCmdBypass(f, nil),
		NewCmdRotateCA(f, nil),
	)

	return cmd
}
