package iostreams

import "github.com/charmbracelet/lipgloss"

// ─── Named Colors ─────────────────────────────────────────────────
// Canonical color values by X11/CSS name (or nearest recognized name).
// These define the actual colors. They never change.
var (
	ColorBurntOrange = lipgloss.Color("#E8714A") // Warm orange (nearest: X11 Coral)
	ColorDeepSkyBlue = lipgloss.Color("#00BFFF") // Exact X11/CSS: DeepSkyBlue
	ColorEmerald     = lipgloss.Color("#04B575") // Vivid green (nearest: X11 MediumSeaGreen)
	ColorAmber       = lipgloss.Color("#FFCC00") // Warm yellow (nearest: X11 Gold)
	ColorHotPink     = lipgloss.Color("#FF5F87") // Bright pink (nearest: X11 HotPink)
	ColorDimGray     = lipgloss.Color("#626262") // Near X11 DimGray
	ColorOrchid      = lipgloss.Color("#AD58B4") // Purple-pink (nearest: X11 MediumOrchid)
	ColorSkyBlue     = lipgloss.Color("#87CEEB") // Exact X11/CSS: SkyBlue
	ColorCharcoal    = lipgloss.Color("#4A4A4A") // Dark gray
	ColorGold        = lipgloss.Color("#FFD700") // Exact X11/CSS: Gold
	ColorOnyx        = lipgloss.Color("#3C3C3C") // Very dark gray
	ColorSalmon      = lipgloss.Color("#FF6B6B") // Warm pink-red (nearest: X11 Salmon)
	ColorJet         = lipgloss.Color("#1A1A1A") // Near-black
	ColorGunmetal    = lipgloss.Color("#2A2A2A") // Dark charcoal
	ColorSilver      = lipgloss.Color("#A0A0A0") // Muted silver (nearest: X11 DarkGray)
)

// ─── Semantic Theme ───────────────────────────────────────────────
// Intent-based aliases. Swap the RHS to change the entire color theme.
var (
	ColorPrimary   = ColorBurntOrange // Brand primary
	ColorSecondary = ColorDeepSkyBlue // Brand secondary
	ColorSuccess   = ColorEmerald
	ColorWarning   = ColorAmber
	ColorError     = ColorHotPink
	ColorMuted     = ColorDimGray
	ColorHighlight = ColorOrchid
	ColorInfo      = ColorSkyBlue
	ColorDisabled  = ColorCharcoal
	ColorSelected  = ColorGold
	ColorBorder    = ColorOnyx
	ColorAccent    = ColorSalmon
	ColorBg        = ColorJet
	ColorBgAlt     = ColorGunmetal
	ColorSubtle    = ColorSilver
)

// Text styles — common text formatting.
var (
	TitleStyle     = lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)
	SubtitleStyle  = lipgloss.NewStyle().Foreground(ColorSecondary)
	ErrorStyle     = lipgloss.NewStyle().Foreground(ColorError)
	SuccessStyle   = lipgloss.NewStyle().Foreground(ColorSuccess)
	WarningStyle   = lipgloss.NewStyle().Foreground(ColorWarning)
	MutedStyle     = lipgloss.NewStyle().Foreground(ColorMuted)
	HighlightStyle = lipgloss.NewStyle().Foreground(ColorHighlight)
	AccentStyle    = lipgloss.NewStyle().Foreground(ColorAccent)
	DisabledStyle  = lipgloss.NewStyle().Foreground(ColorDisabled)
)

// Concrete color styles — pure foreground color, no decorations.
// Used by ColorScheme concrete color methods (Red, Blue, etc.).
var (
	BlueStyle = lipgloss.NewStyle().Foreground(ColorDeepSkyBlue)
	CyanStyle = lipgloss.NewStyle().Foreground(ColorInfo)
)

// Border styles.
var (
	BorderStyle       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	BorderActiveStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorPrimary)
	BorderMutedStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorMuted)
)

// Header styles.
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

// Panel styles.
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
			Foreground(ColorSecondary).
			Padding(0, 1)
)

// List styles.
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

// Help bar styles.
var (
	HelpKeyStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)

	HelpDescStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	HelpSeparatorStyle = lipgloss.NewStyle().
				Foreground(ColorBorder)
)

// Label-value pair styles.
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

// Status indicator styles.
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

// Badge styles.
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

// Table styles.
var (
	// TableHeaderStyle for table column headers: subtle silver foreground, no bold.
	// Headers are rendered as uppercase by the table renderer for visual distinction.
	TableHeaderStyle = lipgloss.NewStyle().Foreground(ColorSubtle)

	// TablePrimaryColumnStyle for the first column: primary brand color for emphasis.
	TablePrimaryColumnStyle = lipgloss.NewStyle().Foreground(ColorPrimary)
)

// RenderFixedWidth renders text at a fixed width using lipgloss.
// Used by tui.TablePrinter to set column widths without importing lipgloss directly.
func RenderFixedWidth(s string, width int) string {
	return lipgloss.NewStyle().Width(width).Render(s)
}

// DividerStyle for horizontal rules.
var DividerStyle = lipgloss.NewStyle().
	Foreground(ColorBorder)

// EmptyStateStyle for empty state messages.
var EmptyStateStyle = lipgloss.NewStyle().
	Foreground(ColorMuted).
	Italic(true).
	Align(lipgloss.Center)

// StatusBarStyle for status bar backgrounds.
var StatusBarStyle = lipgloss.NewStyle().
	Background(ColorBgAlt).
	Foreground(lipgloss.Color("#FFFFFF")).
	Padding(0, 1)

// TagStyle for bordered tag elements.
var TagStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(ColorPrimary).
	Padding(0, 1)

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
