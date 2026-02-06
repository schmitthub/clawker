package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// ProgramOption configures a BubbleTea program.
type ProgramOption func(*programOptions)

type programOptions struct {
	altScreen   bool
	mouseMotion bool
}

func defaultProgramOptions() programOptions {
	return programOptions{}
}

// WithAltScreen enables or disables the alternate screen buffer.
func WithAltScreen(enabled bool) ProgramOption {
	return func(o *programOptions) {
		o.altScreen = enabled
	}
}

// WithMouseMotion enables or disables mouse motion events.
func WithMouseMotion(enabled bool) ProgramOption {
	return func(o *programOptions) {
		o.mouseMotion = enabled
	}
}

// RunProgram creates and runs a BubbleTea program with the given IOStreams.
// It returns the final model state after the program exits.
func RunProgram(ios *iostreams.IOStreams, model tea.Model, opts ...ProgramOption) (tea.Model, error) {
	cfg := defaultProgramOptions()
	for _, opt := range opts {
		opt(&cfg)
	}

	teaOpts := []tea.ProgramOption{
		tea.WithInput(ios.In),
		tea.WithOutput(ios.ErrOut),
	}

	if cfg.altScreen {
		teaOpts = append(teaOpts, tea.WithAltScreen())
	}

	if cfg.mouseMotion {
		teaOpts = append(teaOpts, tea.WithMouseAllMotion())
	}

	p := tea.NewProgram(model, teaOpts...)
	finalModel, err := p.Run()
	return finalModel, err
}
