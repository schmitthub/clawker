// Package tui provides shared TUI components for clawker CLI.
//
// All colors, styles, text utilities, layout helpers, and time formatting
// are re-exported from internal/iostreams — the canonical source of truth.
// This file exists so that tui consumers can access the full visual vocabulary
// without importing iostreams directly (keeping the tui API surface stable).
//
// IMPORTANT: This file must NOT import github.com/charmbracelet/lipgloss.
package tui

import (
	"time"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// ---------------------------------------------------------------------------
// Colors — canonical palette from iostreams
// ---------------------------------------------------------------------------

var (
	ColorPrimary   = iostreams.ColorPrimary
	ColorSecondary = iostreams.ColorSecondary
	ColorSuccess   = iostreams.ColorSuccess
	ColorWarning   = iostreams.ColorWarning
	ColorError     = iostreams.ColorError
	ColorMuted     = iostreams.ColorMuted
	ColorHighlight = iostreams.ColorHighlight
	ColorInfo      = iostreams.ColorInfo
	ColorDisabled  = iostreams.ColorDisabled
	ColorSelected  = iostreams.ColorSelected
	ColorBorder    = iostreams.ColorBorder
	ColorAccent    = iostreams.ColorAccent
	ColorBg        = iostreams.ColorBg
	ColorBgAlt     = iostreams.ColorBgAlt
)

// ---------------------------------------------------------------------------
// Text styles
// ---------------------------------------------------------------------------

var (
	TitleStyle     = iostreams.TitleStyle
	SubtitleStyle  = iostreams.SubtitleStyle
	ErrorStyle     = iostreams.ErrorStyle
	SuccessStyle   = iostreams.SuccessStyle
	WarningStyle   = iostreams.WarningStyle
	MutedStyle     = iostreams.MutedStyle
	HighlightStyle = iostreams.HighlightStyle
	AccentStyle    = iostreams.AccentStyle
	DisabledStyle    = iostreams.DisabledStyle
	BlueStyle        = iostreams.BlueStyle
	CyanStyle        = iostreams.CyanStyle
	BrandOrangeStyle = iostreams.BrandOrangeStyle
)

// ---------------------------------------------------------------------------
// Border styles
// ---------------------------------------------------------------------------

var (
	BorderStyle       = iostreams.BorderStyle
	BorderActiveStyle = iostreams.BorderActiveStyle
	BorderMutedStyle  = iostreams.BorderMutedStyle
)

// ---------------------------------------------------------------------------
// Header styles
// ---------------------------------------------------------------------------

var (
	HeaderStyle         = iostreams.HeaderStyle
	HeaderTitleStyle    = iostreams.HeaderTitleStyle
	HeaderSubtitleStyle = iostreams.HeaderSubtitleStyle
)

// ---------------------------------------------------------------------------
// Panel styles
// ---------------------------------------------------------------------------

var (
	PanelStyle       = iostreams.PanelStyle
	PanelActiveStyle = iostreams.PanelActiveStyle
	PanelTitleStyle  = iostreams.PanelTitleStyle
)

// ---------------------------------------------------------------------------
// List styles
// ---------------------------------------------------------------------------

var (
	ListItemStyle         = iostreams.ListItemStyle
	ListItemSelectedStyle = iostreams.ListItemSelectedStyle
	ListItemDimStyle      = iostreams.ListItemDimStyle
)

// ---------------------------------------------------------------------------
// Help bar styles
// ---------------------------------------------------------------------------

var (
	HelpKeyStyle       = iostreams.HelpKeyStyle
	HelpDescStyle      = iostreams.HelpDescStyle
	HelpSeparatorStyle = iostreams.HelpSeparatorStyle
)

// ---------------------------------------------------------------------------
// Label-value pair styles
// ---------------------------------------------------------------------------

var (
	LabelStyle = iostreams.LabelStyle
	ValueStyle = iostreams.ValueStyle
	CountStyle = iostreams.CountStyle
)

// ---------------------------------------------------------------------------
// Status indicator styles
// ---------------------------------------------------------------------------

var (
	StatusRunningStyle = iostreams.StatusRunningStyle
	StatusStoppedStyle = iostreams.StatusStoppedStyle
	StatusErrorStyle   = iostreams.StatusErrorStyle
	StatusWarningStyle = iostreams.StatusWarningStyle
	StatusInfoStyle    = iostreams.StatusInfoStyle
)

// ---------------------------------------------------------------------------
// Badge styles
// ---------------------------------------------------------------------------

var (
	BadgeStyle        = iostreams.BadgeStyle
	BadgeSuccessStyle = iostreams.BadgeSuccessStyle
	BadgeWarningStyle = iostreams.BadgeWarningStyle
	BadgeErrorStyle   = iostreams.BadgeErrorStyle
	BadgeMutedStyle   = iostreams.BadgeMutedStyle
)

// ---------------------------------------------------------------------------
// Other styles
// ---------------------------------------------------------------------------

var (
	DividerStyle    = iostreams.DividerStyle
	EmptyStateStyle = iostreams.EmptyStateStyle
	StatusBarStyle  = iostreams.StatusBarStyle
	TagStyle        = iostreams.TagStyle
)

// ---------------------------------------------------------------------------
// Status helpers — wrapped to avoid lipgloss.Style in return types
// ---------------------------------------------------------------------------

// StatusStyle returns a render function appropriate for running/stopped status.
// Returns a func(string) string so callers don't need to import lipgloss.
func StatusStyle(running bool) func(string) string {
	style := iostreams.StatusStyle(running)
	return func(s string) string { return style.Render(s) }
}

// StatusText returns display text for running/stopped status.
func StatusText(running bool) string {
	return iostreams.StatusText(running)
}

// StatusIndicator returns a rendered indicator and symbol for a status string.
// Unlike iostreams.StatusIndicator which returns (lipgloss.Style, string),
// this returns (rendered_indicator, symbol) to avoid lipgloss in the API.
func StatusIndicator(status string) (string, string) {
	style, symbol := iostreams.StatusIndicator(status)
	return style.Render(symbol), symbol
}

// ---------------------------------------------------------------------------
// Design tokens — spacing, breakpoints, layout mode
// ---------------------------------------------------------------------------

const (
	SpaceNone = iostreams.SpaceNone
	SpaceXS   = iostreams.SpaceXS
	SpaceSM   = iostreams.SpaceSM
	SpaceMD   = iostreams.SpaceMD
	SpaceLG   = iostreams.SpaceLG

	WidthCompact = iostreams.WidthCompact
	WidthNormal  = iostreams.WidthNormal
	WidthWide    = iostreams.WidthWide
)

// LayoutMode represents the current layout mode based on terminal width.
type LayoutMode = iostreams.LayoutMode

const (
	LayoutCompact = iostreams.LayoutCompact
	LayoutNormal  = iostreams.LayoutNormal
	LayoutWide    = iostreams.LayoutWide
)

// GetLayoutMode returns the appropriate layout mode for the given terminal width.
func GetLayoutMode(width int) LayoutMode { return iostreams.GetLayoutMode(width) }

// GetContentWidth calculates usable content width given total width and padding.
func GetContentWidth(totalWidth, padding int) int {
	return iostreams.GetContentWidth(totalWidth, padding)
}

// GetContentHeight calculates available content height.
func GetContentHeight(totalHeight, headerHeight, footerHeight int) int {
	return iostreams.GetContentHeight(totalHeight, headerHeight, footerHeight)
}

// MinInt returns the smaller of two integers.
func MinInt(a, b int) int { return iostreams.MinInt(a, b) }

// MaxInt returns the larger of two integers.
func MaxInt(a, b int) int { return iostreams.MaxInt(a, b) }

// ClampInt constrains a value between min and max (inclusive).
func ClampInt(value, minVal, maxVal int) int { return iostreams.ClampInt(value, minVal, maxVal) }

// ---------------------------------------------------------------------------
// Text utilities
// ---------------------------------------------------------------------------

// Truncate shortens a string to maxLen characters, adding "..." if truncated.
func Truncate(s string, maxLen int) string { return iostreams.Truncate(s, maxLen) }

// TruncateMiddle shortens a string by removing characters from the middle.
func TruncateMiddle(s string, maxLen int) string { return iostreams.TruncateMiddle(s, maxLen) }

// PadRight pads a string on the right to the specified width.
func PadRight(s string, width int) string { return iostreams.PadRight(s, width) }

// PadLeft pads a string on the left to the specified width.
func PadLeft(s string, width int) string { return iostreams.PadLeft(s, width) }

// PadCenter centers a string within the specified width.
func PadCenter(s string, width int) string { return iostreams.PadCenter(s, width) }

// WordWrap wraps text to the specified width, breaking on word boundaries.
func WordWrap(s string, width int) string { return iostreams.WordWrap(s, width) }

// WrapLines wraps text to the specified width and returns individual lines.
func WrapLines(s string, width int) []string { return iostreams.WrapLines(s, width) }

// CountVisibleWidth returns the visible width of a string, excluding ANSI codes.
func CountVisibleWidth(s string) int { return iostreams.CountVisibleWidth(s) }

// StripANSI removes all ANSI escape sequences from a string.
func StripANSI(s string) string { return iostreams.StripANSI(s) }

// Indent prefixes each line with the given number of spaces.
func Indent(s string, spaces int) string { return iostreams.Indent(s, spaces) }

// JoinNonEmpty joins non-empty strings with the given separator.
func JoinNonEmpty(sep string, parts ...string) string { return iostreams.JoinNonEmpty(sep, parts...) }

// Repeat returns s repeated n times. Returns empty string if n <= 0.
func Repeat(s string, n int) string { return iostreams.Repeat(s, n) }

// FirstLine returns the first line of a multi-line string.
func FirstLine(s string) string { return iostreams.FirstLine(s) }

// LineCount returns the number of lines in a string.
func LineCount(s string) int { return iostreams.LineCount(s) }

// ---------------------------------------------------------------------------
// Layout helpers
// ---------------------------------------------------------------------------

// Type aliases for layout config types.
type (
	SplitConfig      = iostreams.SplitConfig
	GridConfig       = iostreams.GridConfig
	BoxConfig        = iostreams.BoxConfig
	ResponsiveLayout = iostreams.ResponsiveLayout
)

// DefaultSplitConfig returns a sensible default split configuration.
func DefaultSplitConfig() SplitConfig { return iostreams.DefaultSplitConfig() }

// SplitHorizontal calculates widths for a horizontal split (left/right).
func SplitHorizontal(width int, cfg SplitConfig) (int, int) {
	return iostreams.SplitHorizontal(width, cfg)
}

// SplitVertical calculates heights for a vertical split (top/bottom).
func SplitVertical(height int, cfg SplitConfig) (int, int) {
	return iostreams.SplitVertical(height, cfg)
}

// Stack vertically stacks components with the given spacing between them.
func Stack(spacing int, components ...string) string {
	return iostreams.Stack(spacing, components...)
}

// Row arranges components horizontally with the given spacing.
func Row(spacing int, components ...string) string {
	return iostreams.Row(spacing, components...)
}

// Columns arranges content in fixed-width columns.
func Columns(width, gap int, contents ...string) string {
	return iostreams.Columns(width, gap, contents...)
}

// FlexRow arranges items with flexible spacing to fill width.
func FlexRow(width int, left, center, right string) string {
	return iostreams.FlexRow(width, left, center, right)
}

// Grid arranges items in a grid with the specified number of columns.
func Grid(cfg GridConfig, items ...string) string { return iostreams.Grid(cfg, items...) }

// Box creates a fixed-size box with the given content.
func Box(cfg BoxConfig, content string) string { return iostreams.Box(cfg, content) }

// CenterInRect centers content within a rectangle of the given dimensions.
func CenterInRect(content string, width, height int) string {
	return iostreams.CenterInRect(content, width, height)
}

// AlignLeft aligns content to the left within the given width.
func AlignLeft(content string, width int) string { return iostreams.AlignLeft(content, width) }

// AlignRight aligns content to the right within the given width.
func AlignRight(content string, width int) string { return iostreams.AlignRight(content, width) }

// AlignCenter centers content within the given width.
func AlignCenter(content string, width int) string { return iostreams.AlignCenter(content, width) }

// ---------------------------------------------------------------------------
// Time formatting
// ---------------------------------------------------------------------------

// FormatRelative returns a human-readable relative time string like "2 hours ago".
func FormatRelative(t time.Time) string { return iostreams.FormatRelative(t) }

// FormatDuration returns a compact duration string like "2m 30s".
func FormatDuration(d time.Duration) string { return iostreams.FormatDuration(d) }

// FormatUptime formats a duration as an uptime string like "2d 5h 30m".
func FormatUptime(d time.Duration) string { return iostreams.FormatUptime(d) }

// FormatDate formats a date for display like "Jan 2, 2006".
func FormatDate(t time.Time) string { return iostreams.FormatDate(t) }

// FormatDateTime formats a date and time like "Jan 2, 2006 15:04".
func FormatDateTime(t time.Time) string { return iostreams.FormatDateTime(t) }

// FormatTimestamp formats a time for display as "2006-01-02 15:04:05".
func FormatTimestamp(t time.Time) string { return iostreams.FormatTimestamp(t) }
