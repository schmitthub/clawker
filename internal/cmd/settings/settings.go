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

User settings control global behavior like logging, monitoring, and
firewall configuration. The settings file location depends on your
config directory (settings.yaml).`,
		Example: `  # Interactively edit user settings
  clawker settings edit`,
	}

	cmd.AddCommand(settingsedit.NewCmdSettingsEdit(f, nil))

	return cmd
}
