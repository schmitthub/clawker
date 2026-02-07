package tui

import (
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// SpinnerType defines the animation style for a spinner.
type SpinnerType int

const (
	SpinnerDots SpinnerType = iota
	SpinnerLine
	SpinnerMiniDots
	SpinnerJump
	SpinnerPulse
	SpinnerPoints
	SpinnerGlobe
	SpinnerMoon
	SpinnerMonkey
)

// SpinnerModel is a wrapper around bubbles/spinner with clawker styling.
type SpinnerModel struct {
	spinner spinner.Model
	label   string
}

// NewSpinner creates a new spinner with the specified type and label.
func NewSpinner(spinnerType SpinnerType, label string) SpinnerModel {
	s := spinner.New()
	s.Spinner = mapSpinnerType(spinnerType)
	s.Style = iostreams.CyanStyle

	return SpinnerModel{
		spinner: s,
		label:   label,
	}
}

// NewDefaultSpinner creates a spinner with default settings.
func NewDefaultSpinner(label string) SpinnerModel {
	return NewSpinner(SpinnerDots, label)
}

// mapSpinnerType converts our spinner type to bubbles spinner type.
func mapSpinnerType(t SpinnerType) spinner.Spinner {
	switch t {
	case SpinnerDots:
		return spinner.Dot
	case SpinnerLine:
		return spinner.Line
	case SpinnerMiniDots:
		return spinner.MiniDot
	case SpinnerJump:
		return spinner.Jump
	case SpinnerPulse:
		return spinner.Pulse
	case SpinnerPoints:
		return spinner.Points
	case SpinnerGlobe:
		return spinner.Globe
	case SpinnerMoon:
		return spinner.Moon
	case SpinnerMonkey:
		return spinner.Monkey
	default:
		return spinner.Dot
	}
}

// Init initializes the spinner.
func (m SpinnerModel) Init() tea.Cmd {
	return m.spinner.Tick
}

// Update handles messages for the spinner.
func (m SpinnerModel) Update(msg tea.Msg) (SpinnerModel, tea.Cmd) {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

// View renders the spinner.
func (m SpinnerModel) View() string {
	if m.label == "" {
		return m.spinner.View()
	}
	return m.spinner.View() + " " + iostreams.MutedStyle.Render(m.label)
}

// SetLabel updates the spinner's label.
func (m SpinnerModel) SetLabel(label string) SpinnerModel {
	m.label = label
	return m
}

// SetSpinnerType changes the spinner animation type.
func (m SpinnerModel) SetSpinnerType(t SpinnerType) SpinnerModel {
	m.spinner.Spinner = mapSpinnerType(t)
	return m
}

// Tick returns a command to tick the spinner.
func (m SpinnerModel) Tick() tea.Msg {
	return m.spinner.Tick()
}

// SpinnerTickMsg is sent when the spinner should update.
type SpinnerTickMsg = spinner.TickMsg
