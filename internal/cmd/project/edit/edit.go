package edit

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	projectui "github.com/schmitthub/clawker/internal/config/storeui/project"
	"github.com/schmitthub/clawker/internal/iostreams"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/spf13/cobra"
)

// EditOptions holds dependencies for the project edit command.
type EditOptions struct {
	IOStreams *iostreams.IOStreams
	Config   func() (config.Config, error)
}

// NewCmdProjectEdit creates the `clawker project edit` command.
func NewCmdProjectEdit(f *cmdutil.Factory, runF func(context.Context, *EditOptions) error) *cobra.Command {
	opts := &EditOptions{
		IOStreams: f.IOStreams,
		Config:   f.Config,
	}

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Interactively edit project configuration",
		Long:  `Opens an interactive TUI for browsing and editing project configuration (clawker.yaml).`,
		Example: `  # Edit project configuration
  clawker project edit`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return editRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func editRun(_ context.Context, opts *EditOptions) error {
	cfg, err := opts.Config()
	if err != nil {
		return err
	}

	store := cfg.ProjectStore()
	result, err := projectui.Edit(opts.IOStreams, store, cfg)
	if err != nil {
		return err
	}

	cs := opts.IOStreams.ColorScheme()
	if result.Saved {
		fmt.Fprintf(opts.IOStreams.Out, "%s Project configuration saved (%d fields modified)\n",
			cs.SuccessIcon(), len(result.Modified))
	} else if result.Cancelled {
		fmt.Fprintf(opts.IOStreams.Out, "%s Edit cancelled\n", cs.InfoIcon())
	}

	return nil
}
