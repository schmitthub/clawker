package install

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle/fetch"
	"github.com/schmitthub/clawker/internal/cmd/plugin/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

type InstallOptions struct {
	IOStreams   *iostreams.IOStreams
	Scope       string
	Harness     string
	CheckCLI    func() error
	RunClaude   func(ctx context.Context, ios *iostreams.IOStreams, args ...string) error
	FetchSkills func(ctx context.Context) (*shared.FetchedSkills, error)
	SkillsDir   func(harness string) (string, error)
}

func NewCmdInstall(f *cmdutil.Factory, runF func(context.Context, *InstallOptions) error) *cobra.Command {
	opts := &InstallOptions{
		IOStreams: f.IOStreams,
		CheckCLI:  shared.CheckClaudeCLI,
		RunClaude: shared.RunClaude,
		FetchSkills: func(ctx context.Context) (*shared.FetchedSkills, error) {
			return shared.FetchPluginSkills(ctx, fetch.NewFetcher())
		},
		SkillsDir: shared.SkillsDir,
	}

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the clawker agent skills plugin",
		Long: `Install the clawker-support agent skills plugin.

For the claude harness this adds the schmitthub/clawker-plugin marketplace
(if not already present) and installs the clawker-support plugin through the
Claude CLI. For codex, opencode, and pi it fetches the plugin from the
marketplace and copies its skills into the harness's native skills
directory. The plugin gives your coding agent hands-on knowledge of
clawker configuration, troubleshooting, and internals.`,
		Example: `  # Install for Claude Code (default)
  clawker plugin install

  # Install for another harness
  clawker plugin install --harness codex

  # Install with project scope (claude only)
  clawker plugin install --scope project`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return installRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().
		StringVarP(&opts.Scope, "scope", "s", "user", "Installation scope: user, project, or local (claude only)")
	cmd.Flags().
		StringVar(&opts.Harness, "harness", shared.HarnessClaude, "Target harness: claude, codex, opencode, or pi")

	return cmd
}

func installRun(ctx context.Context, opts *InstallOptions) error {
	//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
	if err := shared.ValidateHarness(opts.Harness); err != nil {
		return err
	}
	if opts.Harness == shared.HarnessClaude {
		return installClaude(ctx, opts)
	}
	return installByCopy(ctx, opts)
}

func installClaude(ctx context.Context, opts *InstallOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	if err := shared.ValidateScope(opts.Scope); err != nil {
		return err
	}

	if err := opts.CheckCLI(); err != nil {
		return err
	}

	// Step 1: Add marketplace
	fmt.Fprintf(ios.ErrOut, "%s Adding marketplace %s...\n", cs.InfoIcon(), shared.MarketplaceSource)
	if err := opts.RunClaude(ctx, ios, "plugin", "marketplace", "add", shared.MarketplaceSource); err != nil {
		return fmt.Errorf("adding marketplace: %w", err)
	}

	// Step 2: Install plugin
	fmt.Fprintf(ios.ErrOut, "%s Installing %s (scope: %s)...\n", cs.InfoIcon(), shared.PluginName, opts.Scope)
	if err := opts.RunClaude(ctx, ios, "plugin", "install", "--scope", opts.Scope, shared.PluginName); err != nil {
		return fmt.Errorf(
			"marketplace was added, but plugin install failed: %w\n\nRetry with: claude plugin install --scope %s %s",
			err,
			opts.Scope,
			shared.PluginName,
		)
	}

	fmt.Fprintf(ios.ErrOut, "%s Plugin %s installed successfully\n", cs.SuccessIcon(), shared.MarketplacePluginName)
	return nil
}

func installByCopy(ctx context.Context, opts *InstallOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	dstDir, err := opts.SkillsDir(opts.Harness)
	if err != nil {
		return err
	}

	fmt.Fprintf(
		ios.ErrOut,
		"%s Fetching %s from the marketplace...\n",
		cs.InfoIcon(),
		shared.MarketplacePluginName,
	)
	fetched, fetchErr := opts.FetchSkills(ctx)
	if fetchErr != nil {
		return fmt.Errorf("fetching plugin skills: %w", fetchErr)
	}
	defer fetched.Cleanup()

	skipped, copyErr := shared.CopySkills(fetched.Dir, dstDir, fetched.Names)
	if copyErr != nil {
		return fmt.Errorf("installing skills for %s: %w", opts.Harness, copyErr)
	}
	if skipped > 0 {
		fmt.Fprintf(ios.ErrOut, "%s %d non-regular file(s) skipped\n", cs.WarningIcon(), skipped)
	}

	for _, name := range fetched.Names {
		fmt.Fprintf(ios.Out, "%s Installed skill %s for %s (%s)\n", cs.SuccessIcon(), name, opts.Harness, dstDir)
	}
	return nil
}
