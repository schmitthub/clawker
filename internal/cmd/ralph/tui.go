package ralph

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	ralphtui "github.com/schmitthub/clawker/internal/ralph/tui"
)

type tuiOptions struct {
	IOStreams *iostreams.IOStreams
	Config   func() (*config.Config, error)
}

// NewCmdTUI creates the `clawker ralph tui` command.
func NewCmdTUI(f *cmdutil.Factory) *cobra.Command {
	opts := &tuiOptions{
		IOStreams: f.IOStreams,
		Config:   f.Config,
	}

	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Launch interactive TUI dashboard",
		Long: `Launch an interactive terminal dashboard for monitoring ralph agents.

The TUI provides a real-time view of all ralph agents in the current project,
including their status, loop progress, and recent log output.

Features:
  - Live agent discovery and status updates
  - Log streaming from active agents
  - Quick actions (stop, reset circuit breaker)
  - Session history and statistics`,
		Example: `  # Launch TUI for current project
  clawker ralph tui`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(opts)
		},
	}

	return cmd
}

func runTUI(opts *tuiOptions) error {
	ios := opts.IOStreams

	cfg, err := opts.Config()
	if err != nil {
		if config.IsConfigNotFound(err) {
			cmdutil.PrintError(ios, "No clawker.yaml found in current directory")
			cmdutil.PrintNextSteps(ios,
				"Run 'clawker project init' to create a configuration",
				"Or change to a directory with clawker.yaml",
			)
		}
		return err
	}

	if cfg.Project == "" {
		return fmt.Errorf("project name not set in clawker.yaml")
	}

	model := ralphtui.NewModel(cfg.Project)
	p := tea.NewProgram(model, tea.WithAltScreen())

	_, err = p.Run()
	return err
}
