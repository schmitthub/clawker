package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestNewStatusBar(t *testing.T) {
	sb := NewStatusBar(80)
	assert.Equal(t, 80, sb.Width())
	assert.Empty(t, sb.Left())
	assert.Empty(t, sb.Center())
	assert.Empty(t, sb.Right())
}

func TestStatusBarModel_SetLeft(t *testing.T) {
	sb := NewStatusBar(80).SetLeft("Left content")
	assert.Equal(t, "Left content", sb.Left())
}

func TestStatusBarModel_SetCenter(t *testing.T) {
	sb := NewStatusBar(80).SetCenter("Center content")
	assert.Equal(t, "Center content", sb.Center())
}

func TestStatusBarModel_SetRight(t *testing.T) {
	sb := NewStatusBar(80).SetRight("Right content")
	assert.Equal(t, "Right content", sb.Right())
}

func TestStatusBarModel_SetWidth(t *testing.T) {
	sb := NewStatusBar(80).SetWidth(120)
	assert.Equal(t, 120, sb.Width())
}

func TestStatusBarModel_View(t *testing.T) {
	tests := []struct {
		name      string
		left      string
		center    string
		right     string
		wantParts []string
	}{
		{
			name:      "all sections",
			left:      "LEFT",
			center:    "CENTER",
			right:     "RIGHT",
			wantParts: []string{"LEFT", "CENTER", "RIGHT"},
		},
		{
			name:      "left only",
			left:      "LEFT",
			center:    "",
			right:     "",
			wantParts: []string{"LEFT"},
		},
		{
			name:      "right only",
			left:      "",
			center:    "",
			right:     "RIGHT",
			wantParts: []string{"RIGHT"},
		},
		{
			name:      "center only",
			left:      "",
			center:    "CENTER",
			right:     "",
			wantParts: []string{"CENTER"},
		},
		{
			name:      "empty",
			left:      "",
			center:    "",
			right:     "",
			wantParts: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := NewStatusBar(80).
				SetLeft(tt.left).
				SetCenter(tt.center).
				SetRight(tt.right)

			view := sb.View()
			for _, part := range tt.wantParts {
				assert.Contains(t, view, part)
			}
		})
	}
}

func TestRenderStatusBar(t *testing.T) {
	result := RenderStatusBar("LEFT", "CENTER", "RIGHT", 80)
	assert.Contains(t, result, "LEFT")
	assert.Contains(t, result, "CENTER")
	assert.Contains(t, result, "RIGHT")
}

func TestRenderStatusBarWithSections(t *testing.T) {
	sections := []StatusBarSection{
		{Content: "Section 1", Render: func(s string) string { return iostreams.SuccessStyle.Render(s) }},
		{Content: "Section 2", Render: func(s string) string { return iostreams.MutedStyle.Render(s) }},
		{Content: "Section 3", Render: func(s string) string { return iostreams.ErrorStyle.Render(s) }},
	}

	result := RenderStatusBarWithSections(sections, 80)
	assert.Contains(t, result, "Section 1")
	assert.Contains(t, result, "Section 2")
	assert.Contains(t, result, "Section 3")
}

func TestRenderStatusBarWithSections_Empty(t *testing.T) {
	result := RenderStatusBarWithSections([]StatusBarSection{}, 80)
	assert.Empty(t, result)
}

func TestRenderStatusBarWithSections_Single(t *testing.T) {
	sections := []StatusBarSection{
		{Content: "Only Section", Render: func(s string) string { return iostreams.SuccessStyle.Render(s) }},
	}

	result := RenderStatusBarWithSections(sections, 80)
	assert.Contains(t, result, "Only Section")
}

func TestModeIndicator(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		active   bool
		wantText string
	}{
		{"active", "insert", true, "INSERT"},
		{"inactive", "normal", false, "NORMAL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ModeIndicator(tt.mode, tt.active)
			// Mode should be uppercase
			assert.Contains(t, result, tt.wantText)
		})
	}
}

func TestConnectionIndicator(t *testing.T) {
	tests := []struct {
		name      string
		connected bool
		want      string
	}{
		{"connected", true, "Connected"},
		{"disconnected", false, "Disconnected"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConnectionIndicator(tt.connected)
			assert.Contains(t, result, tt.want)
		})
	}
}

func TestTimerIndicator(t *testing.T) {
	result := TimerIndicator("Uptime", "01:23:45")
	assert.Contains(t, result, "Uptime")
	assert.Contains(t, result, "01:23:45")
}

func TestCounterIndicator(t *testing.T) {
	result := CounterIndicator("Loop", 5, 10)
	assert.Contains(t, result, "Loop")
}
