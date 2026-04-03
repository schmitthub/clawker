package skill

import (
	skillinstall "github.com/schmitthub/clawker/internal/cmd/skill/install"
	skillremove "github.com/schmitthub/clawker/internal/cmd/skill/remove"
	skillshow "github.com/schmitthub/clawker/internal/cmd/skill/show"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

func NewCmdSkill(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage the clawker Claude Code skill plugin",
		Long: `Manage the clawker-support Claude Code skill plugin.

The clawker-support skill gives Claude Code hands-on knowledge of clawker
internals — configuration, Dockerfile generation, firewall rules, MCP wiring,
and troubleshooting. It reads the real config schema and template so the
advice it gives is always accurate.`,
		Example: `  # Install the clawker skill plugin
  clawker skill install

  # Show the manual install commands
  clawker skill show

  # Remove the clawker skill plugin
  clawker skill remove`,
	}

	cmd.AddCommand(skillinstall.NewCmdInstall(f, nil))
	cmd.AddCommand(skillshow.NewCmdShow(f, nil))
	cmd.AddCommand(skillremove.NewCmdRemove(f, nil))

	return cmd
}
