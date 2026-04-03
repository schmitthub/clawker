package show

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

const (
	marketplaceSource = "schmitthub/claude-plugins"
	pluginName        = "clawker-support@schmitthub-plugins"
)

type ShowOptions struct {
	IOStreams *iostreams.IOStreams
}

func NewCmdShow(f *cmdutil.Factory, runF func(context.Context, *ShowOptions) error) *cobra.Command {
	opts := &ShowOptions{
		IOStreams: f.IOStreams,
	}

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show manual install commands for the clawker skill plugin",
		Long: `Display the Claude CLI commands needed to manually install the
clawker-support skill plugin.`,
		Example: `  clawker skill show`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return showRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func showRun(_ context.Context, opts *ShowOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	fmt.Fprintf(ios.Out, "%s Manual install commands:\n\n", cs.InfoIcon())
	fmt.Fprintf(ios.Out, "  %s\n", cs.Cyan("claude plugin marketplace add "+marketplaceSource))
	fmt.Fprintf(ios.Out, "  %s\n", cs.Cyan("claude plugin install "+pluginName))
	fmt.Fprintln(ios.Out)
	fmt.Fprintf(ios.Out, "%s To remove:\n\n", cs.InfoIcon())
	fmt.Fprintf(ios.Out, "  %s\n", cs.Cyan("claude plugin remove "+pluginName))

	return nil
}
