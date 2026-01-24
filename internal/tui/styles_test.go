package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatusStyle(t *testing.T) {
	tests := []struct {
		name    string
		running bool
	}{
		{"running", true},
		{"stopped", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			style := StatusStyle(tt.running)
			// Verify style can render text
			result := style.Render("test")
			assert.Contains(t, result, "test")
		})
	}
}

func TestStatusText(t *testing.T) {
	tests := []struct {
		name     string
		running  bool
		contains string
	}{
		{"running", true, "RUNNING"},
		{"stopped", false, "STOPPED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := StatusText(tt.running)
			assert.Contains(t, text, tt.contains)
		})
	}
}

func TestStylesAreDefined(t *testing.T) {
	// Verify all styles are defined and usable
	tests := []struct {
		name   string
		render func() string
	}{
		{"TitleStyle", func() string { return TitleStyle.Render("test") }},
		{"SubtitleStyle", func() string { return SubtitleStyle.Render("test") }},
		{"ErrorStyle", func() string { return ErrorStyle.Render("test") }},
		{"SuccessStyle", func() string { return SuccessStyle.Render("test") }},
		{"WarningStyle", func() string { return WarningStyle.Render("test") }},
		{"MutedStyle", func() string { return MutedStyle.Render("test") }},
		{"HighlightStyle", func() string { return HighlightStyle.Render("test") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.render()
			// Styled text should contain the original text
			assert.Contains(t, result, "test")
		})
	}
}

func TestColorsAreDefined(t *testing.T) {
	// Verify all colors are defined
	colors := []struct {
		name  string
		color string
	}{
		{"ColorPrimary", string(ColorPrimary)},
		{"ColorSecondary", string(ColorSecondary)},
		{"ColorSuccess", string(ColorSuccess)},
		{"ColorWarning", string(ColorWarning)},
		{"ColorError", string(ColorError)},
		{"ColorMuted", string(ColorMuted)},
		{"ColorHighlight", string(ColorHighlight)},
		{"ColorInfo", string(ColorInfo)},
		{"ColorDisabled", string(ColorDisabled)},
		{"ColorSelected", string(ColorSelected)},
		{"ColorBorder", string(ColorBorder)},
		{"ColorAccent", string(ColorAccent)},
		{"ColorBg", string(ColorBg)},
		{"ColorBgAlt", string(ColorBgAlt)},
	}

	for _, tt := range colors {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotEmpty(t, tt.color)
		})
	}
}

func TestComponentStylesAreDefined(t *testing.T) {
	// Verify component styles can render
	tests := []struct {
		name   string
		render func() string
	}{
		{"HeaderStyle", func() string { return HeaderStyle.Render("test") }},
		{"HeaderTitleStyle", func() string { return HeaderTitleStyle.Render("test") }},
		{"HeaderSubtitleStyle", func() string { return HeaderSubtitleStyle.Render("test") }},
		{"PanelStyle", func() string { return PanelStyle.Render("test") }},
		{"PanelActiveStyle", func() string { return PanelActiveStyle.Render("test") }},
		{"PanelTitleStyle", func() string { return PanelTitleStyle.Render("test") }},
		{"ListItemStyle", func() string { return ListItemStyle.Render("test") }},
		{"ListItemSelectedStyle", func() string { return ListItemSelectedStyle.Render("test") }},
		{"ListItemDimStyle", func() string { return ListItemDimStyle.Render("test") }},
		{"HelpKeyStyle", func() string { return HelpKeyStyle.Render("test") }},
		{"HelpDescStyle", func() string { return HelpDescStyle.Render("test") }},
		{"HelpSeparatorStyle", func() string { return HelpSeparatorStyle.Render("test") }},
		{"LabelStyle", func() string { return LabelStyle.Render("test") }},
		{"ValueStyle", func() string { return ValueStyle.Render("test") }},
		{"CountStyle", func() string { return CountStyle.Render("test") }},
		{"StatusRunningStyle", func() string { return StatusRunningStyle.Render("test") }},
		{"StatusStoppedStyle", func() string { return StatusStoppedStyle.Render("test") }},
		{"StatusErrorStyle", func() string { return StatusErrorStyle.Render("test") }},
		{"StatusWarningStyle", func() string { return StatusWarningStyle.Render("test") }},
		{"StatusInfoStyle", func() string { return StatusInfoStyle.Render("test") }},
		{"BadgeStyle", func() string { return BadgeStyle.Render("test") }},
		{"BadgeSuccessStyle", func() string { return BadgeSuccessStyle.Render("test") }},
		{"BadgeWarningStyle", func() string { return BadgeWarningStyle.Render("test") }},
		{"BadgeErrorStyle", func() string { return BadgeErrorStyle.Render("test") }},
		{"BadgeMutedStyle", func() string { return BadgeMutedStyle.Render("test") }},
		{"DividerStyle", func() string { return DividerStyle.Render("test") }},
		{"EmptyStateStyle", func() string { return EmptyStateStyle.Render("test") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.render()
			assert.Contains(t, result, "test")
		})
	}
}

func TestStatusIndicator(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		wantSymbol string
	}{
		{"running", "running", "\u25cf"},
		{"stopped", "stopped", "\u25cb"},
		{"exited", "exited", "\u25cb"},
		{"error", "error", "\u2717"},
		{"failed", "failed", "\u2717"},
		{"warning", "warning", "\u26a0"},
		{"pending", "pending", "\u25cb"},
		{"waiting", "waiting", "\u25cb"},
		{"unknown", "unknown", "\u25cb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			style, symbol := StatusIndicator(tt.status)
			assert.Equal(t, tt.wantSymbol, symbol)
			// Verify style can render
			result := style.Render("test")
			assert.Contains(t, result, "test")
		})
	}
}
