package install

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmd/skill/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

type InstallOptions struct {
	IOStreams *iostreams.IOStreams
	Scope     string
	CheckCLI  func() error
	RunClaude func(ctx context.Context, ios *iostreams.IOStreams, args ...string) error
}

func NewCmdInstall(f *cmdutil.Factory, runF func(context.Context, *InstallOptions) error) *cobra.Command {
	opts := &InstallOptions{
		IOStreams: f.IOStreams,
		CheckCLI:  shared.CheckClaudeCLI,
		RunClaude: shared.RunClaude,
	}

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the clawker skill plugin for Claude Code",
		Long: `Install the clawker-support skill plugin for Claude Code.

This adds the schmitthub/claude-plugins marketplace (if not already present)
and installs the clawker-support plugin. The plugin gives Claude Code
hands-on knowledge of clawker configuration, troubleshooting, and internals.`,
		Example: `  # Install with default user scope
  clawker skill install

  # Install with project scope
  clawker skill install --scope project`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return installRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Scope, "scope", "s", "user", "Installation scope: user, project, or local")

	return cmd
}

func installRun(ctx context.Context, opts *InstallOptions) error {
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
		return fmt.Errorf("marketplace was added, but plugin install failed: %w\n\nRetry with: claude plugin install --scope %s %s",
			err, opts.Scope, shared.PluginName)
	}

	fmt.Fprintf(ios.ErrOut, "%s Clawker skill plugin installed successfully\n", cs.SuccessIcon())
	return nil
}
