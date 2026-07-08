package harness

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

// ListOptions holds the inputs for `clawker harness list`.
type ListOptions struct {
	IOStreams *iostreams.IOStreams
	TUI       *tui.TUI
	Config    func() (config.Config, error)
	Format    *cmdutil.FormatFlags
}

// NewCmdHarnessList creates the `clawker harness list` command.
func NewCmdHarnessList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams: f.IOStreams,
		TUI:       f.TUI,
		Config:    f.Config,
		Format:    nil, // set below via AddFormatFlags
	}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List registered and built-in harnesses",
		Long: `Lists every harness available in the current project: project-registered
harness bundles from clawker.yaml and the built-in harnesses shipped with
clawker.

A project registration that reuses a shipped harness's name shadows it — the
SHADOWS column flags that. A harnesses.<name> entry that only carries
per-harness init config (no path) is not a registration and is not listed.`,
		Example: `  # List harnesses
  clawker harness list

  # Names only
  clawker harness list -q

  # JSON output
  clawker harness list --json`,
		Args: cmdutil.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return listRun(cmd.Context(), opts)
		},
	}

	opts.Format = cmdutil.AddFormatFlags(cmd)
	cmd.Flags().Lookup("quiet").Usage = "Only display harness names"

	return cmd
}

func listRun(_ context.Context, opts *ListOptions) error {
	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	rows := cmdutil.MergeRegistryRows(bundler.ShippedHarnessNames(), registeredHarnesses(cfg))
	if err = cmdutil.RenderRegistryRows(
		opts.IOStreams,
		opts.TUI,
		opts.Format,
		rows,
		"No harnesses found.",
	); err != nil {
		return fmt.Errorf("rendering harness list: %w", err)
	}
	return nil
}

// registeredHarnesses extracts the project harness registry as a name→path
// map. Only entries carrying a path are registrations; an init-config-only
// entry (no path) is not listed.
func registeredHarnesses(cfg config.Config) map[string]string {
	out := map[string]string{}
	for name, entry := range cfg.Project().Harnesses {
		if entry.Path != "" {
			out[name] = entry.Path
		}
	}
	return out
}
