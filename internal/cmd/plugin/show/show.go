package show

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmd/plugin/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

type ShowOptions struct {
	IOStreams *iostreams.IOStreams
	Harness   string
}

func NewCmdShow(f *cmdutil.Factory, runF func(context.Context, *ShowOptions) error) *cobra.Command {
	opts := &ShowOptions{
		IOStreams: f.IOStreams,
	}

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show manual install commands for the clawker plugin",
		Long: `Display the commands needed to manually install the
clawker-support skill plugin for a harness.`,
		Example: `  # Claude Code (default)
  clawker plugin show

  # Another harness
  clawker plugin show --harness opencode`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return showRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().
		StringVar(&opts.Harness, "harness", shared.HarnessClaude, "Target harness: claude, codex, opencode, or pi")

	return cmd
}

func showRun(_ context.Context, opts *ShowOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
	if err := shared.ValidateHarness(opts.Harness); err != nil {
		return err
	}

	if opts.Harness == shared.HarnessClaude {
		fmt.Fprintf(ios.ErrOut, "%s Manual install commands:\n\n", cs.InfoIcon())
		fmt.Fprintf(ios.Out, "  %s\n", cs.Cyan("claude plugin marketplace add "+shared.MarketplaceSource))
		fmt.Fprintf(ios.Out, "  %s\n", cs.Cyan("claude plugin install "+shared.PluginName))
		fmt.Fprintln(ios.Out)
		fmt.Fprintf(ios.ErrOut, "%s To remove:\n\n", cs.InfoIcon())
		fmt.Fprintf(ios.Out, "  %s\n", cs.Cyan("claude plugin remove "+shared.PluginName))
		return nil
	}

	dstDir, err := shared.SkillsDir(opts.Harness)
	if err != nil {
		return fmt.Errorf("resolving %s skills dir: %w", opts.Harness, err)
	}

	fmt.Fprintf(
		ios.ErrOut,
		"%s Manual install: copy the plugin's skills into the %s skills directory:\n\n",
		cs.InfoIcon(),
		opts.Harness,
	)
	fmt.Fprintf(ios.Out, "  %s\n", cs.Cyan("git clone --depth 1 https://github.com/schmitthub/clawker-plugin.git"))
	fmt.Fprintf(ios.Out, "  %s\n", cs.Cyan("cp -r clawker-plugin/skills/. "+dstDir+"/"))
	fmt.Fprintln(ios.Out)
	fmt.Fprintf(ios.ErrOut, "%s Or let clawker do it (installs from the marketplace):\n\n", cs.InfoIcon())
	fmt.Fprintf(ios.Out, "  %s\n", cs.Cyan("clawker plugin install --harness "+opts.Harness))

	return nil
}
