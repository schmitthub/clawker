// Package tui provides shared TUI components for clawker CLI.
package tui

// Spacing tokens - consistent spacing values for layouts
const (
	SpaceNone = 0
	SpaceXS   = 1
	SpaceSM   = 2
	SpaceMD   = 4
	SpaceLG   = 8
)

// Layout breakpoints - terminal width thresholds
const (
	WidthCompact = 60  // Narrow terminals
	WidthNormal  = 80  // Standard terminals
	WidthWide    = 120 // Wide terminals
)

// LayoutMode represents the current layout mode based on terminal width.
type LayoutMode int

const (
	LayoutCompact LayoutMode = iota
	LayoutNormal
	LayoutWide
)

// String returns a string representation of the layout mode.
func (m LayoutMode) String() string {
	switch m {
	case LayoutCompact:
		return "compact"
	case LayoutNormal:
		return "normal"
	case LayoutWide:
		return "wide"
	default:
		return "unknown"
	}
}

// GetLayoutMode returns the appropriate layout mode for the given terminal width.
func GetLayoutMode(width int) LayoutMode {
	switch {
	case width >= WidthWide:
		return LayoutWide
	case width >= WidthNormal:
		return LayoutNormal
	default:
		return LayoutCompact
	}
}

// GetContentWidth calculates usable content width given total width and padding.
// This accounts for borders (2 chars) and internal padding (2*padding).
func GetContentWidth(totalWidth, padding int) int {
	// Account for left/right borders (2) plus internal padding on each side
	content := totalWidth - 2 - (2 * padding)
	if content < 0 {
		return 0
	}
	return content
}

// GetContentHeight calculates available content height given total height,
// header height, and footer height.
func GetContentHeight(totalHeight, headerHeight, footerHeight int) int {
	content := totalHeight - headerHeight - footerHeight
	if content < 0 {
		return 0
	}
	return content
}

// MinInt returns the smaller of two integers.
func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MaxInt returns the larger of two integers.
func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ClampInt constrains a value between min and max (inclusive).
func ClampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
