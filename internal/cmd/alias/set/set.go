package set

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmd/alias/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// SetOptions holds dependencies for the alias set command.
type SetOptions struct {
	IOStreams    *iostreams.IOStreams
	Config       func() (config.Config, error)
	ValidCommand shared.ValidCommandFunc

	Name      string
	Expansion string
	Clobber   bool
}

// NewCmdSet creates the `clawker alias set` command.
func NewCmdSet(f *cmdutil.Factory, validCommand shared.ValidCommandFunc, runF func(context.Context, *SetOptions) error) *cobra.Command {
	opts := &SetOptions{
		IOStreams:    f.IOStreams,
		Config:       f.Config,
		ValidCommand: validCommand,
	}

	cmd := &cobra.Command{
		Use:   "set <alias> <expansion>",
		Short: "Create or update a command alias",
		Long: `Create a shortcut for a clawker command.

The expansion is appended to 'clawker' in place of the alias name; any
extra arguments are appended after it. Use $1..$N in the expansion to
place positional arguments explicitly.

Be cautious with placeholders: most shells expand $1 before clawker
sees it. Escape them ("\$1") or single-quote the expansion.

Overwriting an existing alias requires --clobber.`,
		Example: `  # Shortcut with appended arguments
  clawker alias set fable "container run --rm -it --agent fable @ --dangerously-skip-permissions --model \"claude-fable-5\""

  # Positional placeholders
  clawker alias set wtm "container run --rm -it --agent \$1 --worktree \$2:main @ --dangerously-skip-permissions"

  # Overwrite an existing alias
  clawker alias set go "run --rm -it --agent go @" --clobber`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			opts.Expansion = args[1]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return setRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Clobber, "clobber", false, "Overwrite an existing alias")

	return cmd
}

func setRun(_ context.Context, opts *SetOptions) error {
	if err := shared.ValidateName(opts.Name); err != nil {
		return err
	}
	if opts.ValidCommand != nil && opts.ValidCommand(opts.Name) {
		return fmt.Errorf("alias %q cannot shadow an existing clawker command", opts.Name)
	}

	cfg, err := opts.Config()
	if err != nil {
		return err
	}

	aliases := cfg.Project().Aliases
	_, exists := aliases[opts.Name]
	if exists && !opts.Clobber {
		return fmt.Errorf("alias %q already exists; use --clobber to overwrite it", opts.Name)
	}

	if err := shared.ValidateExpansionTarget(opts.Name, opts.Expansion, opts.ValidCommand, aliases); err != nil {
		return err
	}

	target, err := shared.SetTarget()
	if err != nil {
		return fmt.Errorf("resolving user config file: %w", err)
	}

	ios := opts.IOStreams
	cs := ios.ColorScheme()
	if err := shared.WriteAliases(ios.Out, target, func(m map[string]string) {
		m[opts.Name] = opts.Expansion
	}); err != nil {
		return err
	}
	verb := "Added"
	if exists {
		verb = "Changed"
	}
	fmt.Fprintf(ios.Out, "%s %s alias %q: %s\n", cs.SuccessIcon(), verb, opts.Name, opts.Expansion)

	// Walk-up project files outrank the user config-dir file in the merge —
	// a same-named alias there keeps winning over what was just written.
	winner, ok := cfg.ProjectStore().Provenance(shared.AliasFieldPath(opts.Name))
	if ok && winner.Path != "" && !shared.SamePath(winner.Path, target) {
		fmt.Fprintf(ios.ErrOut, "%s Alias %q is also defined in %s, which takes precedence\n", cs.WarningIcon(), opts.Name, winner.Path)
	}
	return nil
}
