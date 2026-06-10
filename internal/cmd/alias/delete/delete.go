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
		Long: `Delete a command alias.

The alias is removed from every config file that defines it, so one
delete clears the name regardless of which layer a value lives in.

Shipped default aliases cannot be removed outright — deleting one
disables it by storing an empty expansion in the user-level config
file, which the alias loader skips.`,
		Example: `  # Delete a user-defined alias
  clawker alias delete co

  # Disable the shipped default
  clawker alias delete go`,
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

	aliases := cfg.Project().Aliases
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

	ios := opts.IOStreams
	cs := ios.ColorScheme()

	var disableTarget string
	if isDefault {
		disableTarget, err = shared.SetTarget()
		if err != nil {
			return fmt.Errorf("resolving user config file: %w", err)
		}
	}

	// Remove the entry from every file layer that carries it, so a single
	// delete clears the name instead of unmasking the next layer down.
	for _, path := range shared.LayersContaining(cfg, opts.Name) {
		if isDefault && shared.SamePath(path, disableTarget) {
			continue // rewritten below with the disabling empty expansion
		}
		if err := shared.WriteAliases(ios.Out, path, func(m map[string]string) {
			delete(m, opts.Name)
		}); err != nil {
			return err
		}
	}

	if isDefault {
		// Union merge keeps defaults-layer keys present, so a default alias
		// is disabled (empty expansion in the user config file) rather than
		// removed.
		if err := shared.WriteAliases(ios.Out, disableTarget, func(m map[string]string) {
			m[opts.Name] = ""
		}); err != nil {
			return err
		}
		fmt.Fprintf(ios.Out, "%s Disabled default alias %q\n", cs.SuccessIcon(), opts.Name)
	} else {
		fmt.Fprintf(ios.Out, "%s Deleted alias %q\n", cs.SuccessIcon(), opts.Name)
	}
	return nil
}
