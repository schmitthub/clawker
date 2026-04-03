package remove

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmd/skill/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

type RemoveOptions struct {
	IOStreams *iostreams.IOStreams
	Scope     string
	CheckCLI  func() error
	RunClaude func(ctx context.Context, ios *iostreams.IOStreams, args ...string) error
}

func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams: f.IOStreams,
		CheckCLI:  shared.CheckClaudeCLI,
		RunClaude: shared.RunClaude,
	}

	cmd := &cobra.Command{
		Use:     "remove",
		Aliases: []string{"uninstall", "rm"},
		Short:   "Remove the clawker skill plugin from Claude Code",
		Long: `Remove the clawker-support skill plugin from Claude Code.

This uninstalls the plugin from the specified scope. The marketplace
registration is left in place.`,
		Example: `  # Remove with default user scope
  clawker skill remove

  # Remove from project scope
  clawker skill remove --scope project`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return removeRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Scope, "scope", "s", "user", "Uninstall from scope: user, project, or local")

	return cmd
}

func removeRun(ctx context.Context, opts *RemoveOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	if err := shared.ValidateScope(opts.Scope); err != nil {
		return err
	}

	if err := opts.CheckCLI(); err != nil {
		return err
	}

	fmt.Fprintf(ios.ErrOut, "%s Removing %s (scope: %s)...\n", cs.InfoIcon(), shared.PluginName, opts.Scope)

	if err := opts.RunClaude(ctx, ios, "plugin", "remove", "--scope", opts.Scope, shared.PluginName); err != nil {
		return fmt.Errorf("removing plugin: %w", err)
	}

	fmt.Fprintf(ios.ErrOut, "%s Clawker skill plugin removed successfully\n", cs.SuccessIcon())
	return nil
}
