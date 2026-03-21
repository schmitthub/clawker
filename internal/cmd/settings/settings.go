package settings

import (
	settingsedit "github.com/schmitthub/clawker/internal/cmd/settings/edit"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

func NewCmdSettings(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Manage clawker user settings",
		Long: `Manage clawker user settings.

User settings are stored in ~/.config/clawker/settings.yaml and control
global behavior like logging, monitoring, and firewall settings.`,
		Example: `  # Interactively edit user settings
  clawker settings edit`,
	}

	cmd.AddCommand(settingsedit.NewCmdSettingsEdit(f, nil))

	return cmd
}
