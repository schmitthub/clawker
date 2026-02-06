package tui

import (
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
)

func TestColorReExports(t *testing.T) {
	// Verify colors are the same object as iostreams canonical colors.
	assert.Equal(t, iostreams.ColorPrimary, ColorPrimary)
	assert.Equal(t, iostreams.ColorSecondary, ColorSecondary)
	assert.Equal(t, iostreams.ColorSuccess, ColorSuccess)
	assert.Equal(t, iostreams.ColorWarning, ColorWarning)
	assert.Equal(t, iostreams.ColorError, ColorError)
	assert.Equal(t, iostreams.ColorMuted, ColorMuted)
	assert.Equal(t, iostreams.ColorHighlight, ColorHighlight)
	assert.Equal(t, iostreams.ColorInfo, ColorInfo)
	assert.Equal(t, iostreams.ColorDisabled, ColorDisabled)
	assert.Equal(t, iostreams.ColorSelected, ColorSelected)
	assert.Equal(t, iostreams.ColorBorder, ColorBorder)
	assert.Equal(t, iostreams.ColorAccent, ColorAccent)
	assert.Equal(t, iostreams.ColorBg, ColorBg)
	assert.Equal(t, iostreams.ColorBgAlt, ColorBgAlt)
}

func TestStyleReExports(t *testing.T) {
	// All re-exported styles should render text without panicking.
	// We call .Render() (variadic) directly — the test verifies re-exports work.
	tests := []struct {
		name   string
		render string
	}{
		{"TitleStyle", TitleStyle.Render("test")},
		{"SubtitleStyle", SubtitleStyle.Render("test")},
		{"ErrorStyle", ErrorStyle.Render("test")},
		{"SuccessStyle", SuccessStyle.Render("test")},
		{"WarningStyle", WarningStyle.Render("test")},
		{"MutedStyle", MutedStyle.Render("test")},
		{"HighlightStyle", HighlightStyle.Render("test")},
		{"BorderStyle", BorderStyle.Render("test")},
		{"PanelStyle", PanelStyle.Render("test")},
		{"PanelActiveStyle", PanelActiveStyle.Render("test")},
		{"BadgeStyle", BadgeStyle.Render("test")},
		{"BadgeMutedStyle", BadgeMutedStyle.Render("test")},
		{"EmptyStateStyle", EmptyStateStyle.Render("test")},
		{"StatusBarStyle", StatusBarStyle.Render("test")},
		{"TagStyle", TagStyle.Render("test")},
		{"ListItemStyle", ListItemStyle.Render("test")},
		{"ListItemSelectedStyle", ListItemSelectedStyle.Render("test")},
		{"HelpKeyStyle", HelpKeyStyle.Render("test")},
		{"HelpDescStyle", HelpDescStyle.Render("test")},
		{"StatusRunningStyle", StatusRunningStyle.Render("test")},
		{"StatusErrorStyle", StatusErrorStyle.Render("test")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Contains(t, tt.render, "test")
		})
	}
}

func TestStatusStyleReExport(t *testing.T) {
	renderFn := StatusStyle(true)
	assert.Contains(t, renderFn("test"), "test")

	renderFn = StatusStyle(false)
	assert.Contains(t, renderFn("test"), "test")
}

func TestStatusTextReExport(t *testing.T) {
	assert.Contains(t, StatusText(true), "RUNNING")
	assert.Contains(t, StatusText(false), "STOPPED")
}

func TestStatusIndicatorReExport(t *testing.T) {
	rendered, symbol := StatusIndicator("running")
	assert.Equal(t, "\u25cf", symbol) // ●
	assert.Contains(t, rendered, symbol)

	rendered, symbol = StatusIndicator("error")
	assert.Equal(t, "\u2717", symbol) // ✗
	assert.Contains(t, rendered, symbol)
}

func TestTokenConstants(t *testing.T) {
	assert.Equal(t, 0, SpaceNone)
	assert.Equal(t, 1, SpaceXS)
	assert.Equal(t, 2, SpaceSM)
	assert.Equal(t, 4, SpaceMD)
	assert.Equal(t, 8, SpaceLG)

	assert.Equal(t, 60, WidthCompact)
	assert.Equal(t, 80, WidthNormal)
	assert.Equal(t, 120, WidthWide)
}

func TestLayoutModeReExport(t *testing.T) {
	assert.Equal(t, LayoutCompact, GetLayoutMode(50))
	assert.Equal(t, LayoutNormal, GetLayoutMode(80))
	assert.Equal(t, LayoutWide, GetLayoutMode(120))
}

func TestUtilityFunctionReExports(t *testing.T) {
	assert.Equal(t, 3, MinInt(3, 5))
	assert.Equal(t, 5, MaxInt(3, 5))
	assert.Equal(t, 5, ClampInt(3, 5, 10))
	assert.Equal(t, 38, GetContentWidth(44, 2))
	assert.Equal(t, 15, GetContentHeight(20, 3, 2))
}

func TestTextFunctionReExports(t *testing.T) {
	assert.Equal(t, "hel...", Truncate("hello world", 6))
	assert.Equal(t, "hello  ", PadRight("hello", 7))
	assert.Equal(t, "  hello", PadLeft("hello", 7))
	assert.Equal(t, " hello ", PadCenter("hello", 7))
	assert.Equal(t, 5, CountVisibleWidth("hello"))
	assert.Equal(t, "hello", StripANSI("hello"))
	assert.Equal(t, "  hello", Indent("hello", 2))
	assert.Equal(t, "a, b", JoinNonEmpty(", ", "a", "", "b"))
	assert.Equal(t, "aaa", Repeat("a", 3))
	assert.Equal(t, "first", FirstLine("first\nsecond"))
	assert.Equal(t, 2, LineCount("a\nb"))
}

func TestLayoutFunctionReExports(t *testing.T) {
	left, right := SplitHorizontal(80, DefaultSplitConfig())
	assert.Greater(t, left, 0)
	assert.Greater(t, right, 0)

	result := Stack(1, "a", "b")
	assert.Contains(t, result, "a")
	assert.Contains(t, result, "b")

	result = Row(1, "a", "b")
	assert.Contains(t, result, "a")
	assert.Contains(t, result, "b")

	result = FlexRow(80, "LEFT", "", "RIGHT")
	assert.Contains(t, result, "LEFT")
	assert.Contains(t, result, "RIGHT")
}

func TestTimeFunctionReExports(t *testing.T) {
	result := FormatDuration(2*time.Hour + 30*time.Minute)
	assert.Equal(t, "2h 30m", result)

	result = FormatDate(time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC))
	assert.Equal(t, "Jan 15, 2024", result)

	result = FormatUptime(0)
	assert.Equal(t, "0s", result)
}
