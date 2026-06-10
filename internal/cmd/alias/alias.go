// Package alias implements the `clawker alias` command group: user-defined
// command shortcuts stored in the project config, expanded before execution.
//
// Active aliases are the merged aliases key across every project config
// layer: walk-up files, the user config-dir clawker.yaml (base file layer),
// and shipped defaults. `alias set` writes the user config-dir file,
// `alias export` publishes into the most local walk-up file, and
// `alias delete` removes an entry from every file that defines it.
package alias

import (
	aliasdelete "github.com/schmitthub/clawker/internal/cmd/alias/delete"
	aliasexport "github.com/schmitthub/clawker/internal/cmd/alias/export"
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

Aliases live in the project config's aliases key and merge across all
layers: project files discovered in the walk-up override the user-level
clawker.yaml in the config directory, which overrides shipped defaults.
'clawker alias set' writes the user-level file; 'clawker alias export'
publishes aliases into the project's own config file so they are
version-controlled with the repo.`,
		Example: `  # Define an alias
  clawker alias set fable "container run --rm -it --agent fable @ --dangerously-skip-permissions --model \"claude-fable-5\""

  clawker alias set wt "container run --rm -it --agent $1 --worktree $2:main @ --dangerously-skip-permissions"

  # List configured aliases
  clawker alias list

  # Share aliases with the team via the project config
  clawker alias export`,
	}

	cmd.AddCommand(aliasset.NewCmdSet(f, validCommand, nil))
	cmd.AddCommand(aliaslist.NewCmdList(f, nil))
	cmd.AddCommand(aliasdelete.NewCmdDelete(f, nil))
	cmd.AddCommand(aliasexport.NewCmdExport(f, nil))

	return cmd
}
