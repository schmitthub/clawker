package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
)

// testModel is a minimal BubbleTea model for testing RunProgram.
type testModel struct {
	initialized bool
	quitted     bool
}

func (m testModel) Init() tea.Cmd {
	return tea.Quit
}

func (m testModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.QuitMsg:
		m.quitted = true
	}
	return m, nil
}

func (m testModel) View() string {
	return "test view"
}

func TestRunProgram(t *testing.T) {
	tio := iostreams.NewTestIOStreams()

	model := testModel{}
	result, err := RunProgram(tio.IOStreams, model)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRunProgram_WithAltScreen(t *testing.T) {
	tio := iostreams.NewTestIOStreams()

	model := testModel{}
	result, err := RunProgram(tio.IOStreams, model, WithAltScreen(true))
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRunProgram_WithMouseMotion(t *testing.T) {
	tio := iostreams.NewTestIOStreams()

	model := testModel{}
	result, err := RunProgram(tio.IOStreams, model, WithMouseMotion(true))
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRunProgram_MultipleOptions(t *testing.T) {
	tio := iostreams.NewTestIOStreams()

	model := testModel{}
	result, err := RunProgram(tio.IOStreams, model,
		WithAltScreen(true),
		WithMouseMotion(true),
	)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestProgramOptions(t *testing.T) {
	opts := defaultProgramOptions()
	assert.False(t, opts.altScreen)
	assert.False(t, opts.mouseMotion)

	WithAltScreen(true)(&opts)
	assert.True(t, opts.altScreen)

	WithMouseMotion(true)(&opts)
	assert.True(t, opts.mouseMotion)
}
