package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// testRenderer is a minimal DashboardRenderer for testing.
type testRenderer struct {
	events []any
	view   string
}

func (r *testRenderer) ProcessEvent(ev any) {
	r.events = append(r.events, ev)
}

func (r *testRenderer) View(cs *iostreams.ColorScheme, width int) string {
	return r.view
}

func newTestDashboard(ch <-chan any) (dashboardModel, *testRenderer) {
	ios := iostreams.NewTestIOStreams()
	renderer := &testRenderer{view: "  test content\n"}
	cfg := DashboardConfig{HelpText: "q quit  ctrl+c stop"}
	return newDashboardModel(ios.IOStreams, renderer, cfg, ch), renderer
}

func TestDashboard_Init(t *testing.T) {
	ch := make(chan any, 1)
	defer close(ch)

	m, _ := newTestDashboard(ch)
	cmd := m.Init()

	require.NotNil(t, cmd)
}

func TestDashboard_Update_Detach_Q(t *testing.T) {
	ch := make(chan any, 1)
	defer close(ch)

	m, _ := newTestDashboard(ch)
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}
	updated, cmd := m.Update(msg)
	model := updated.(dashboardModel)

	assert.True(t, model.detached)
	assert.False(t, model.interrupted)
	assert.True(t, model.finished)
	require.NotNil(t, cmd)
}

func TestDashboard_Update_Detach_Esc(t *testing.T) {
	ch := make(chan any, 1)
	defer close(ch)

	m, _ := newTestDashboard(ch)
	msg := tea.KeyMsg{Type: tea.KeyEsc}
	updated, cmd := m.Update(msg)
	model := updated.(dashboardModel)

	assert.True(t, model.detached)
	assert.False(t, model.interrupted)
	assert.True(t, model.finished)
	require.NotNil(t, cmd)
}

func TestDashboard_Update_Interrupt_CtrlC(t *testing.T) {
	ch := make(chan any, 1)
	defer close(ch)

	m, _ := newTestDashboard(ch)
	msg := tea.KeyMsg{Type: tea.KeyCtrlC}
	updated, cmd := m.Update(msg)
	model := updated.(dashboardModel)

	assert.True(t, model.interrupted)
	assert.False(t, model.detached)
	assert.True(t, model.finished)
	require.NotNil(t, cmd)
}

func TestDashboard_Update_WindowSize(t *testing.T) {
	ch := make(chan any, 1)
	defer close(ch)

	m, _ := newTestDashboard(ch)
	msg := tea.WindowSizeMsg{Width: 120, Height: 40}
	updated, cmd := m.Update(msg)
	model := updated.(dashboardModel)

	assert.Equal(t, 120, model.width)
	assert.Nil(t, cmd)
}

func TestDashboard_Update_OtherKeys(t *testing.T) {
	ch := make(chan any, 1)
	defer close(ch)

	m, _ := newTestDashboard(ch)
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
	updated, cmd := m.Update(msg)
	model := updated.(dashboardModel)

	assert.False(t, model.interrupted)
	assert.False(t, model.finished)
	assert.Nil(t, cmd)
}

func TestDashboard_Update_Event(t *testing.T) {
	ch := make(chan any, 1)
	defer close(ch)

	m, renderer := newTestDashboard(ch)
	msg := dashEventMsg{ev: "test-event"}
	updated, cmd := m.Update(msg)
	_ = updated.(dashboardModel)

	assert.Len(t, renderer.events, 1)
	assert.Equal(t, "test-event", renderer.events[0])
	require.NotNil(t, cmd)
}

func TestDashboard_Update_ChannelClosed(t *testing.T) {
	ch := make(chan any, 1)
	defer close(ch)

	m, _ := newTestDashboard(ch)
	msg := dashChannelClosedMsg{}
	updated, cmd := m.Update(msg)
	model := updated.(dashboardModel)

	assert.True(t, model.finished)
	require.NotNil(t, cmd) // tea.Quit
}

func TestDashboard_View_IncludesRendererContent(t *testing.T) {
	ch := make(chan any, 1)
	defer close(ch)

	m, _ := newTestDashboard(ch)
	view := m.View()

	assert.Contains(t, view, "test content")
}

func TestDashboard_View_IncludesHelpText(t *testing.T) {
	ch := make(chan any, 1)
	defer close(ch)

	m, _ := newTestDashboard(ch)
	view := m.View()

	assert.Contains(t, view, "q quit")
	assert.Contains(t, view, "ctrl+c stop")
}

func TestDashboard_View_NoHelpText(t *testing.T) {
	ios := iostreams.NewTestIOStreams()
	renderer := &testRenderer{view: "content\n"}
	cfg := DashboardConfig{} // no help text
	ch := make(chan any, 1)
	defer close(ch)

	m := newDashboardModel(ios.IOStreams, renderer, cfg, ch)
	view := m.View()

	assert.Contains(t, view, "content")
	// Should not contain help line markup
	assert.NotContains(t, view, "q quit")
}

func TestDashboard_HighWaterMark(t *testing.T) {
	ch := make(chan any, 1)
	defer close(ch)

	m, renderer := newTestDashboard(ch)
	m.width = 80

	// Render with short content
	renderer.view = "line1\n"
	view1 := m.View()
	lines1 := strings.Count(view1, "\n")

	// Render with longer content
	renderer.view = "line1\nline2\nline3\nline4\nline5\n"
	view2 := m.View()
	lines2 := strings.Count(view2, "\n")

	// Second view should be >= first (high-water mark)
	assert.GreaterOrEqual(t, lines2, lines1)
	assert.Greater(t, *m.highWater, 0)
}

func TestDashboard_EventForwarding(t *testing.T) {
	ch := make(chan any, 1)
	defer close(ch)

	m, renderer := newTestDashboard(ch)

	// Send multiple events
	m.Update(dashEventMsg{ev: "event-1"})
	m.Update(dashEventMsg{ev: "event-2"})
	m.Update(dashEventMsg{ev: 42})

	assert.Len(t, renderer.events, 3)
	assert.Equal(t, "event-1", renderer.events[0])
	assert.Equal(t, "event-2", renderer.events[1])
	assert.Equal(t, 42, renderer.events[2])
}
