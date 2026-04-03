package remove

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

const pluginName = "clawker-support@schmitthub-plugins"

type RemoveOptions struct {
	IOStreams *iostreams.IOStreams
	Scope     string
}

func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams: f.IOStreams,
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

	if err := validateScope(opts.Scope); err != nil {
		return err
	}

	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("claude CLI not found in PATH — install it from https://docs.anthropic.com/en/docs/claude-code")
	}

	fmt.Fprintf(ios.ErrOut, "%s Removing %s (scope: %s)...\n", cs.InfoIcon(), pluginName, opts.Scope)

	cmd := exec.CommandContext(ctx, "claude", "plugin", "remove", "--scope", opts.Scope, pluginName)
	cmd.Stdout = ios.Out
	cmd.Stderr = ios.ErrOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("removing plugin: %w", err)
	}

	fmt.Fprintf(ios.ErrOut, "%s Clawker skill plugin removed successfully\n", cs.SuccessIcon())
	return nil
}

func validateScope(scope string) error {
	switch scope {
	case "user", "project", "local":
		return nil
	default:
		return cmdutil.FlagErrorf("--scope must be user, project, or local; got %q", scope)
	}
}
