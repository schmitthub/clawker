package install

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

const (
	marketplaceSource = "schmitthub/claude-plugins"
	pluginName        = "clawker-support@schmitthub-plugins"
)

type InstallOptions struct {
	IOStreams *iostreams.IOStreams
	Scope     string
}

func NewCmdInstall(f *cmdutil.Factory, runF func(context.Context, *InstallOptions) error) *cobra.Command {
	opts := &InstallOptions{
		IOStreams: f.IOStreams,
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

	if err := validateScope(opts.Scope); err != nil {
		return err
	}

	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("claude CLI not found in PATH — install it from https://docs.anthropic.com/en/docs/claude-code")
	}

	// Step 1: Add marketplace
	fmt.Fprintf(ios.ErrOut, "%s Adding marketplace %s...\n", cs.InfoIcon(), marketplaceSource)
	if err := runClaude(ctx, ios, "plugin", "marketplace", "add", marketplaceSource); err != nil {
		return fmt.Errorf("adding marketplace: %w", err)
	}

	// Step 2: Install plugin
	fmt.Fprintf(ios.ErrOut, "%s Installing %s (scope: %s)...\n", cs.InfoIcon(), pluginName, opts.Scope)
	if err := runClaude(ctx, ios, "plugin", "install", "--scope", opts.Scope, pluginName); err != nil {
		return fmt.Errorf("installing plugin: %w", err)
	}

	fmt.Fprintf(ios.ErrOut, "%s Clawker skill plugin installed successfully\n", cs.SuccessIcon())
	return nil
}

func runClaude(ctx context.Context, ios *iostreams.IOStreams, args ...string) error {
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Stdout = ios.Out
	cmd.Stderr = ios.ErrOut
	return cmd.Run()
}

func validateScope(scope string) error {
	switch scope {
	case "user", "project", "local":
		return nil
	default:
		return cmdutil.FlagErrorf("--scope must be user, project, or local; got %q", scope)
	}
}
