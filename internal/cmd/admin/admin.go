// Package admin provides the admin command suite and its subcommands.
package admin

import (
	"github.com/schmitthub/clawker/internal/cmd/admin/grant"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdAdmin creates the admin command suite.
// This is a parent command that groups administrative subcommands.
func NewCmdAdmin(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Administrative commands",
		Long:  `Administrative commands for managing clawker on this host.`,
		Example: `  # Grant the host permissions clawker needs
  sudo clawker admin grant`,
		// No RunE - this is a parent command
	}

	// Add subcommands
	cmd.AddCommand(grant.NewCmdGrant(f, nil))

	return cmd
}
