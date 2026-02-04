package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	ralphtui "github.com/schmitthub/clawker/internal/ralph/tui"
)

// TUIOptions holds options for the ralph tui command.
type TUIOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() *config.Config
}

// NewCmdTUI creates the `clawker ralph tui` command.
func NewCmdTUI(f *cmdutil.Factory, runF func(context.Context, *TUIOptions) error) *cobra.Command {
	opts := &TUIOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
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
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return tuiRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func tuiRun(_ context.Context, opts *TUIOptions) error {
	cfg := opts.Config().Project

	if cfg.Project == "" {
		return fmt.Errorf("project name not set in clawker.yaml")
	}

	model := ralphtui.NewModel(cfg.Project)
	p := tea.NewProgram(model, tea.WithAltScreen())

	_, err := p.Run()
	return err
}
