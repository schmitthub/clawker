package edit

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	settingsui "github.com/schmitthub/clawker/internal/config/storeui/settings"
	"github.com/schmitthub/clawker/internal/iostreams"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/spf13/cobra"
)

// EditOptions holds dependencies for the settings edit command.
type EditOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)
}

// NewCmdSettingsEdit creates the `clawker settings edit` command.
func NewCmdSettingsEdit(f *cmdutil.Factory, runF func(context.Context, *EditOptions) error) *cobra.Command {
	opts := &EditOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Interactively edit user settings",
		Long:  `Opens an interactive TUI for browsing and editing user settings (settings.yaml).`,
		Example: `  # Edit user settings
  clawker settings edit`,
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

	store := cfg.SettingsStore()
	result, err := settingsui.Edit(opts.IOStreams, store)
	if err != nil {
		return err
	}

	cs := opts.IOStreams.ColorScheme()
	if result.Saved {
		fmt.Fprintf(opts.IOStreams.Out, "%s Settings saved (%d fields modified)\n",
			cs.SuccessIcon(), len(result.Modified))
	} else if result.Cancelled {
		fmt.Fprintf(opts.IOStreams.Out, "%s Edit cancelled\n", cs.InfoIcon())
	}

	return nil
}
