package config

import (
	"github.com/schmitthub/clawker/internal/cmd/config/check"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdConfig creates the config command.
func NewCmdConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration management commands",
		Long:  `Commands for managing and validating clawker configuration.`,
	}

	cmd.AddCommand(check.NewCmdCheck(f, nil))

	return cmd
}
