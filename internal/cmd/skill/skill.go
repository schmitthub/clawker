package skill

import (
	"github.com/spf13/cobra"

	skillinstall "github.com/schmitthub/clawker/internal/cmd/skill/install"
	skillremove "github.com/schmitthub/clawker/internal/cmd/skill/remove"
	skillshow "github.com/schmitthub/clawker/internal/cmd/skill/show"
	"github.com/schmitthub/clawker/internal/cmdutil"
)

func NewCmdSkill(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage the clawker agent skills plugin",
		Long: `Manage the clawker-support agent skills plugin.

The clawker-support skill gives your coding agent hands-on knowledge of
clawker internals — configuration, Dockerfile generation, firewall rules,
MCP wiring, and troubleshooting. It reads the real config schema and
templates so the advice it gives is always accurate.

The claude harness installs through the Claude CLI marketplace; codex,
opencode, and pi install by copying the plugin's skills into the harness's
native skills directory from the marketplace.`,
		Example: `  # Install the clawker skill plugin for Claude Code
  clawker skill install

  # Install for another harness
  clawker skill install --harness codex

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
