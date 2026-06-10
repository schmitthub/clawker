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
		Example: `  # Delete an alias
  clawker alias delete co`,
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

	if _, ok := cfg.Project().Aliases[opts.Name]; !ok {
		return fmt.Errorf("no alias %q configured", opts.Name)
	}

	defaults, err := shared.DefaultAliases()
	if err != nil {
		return err
	}
	_, isDefault := defaults[opts.Name]

	// Defaults are the immutable base layer — only file entries are
	// deletable. Remove the entry from every file layer that carries it,
	// so a single delete clears the name instead of unmasking the next
	// layer down.
	layers := shared.LayersContaining(cfg, opts.Name)
	if len(layers) == 0 {
		return fmt.Errorf("alias %q is a shipped default and cannot be deleted; override it with 'clawker alias set %s <expansion> --clobber'", opts.Name, opts.Name)
	}
	ios := opts.IOStreams
	for _, path := range layers {
		if err := shared.WriteAliases(ios.Out, path, func(m map[string]string) {
			delete(m, opts.Name)
		}); err != nil {
			return err
		}
	}

	cs := ios.ColorScheme()
	if isDefault {
		fmt.Fprintf(ios.Out, "%s Removed override; shipped default %q restored\n", cs.SuccessIcon(), opts.Name)
	} else {
		fmt.Fprintf(ios.Out, "%s Deleted alias %q\n", cs.SuccessIcon(), opts.Name)
	}
	return nil
}
