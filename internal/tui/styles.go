// Package tui provides shared TUI components for clawker CLI.
package tui

import "github.com/charmbracelet/lipgloss"

// Color palette - consistent across all TUI features
var (
	ColorPrimary   = lipgloss.Color("#7D56F4")
	ColorSecondary = lipgloss.Color("#6C6C6C")
	ColorSuccess   = lipgloss.Color("#04B575")
	ColorWarning   = lipgloss.Color("#FFCC00")
	ColorError     = lipgloss.Color("#FF5F87")
	ColorMuted     = lipgloss.Color("#626262")
	ColorHighlight = lipgloss.Color("#AD58B4")
)

// Additional colors for components
var (
	ColorInfo     = lipgloss.Color("#87CEEB") // Light sky blue for info
	ColorDisabled = lipgloss.Color("#4A4A4A") // Dark gray for disabled
	ColorSelected = lipgloss.Color("#FFD700") // Gold for selection
	ColorBorder   = lipgloss.Color("#3C3C3C") // Subtle border color
	ColorAccent   = lipgloss.Color("#FF6B6B") // Accent for emphasis
	ColorBg       = lipgloss.Color("#1A1A1A") // Dark background
	ColorBgAlt    = lipgloss.Color("#2A2A2A") // Alternate background
)

// Common text styles
var (
	TitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)
	SubtitleStyle = lipgloss.NewStyle().Foreground(ColorSecondary)
	ErrorStyle    = lipgloss.NewStyle().Foreground(ColorError)
	SuccessStyle  = lipgloss.NewStyle().Foreground(ColorSuccess)
	WarningStyle  = lipgloss.NewStyle().Foreground(ColorWarning)
	MutedStyle    = lipgloss.NewStyle().Foreground(ColorMuted)
	HighlightStyle = lipgloss.NewStyle().Foreground(ColorHighlight)
)

// Border styles
var (
	BorderStyle        = lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	BorderActiveStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorPrimary)
	BorderMutedStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorMuted)
)

// StatusStyle returns a style appropriate for running/stopped status.
func StatusStyle(running bool) lipgloss.Style {
	if running {
		return SuccessStyle
	}
	return MutedStyle
}

// StatusText returns display text for running/stopped status.
func StatusText(running bool) string {
	if running {
		return SuccessStyle.Render("RUNNING")
	}
	return MutedStyle.Render("STOPPED")
}

// Component styles - used by TUI components

// Header styles
var (
	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary).
			Padding(0, 1)

	HeaderTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF"))

	HeaderSubtitleStyle = lipgloss.NewStyle().
				Foreground(ColorSecondary).
				Italic(true)
)

// Panel styles
var (
	PanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	PanelActiveStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary).
				Padding(0, 1)

	PanelTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary).
			Padding(0, 1)
)

// List styles
var (
	ListItemStyle = lipgloss.NewStyle().
			Padding(0, 1)

	ListItemSelectedStyle = lipgloss.NewStyle().
				Foreground(ColorSelected).
				Bold(true).
				Padding(0, 1)

	ListItemDimStyle = lipgloss.NewStyle().
				Foreground(ColorMuted).
				Padding(0, 1)
)

// Help bar styles
var (
	HelpKeyStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)

	HelpDescStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	HelpSeparatorStyle = lipgloss.NewStyle().
				Foreground(ColorBorder)
)

// Label-value pair styles
var (
	LabelStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Width(12)

	ValueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF"))

	CountStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)
)

// Status indicator styles
var (
	StatusRunningStyle = lipgloss.NewStyle().
				Foreground(ColorSuccess).
				Bold(true)

	StatusStoppedStyle = lipgloss.NewStyle().
				Foreground(ColorMuted)

	StatusErrorStyle = lipgloss.NewStyle().
				Foreground(ColorError).
				Bold(true)

	StatusWarningStyle = lipgloss.NewStyle().
				Foreground(ColorWarning)

	StatusInfoStyle = lipgloss.NewStyle().
			Foreground(ColorInfo)
)

// Badge styles
var (
	BadgeStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Background(ColorPrimary).
			Foreground(lipgloss.Color("#FFFFFF"))

	BadgeSuccessStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(ColorSuccess).
				Foreground(lipgloss.Color("#FFFFFF"))

	BadgeWarningStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(ColorWarning).
				Foreground(lipgloss.Color("#000000"))

	BadgeErrorStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Background(ColorError).
			Foreground(lipgloss.Color("#FFFFFF"))

	BadgeMutedStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Background(ColorMuted).
			Foreground(lipgloss.Color("#FFFFFF"))
)

// Divider style
var DividerStyle = lipgloss.NewStyle().
	Foreground(ColorBorder)

// Empty state style
var EmptyStateStyle = lipgloss.NewStyle().
	Foreground(ColorMuted).
	Italic(true).
	Align(lipgloss.Center)

// StatusIndicator returns the appropriate style and symbol for a status.
func StatusIndicator(status string) (lipgloss.Style, string) {
	switch status {
	case "running":
		return StatusRunningStyle, "\u25cf" // ●
	case "stopped", "exited":
		return StatusStoppedStyle, "\u25cb" // ○
	case "error", "failed":
		return StatusErrorStyle, "\u2717" // ✗
	case "warning":
		return StatusWarningStyle, "\u26a0" // ⚠
	case "pending", "waiting":
		return StatusInfoStyle, "\u25cb" // ○
	default:
		return MutedStyle, "\u25cb" // ○
	}
}
