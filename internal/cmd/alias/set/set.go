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

Aliases are stored in user settings (settings.yaml). An alias cannot
shadow an existing clawker command. Overwriting an existing alias
requires --clobber.`,
		Example: `  # Shortcut with appended arguments
  clawker alias set co "container run --rm -it"

  # Positional placeholders
  clawker alias set lg "logs $1 --tail $2"

  # Overwrite an existing alias
  clawker alias set dev "run --rm -it --agent dev @" --clobber`,
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

	aliases := cfg.Settings().Aliases
	_, exists := aliases[opts.Name]
	if exists && !opts.Clobber {
		return fmt.Errorf("alias %q already exists; use --clobber to overwrite it", opts.Name)
	}

	if err := shared.ValidateExpansionTarget(opts.Name, opts.Expansion, opts.ValidCommand, aliases); err != nil {
		return err
	}

	store := cfg.SettingsStore()
	if err := store.Set(func(s *config.Settings) {
		if s.Aliases == nil {
			s.Aliases = make(map[string]string)
		}
		s.Aliases[opts.Name] = opts.Expansion
	}); err != nil {
		return fmt.Errorf("updating settings: %w", err)
	}
	if err := store.Write(); err != nil {
		return fmt.Errorf("saving settings: %w", err)
	}

	cs := opts.IOStreams.ColorScheme()
	verb := "Added"
	if exists {
		verb = "Changed"
	}
	fmt.Fprintf(opts.IOStreams.Out, "%s %s alias %q: %s\n", cs.SuccessIcon(), verb, opts.Name, opts.Expansion)
	return nil
}
