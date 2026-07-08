package stack

import (
	"context"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// RemoveOptions holds the inputs for `clawker stack remove`.
type RemoveOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)

	Name string // positional arg
}

// NewCmdStackRemove creates the `clawker stack remove` command.
func NewCmdStackRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Name:      "",
	}

	cmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a stack registration",
		Long: `Removes a stack registration from the project's clawker.yaml.

Only project registrations can be removed. Built-in (shipped) stacks cannot be
removed — they can only be shadowed by registering your own definition under
the same name.`,
		Example: `  # Remove a registration
  clawker stack remove my-rust`,
		Args: cmdutil.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return removeRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func removeRun(_ context.Context, opts *RemoveOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if _, registered := cfg.Project().Stacks[opts.Name]; !registered {
		if isShippedStack(opts.Name) {
			return fmt.Errorf(
				"stack %q is a built-in stack and cannot be removed — register your own definition under the same name to shadow it",
				opts.Name,
			)
		}
		return fmt.Errorf("stack %q is not registered — run 'clawker stack list' to see registered stacks", opts.Name)
	}

	store := cfg.ProjectStore()
	removed, err := store.Remove(entryKey(opts.Name))
	if err != nil {
		return fmt.Errorf("removing stack registration: %w", err)
	}
	if !removed {
		return fmt.Errorf(
			"stack %q registration was not found in a writable config layer — it may be inherited from a parent or user-level clawker.yaml",
			opts.Name,
		)
	}
	if err = store.Write(); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Fprintf(ios.Out, "%s Removed stack registration '%s'\n", cs.SuccessIcon(), opts.Name)
	return nil
}

// isShippedStack reports whether name is a built-in stack.
func isShippedStack(name string) bool {
	return slices.Contains(bundler.ShippedStackNames(), name)
}
