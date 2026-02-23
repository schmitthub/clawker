// Package remove provides the project remove command.
package remove

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/spf13/cobra"
)

// RemoveOptions contains the options for the project remove command.
type RemoveOptions struct {
	IOStreams      *iostreams.IOStreams
	ProjectManager func() (project.ProjectManager, error)
	Prompter       func() *prompter.Prompter

	Names []string
	Yes   bool
}

// NewCmdRemove creates the project remove command.
func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams:      f.IOStreams,
		ProjectManager: f.ProjectManager,
		Prompter:       f.Prompter,
	}

	cmd := &cobra.Command{
		Use:     "remove NAME [NAME...]",
		Aliases: []string{"rm"},
		Short:   "Remove projects from the registry",
		Long: `Removes one or more projects from the clawker project registry.

This only removes the project's registration — it does not delete any files
from disk. The project directory and clawker.yaml remain untouched.

Use 'clawker project list' to see registered project names.`,
		Example: `  # Remove a project by name
  clawker project remove my-app

  # Remove multiple projects
  clawker project rm my-app another-app

  # Remove without confirmation prompt
  clawker project remove --yes my-app`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Names = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return removeRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Skip confirmation prompt")

	return cmd
}

func removeRun(ctx context.Context, opts *RemoveOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	mgr, err := opts.ProjectManager()
	if err != nil {
		return fmt.Errorf("loading project manager: %w", err)
	}

	// Build a name→root lookup from the registry.
	entries, err := mgr.List(ctx)
	if err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}
	nameToRoot := make(map[string]string, len(entries))
	for _, e := range entries {
		nameToRoot[e.Name] = e.Root
	}

	// Resolve all names to roots first, failing fast on unknown names.
	type target struct {
		name string
		root string
	}
	var targets []target
	for _, name := range opts.Names {
		root, ok := nameToRoot[name]
		if !ok {
			return fmt.Errorf("project %q is not registered", name)
		}
		targets = append(targets, target{name: name, root: root})
	}

	// Confirm unless --yes.
	if !opts.Yes && ios.IsInteractive() {
		p := opts.Prompter()
		msg := fmt.Sprintf("Remove %d project(s) from registry?", len(targets))
		if len(targets) == 1 {
			msg = fmt.Sprintf("Remove project %q from registry?", targets[0].name)
		}
		confirmed, confirmErr := p.Confirm(msg, false)
		if confirmErr != nil {
			return confirmErr
		}
		if !confirmed {
			return cmdutil.ErrAborted
		}
	}

	var errs []error
	for _, t := range targets {
		if err := mgr.Remove(ctx, t.root); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(ios.ErrOut, "%s %s: %v\n", cs.FailureIcon(), t.name, err)
		} else {
			fmt.Fprintf(ios.Out, "%s Removed %s\n", cs.SuccessIcon(), t.name)
		}
	}

	if len(errs) > 0 {
		return cmdutil.SilentError
	}
	return nil
}
