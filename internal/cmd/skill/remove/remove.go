package remove

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle/fetch"
	"github.com/schmitthub/clawker/internal/cmd/skill/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

type RemoveOptions struct {
	IOStreams   *iostreams.IOStreams
	Scope       string
	Harness     string
	CheckCLI    func() error
	RunClaude   func(ctx context.Context, ios *iostreams.IOStreams, args ...string) error
	FetchSkills func(ctx context.Context) (*shared.FetchedSkills, error)
	SkillsDir   func(harness string) (string, error)
}

func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams: f.IOStreams,
		CheckCLI:  shared.CheckClaudeCLI,
		RunClaude: shared.RunClaude,
		FetchSkills: func(ctx context.Context) (*shared.FetchedSkills, error) {
			return shared.FetchPluginSkills(ctx, fetch.NewFetcher())
		},
		SkillsDir: shared.SkillsDir,
	}

	cmd := &cobra.Command{
		Use:     "remove",
		Aliases: []string{"uninstall", "rm"},
		Short:   "Remove the clawker agent skills plugin",
		Long: `Remove the clawker-support agent skills plugin.

For the claude harness this uninstalls the plugin through the Claude CLI;
the marketplace registration is left in place. For codex, opencode, and pi
it deletes the plugin's skills from the harness's native skills directory.`,
		Example: `  # Remove from Claude Code (default)
  clawker skill remove

  # Remove from another harness
  clawker skill remove --harness codex

  # Remove from project scope (claude only)
  clawker skill remove --scope project`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return removeRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().
		StringVarP(&opts.Scope, "scope", "s", "user", "Uninstall from scope: user, project, or local (claude only)")
	cmd.Flags().
		StringVar(&opts.Harness, "harness", shared.HarnessClaude, "Target harness: claude, codex, opencode, or pi")

	return cmd
}

func removeRun(ctx context.Context, opts *RemoveOptions) error {
	//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
	if err := shared.ValidateHarness(opts.Harness); err != nil {
		return err
	}
	if opts.Harness == shared.HarnessClaude {
		return removeClaude(ctx, opts)
	}
	return removeByCopy(ctx, opts)
}

func removeClaude(ctx context.Context, opts *RemoveOptions) error {
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

// removeByCopy fetches the pinned plugin to enumerate its skills — removal
// has no local receipt to consult — then deletes each from the harness dir.
func removeByCopy(ctx context.Context, opts *RemoveOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	dstDir, err := opts.SkillsDir(opts.Harness)
	if err != nil {
		return err
	}

	fetched, fetchErr := opts.FetchSkills(ctx)
	if fetchErr != nil {
		return fmt.Errorf("resolving plugin skills: %w", fetchErr)
	}
	defer fetched.Cleanup()

	if rmErr := shared.RemoveSkills(dstDir, fetched.Names); rmErr != nil {
		return fmt.Errorf("removing skills for %s: %w", opts.Harness, rmErr)
	}

	for _, name := range fetched.Names {
		fmt.Fprintf(ios.Out, "%s Removed skill %s from %s (%s)\n", cs.SuccessIcon(), name, opts.Harness, dstDir)
	}
	return nil
}
