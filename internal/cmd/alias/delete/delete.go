package delete

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmd/alias/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// DeleteOptions holds dependencies for the alias delete command.
type DeleteOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)

	Name string
}

// NewCmdDelete creates the `clawker alias delete` command.
func NewCmdDelete(f *cmdutil.Factory, runF func(context.Context, *DeleteOptions) error) *cobra.Command {
	opts := &DeleteOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:     "delete <alias>",
		Aliases: []string{"rm"},
		Short:   "Delete a command alias",
		Long: `Delete a command alias from user settings.

Shipped default aliases cannot be removed outright — deleting one
disables it by storing an empty expansion, which the alias loader
skips.`,
		Example: `  # Delete a user-defined alias
  clawker alias delete co

  # Disable the shipped default
  clawker alias delete dev`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return deleteRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func deleteRun(_ context.Context, opts *DeleteOptions) error {
	cfg, err := opts.Config()
	if err != nil {
		return err
	}

	aliases := cfg.Settings().Aliases
	expansion, ok := aliases[opts.Name]
	if !ok {
		return fmt.Errorf("no alias %q configured", opts.Name)
	}

	defaults, err := shared.DefaultAliases()
	if err != nil {
		return err
	}
	_, isDefault := defaults[opts.Name]

	if isDefault && expansion == "" {
		return fmt.Errorf("default alias %q is already disabled", opts.Name)
	}

	store := cfg.SettingsStore()
	if err := store.Set(func(s *config.Settings) {
		if isDefault {
			// Union merge keeps defaults-layer keys present, so a default
			// alias is disabled (empty expansion) rather than removed.
			if s.Aliases == nil {
				s.Aliases = make(map[string]string)
			}
			s.Aliases[opts.Name] = ""
			return
		}
		delete(s.Aliases, opts.Name)
	}); err != nil {
		return fmt.Errorf("updating settings: %w", err)
	}
	if err := store.Write(); err != nil {
		return fmt.Errorf("saving settings: %w", err)
	}

	cs := opts.IOStreams.ColorScheme()
	if isDefault {
		fmt.Fprintf(opts.IOStreams.Out, "%s Disabled default alias %q\n", cs.SuccessIcon(), opts.Name)
	} else {
		fmt.Fprintf(opts.IOStreams.Out, "%s Deleted alias %q\n", cs.SuccessIcon(), opts.Name)
	}
	return nil
}
