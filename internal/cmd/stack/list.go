package stack

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// ListOptions holds the inputs for `clawker stack list`.
type ListOptions struct {
	IOStreams *iostreams.IOStreams
	TUI       *tui.TUI
	Config    func() (config.Config, error)
	Format    *cmdutil.FormatFlags
}

// NewCmdStackList creates the `clawker stack list` command.
func NewCmdStackList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams: f.IOStreams,
		TUI:       f.TUI,
		Config:    f.Config,
		Format:    nil, // set below via AddFormatFlags
	}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List registered and built-in stacks",
		Long: `Lists every stack available in the current project: project-registered
stacks from clawker.yaml and the built-in stacks shipped with clawker.

A project registration that reuses a shipped stack's name shadows it — the
SHADOWS column flags that.`,
		Example: `  # List stacks
  clawker stack list

  # Names only
  clawker stack list -q

  # JSON output
  clawker stack list --json`,
		Args: cmdutil.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return listRun(cmd.Context(), opts)
		},
	}

	opts.Format = cmdutil.AddFormatFlags(cmd)
	cmd.Flags().Lookup("quiet").Usage = "Only display stack names"

	return cmd
}

func listRun(_ context.Context, opts *ListOptions) error {
	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	rows := cmdutil.MergeRegistryRows(bundler.ShippedStackNames(), registeredStacks(cfg))
	if err = cmdutil.RenderRegistryRows(opts.IOStreams, opts.TUI, opts.Format, rows, "No stacks found."); err != nil {
		return fmt.Errorf("rendering stack list: %w", err)
	}
	return nil
}

// registeredStacks extracts the project stack registry as a name→path map.
func registeredStacks(cfg config.Config) map[string]string {
	out := map[string]string{}
	for name, entry := range cfg.Project().Stacks {
		out[name] = entry.Path
	}
	return out
}
