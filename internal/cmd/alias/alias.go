// Package alias implements the `clawker alias` command group: user-defined
// command shortcuts stored in settings.yaml, expanded before execution.
//
// Active aliases live in user settings only. The project config's aliases
// key is a dormant sharing vehicle — `alias import` and `alias export` move
// entries between the two; project aliases are never applied automatically.
package alias

import (
	aliasdelete "github.com/schmitthub/clawker/internal/cmd/alias/delete"
	aliasexport "github.com/schmitthub/clawker/internal/cmd/alias/export"
	aliasimport "github.com/schmitthub/clawker/internal/cmd/alias/importcmd"
	aliaslist "github.com/schmitthub/clawker/internal/cmd/alias/list"
	aliasset "github.com/schmitthub/clawker/internal/cmd/alias/set"
	"github.com/schmitthub/clawker/internal/cmd/alias/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdAlias creates the `clawker alias` command group. validCommand
// reports whether a name belongs to a real (non-alias) clawker command;
// the root command wires it after the full command tree is built.
func NewCmdAlias(f *cmdutil.Factory, validCommand shared.ValidCommandFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Manage command aliases",
		Long: `Manage user-defined command aliases.

Aliases are shortcuts expanded before execution: the stored value is
appended to 'clawker' in place of the alias name, and any further
arguments are appended after it. Values may reference positional
arguments as $1..$N.

Active aliases are stored in user settings (settings.yaml) and can also
be edited with 'clawker settings edit'. The project config's aliases key
is a sharing vehicle only: 'clawker alias import' deliberately copies
project aliases into settings, and 'clawker alias export' publishes
settings aliases into the project config. Project aliases are never
applied automatically.`,
		Example: `  # Define an alias
  clawker alias set fable "container run --rm -it --agent fable @ --dangerously-skip-permissions --model \"claude-fable-5\""
  
  clawker alias set wt "container run --rm -it --agent $1 --worktree $2:main @ --dangerously-skip-permissions"

  # List configured aliases
  clawker alias list

  # Import aliases shared in the project config
  clawker alias import`,
	}

	cmd.AddCommand(aliasset.NewCmdSet(f, validCommand, nil))
	cmd.AddCommand(aliaslist.NewCmdList(f, nil))
	cmd.AddCommand(aliasdelete.NewCmdDelete(f, nil))
	cmd.AddCommand(aliasimport.NewCmdImport(f, validCommand, nil))
	cmd.AddCommand(aliasexport.NewCmdExport(f, nil))

	return cmd
}
