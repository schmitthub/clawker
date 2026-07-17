package plugin

import (
	"github.com/spf13/cobra"

	plugininstall "github.com/schmitthub/clawker/internal/cmd/plugin/install"
	pluginremove "github.com/schmitthub/clawker/internal/cmd/plugin/remove"
	pluginshow "github.com/schmitthub/clawker/internal/cmd/plugin/show"
	"github.com/schmitthub/clawker/internal/cmdutil"
)

func NewCmdPlugin(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "plugin",
		Aliases: []string{"skill"},
		Short:   "Manage the clawker agent skills plugin",
		Long: `Manage the clawker-support agent skills plugin.

The clawker-support skill gives your coding agent hands-on knowledge of
clawker internals — configuration, Dockerfile generation, firewall rules,
MCP wiring, and troubleshooting. It reads the real config schema and
templates so the advice it gives is always accurate.

The claude harness installs through the Claude CLI marketplace; codex,
opencode, and pi install by copying the plugin's skills into the harness's
native skills directory from the marketplace.`,
		Example: `  # Install the clawker plugin for Claude Code
  clawker plugin install

  # Install for another harness
  clawker plugin install --harness codex

  # Show the manual install commands
  clawker plugin show

  # Remove the clawker plugin
  clawker plugin remove`,
	}

	cmd.AddCommand(plugininstall.NewCmdInstall(f, nil))
	cmd.AddCommand(pluginshow.NewCmdShow(f, nil))
	cmd.AddCommand(pluginremove.NewCmdRemove(f, nil))

	return cmd
}
