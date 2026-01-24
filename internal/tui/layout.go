package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// SplitConfig configures horizontal or vertical splitting.
type SplitConfig struct {
	// Ratio is the proportion for the first section (0.0 to 1.0).
	// For horizontal splits: left section ratio.
	// For vertical splits: top section ratio.
	Ratio float64

	// MinFirst is the minimum size for the first section.
	MinFirst int

	// MinSecond is the minimum size for the second section.
	MinSecond int

	// Gap is the space between sections.
	Gap int
}

// DefaultSplitConfig returns a sensible default split configuration.
func DefaultSplitConfig() SplitConfig {
	return SplitConfig{
		Ratio:     0.5,
		MinFirst:  10,
		MinSecond: 10,
		Gap:       1,
	}
}

// SplitHorizontal calculates widths for a horizontal split (left/right).
func SplitHorizontal(width int, cfg SplitConfig) (leftW, rightW int) {
	available := width - cfg.Gap
	if available <= 0 {
		return 0, 0
	}

	leftW = int(float64(available) * cfg.Ratio)
	rightW = available - leftW

	// Apply minimums
	if leftW < cfg.MinFirst {
		leftW = cfg.MinFirst
		rightW = available - leftW
	}
	if rightW < cfg.MinSecond {
		rightW = cfg.MinSecond
		leftW = available - rightW
	}

	// Clamp to available space
	if leftW < 0 {
		leftW = 0
	}
	if rightW < 0 {
		rightW = 0
	}

	return leftW, rightW
}

// SplitVertical calculates heights for a vertical split (top/bottom).
func SplitVertical(height int, cfg SplitConfig) (topH, bottomH int) {
	available := height - cfg.Gap
	if available <= 0 {
		return 0, 0
	}

	topH = int(float64(available) * cfg.Ratio)
	bottomH = available - topH

	// Apply minimums
	if topH < cfg.MinFirst {
		topH = cfg.MinFirst
		bottomH = available - topH
	}
	if bottomH < cfg.MinSecond {
		bottomH = cfg.MinSecond
		topH = available - bottomH
	}

	// Clamp to available space
	if topH < 0 {
		topH = 0
	}
	if bottomH < 0 {
		bottomH = 0
	}

	return topH, bottomH
}

// Stack vertically stacks components with the given spacing between them.
func Stack(spacing int, components ...string) string {
	if len(components) == 0 {
		return ""
	}

	var nonEmpty []string
	for _, c := range components {
		if c != "" {
			nonEmpty = append(nonEmpty, c)
		}
	}

	if len(nonEmpty) == 0 {
		return ""
	}

	spacer := strings.Repeat("\n", spacing)
	return strings.Join(nonEmpty, "\n"+spacer)
}

// Row arranges components horizontally with the given spacing.
func Row(spacing int, components ...string) string {
	if len(components) == 0 {
		return ""
	}

	var nonEmpty []string
	for _, c := range components {
		if c != "" {
			nonEmpty = append(nonEmpty, c)
		}
	}

	if len(nonEmpty) == 0 {
		return ""
	}

	gap := strings.Repeat(" ", spacing)
	return lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(nonEmpty, gap))
}

// Columns arranges content in fixed-width columns.
func Columns(width, gap int, contents ...string) string {
	if len(contents) == 0 {
		return ""
	}

	colWidth := max((width-gap*(len(contents)-1))/len(contents), 1)

	var cols []string
	for _, content := range contents {
		col := lipgloss.NewStyle().Width(colWidth).Render(content)
		cols = append(cols, col)
	}

	spacer := strings.Repeat(" ", gap)
	return strings.Join(cols, spacer)
}

// CenterInRect centers content within a rectangle of the given dimensions.
func CenterInRect(content string, width, height int) string {
	style := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Align(lipgloss.Center, lipgloss.Center)

	return style.Render(content)
}

// AlignLeft aligns content to the left within the given width.
func AlignLeft(content string, width int) string {
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Left).Render(content)
}

// AlignRight aligns content to the right within the given width.
func AlignRight(content string, width int) string {
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Right).Render(content)
}

// AlignCenter centers content within the given width.
func AlignCenter(content string, width int) string {
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(content)
}

// FlexRow arranges items with flexible spacing to fill width.
// Items at the start, middle, and end are distributed across the width.
func FlexRow(width int, left, center, right string) string {
	leftW := CountVisibleWidth(left)
	centerW := CountVisibleWidth(center)
	rightW := CountVisibleWidth(right)

	// Calculate available space for padding
	totalContent := leftW + centerW + rightW
	available := max(width-totalContent, 0)

	// Distribute space
	leftPad := available / 2
	rightPad := available - leftPad

	// Handle empty sections
	if center == "" {
		leftPad = available
		rightPad = 0
	}
	if left == "" {
		leftPad = 0
	}
	if right == "" {
		rightPad = 0
	}

	return left + strings.Repeat(" ", leftPad) + center + strings.Repeat(" ", rightPad) + right
}

// GridConfig configures a grid layout.
type GridConfig struct {
	Columns int
	Gap     int
	Width   int
}

// Grid arranges items in a grid with the specified number of columns.
func Grid(cfg GridConfig, items ...string) string {
	if len(items) == 0 || cfg.Columns <= 0 {
		return ""
	}

	colWidth := max((cfg.Width-cfg.Gap*(cfg.Columns-1))/cfg.Columns, 1)

	var rows []string
	for i := 0; i < len(items); i += cfg.Columns {
		end := min(i+cfg.Columns, len(items))

		rowItems := items[i:end]
		var rowParts []string
		for _, item := range rowItems {
			cell := lipgloss.NewStyle().Width(colWidth).Render(item)
			rowParts = append(rowParts, cell)
		}

		row := strings.Join(rowParts, strings.Repeat(" ", cfg.Gap))
		rows = append(rows, row)
	}

	return strings.Join(rows, "\n")
}

// BoxConfig configures a box layout.
type BoxConfig struct {
	Width   int
	Height  int
	Padding int
}

// Box creates a fixed-size box with the given content.
func Box(cfg BoxConfig, content string) string {
	style := lipgloss.NewStyle().
		Width(cfg.Width).
		Height(cfg.Height).
		Padding(cfg.Padding)

	return style.Render(content)
}

// ResponsiveLayout returns different layouts based on width.
type ResponsiveLayout struct {
	Compact func() string
	Normal  func() string
	Wide    func() string
}

// Render returns the appropriate layout for the given width.
func (r ResponsiveLayout) Render(width int) string {
	mode := GetLayoutMode(width)
	switch mode {
	case LayoutWide:
		if r.Wide != nil {
			return r.Wide()
		}
		fallthrough
	case LayoutNormal:
		if r.Normal != nil {
			return r.Normal()
		}
		fallthrough
	default:
		if r.Compact != nil {
			return r.Compact()
		}
		return ""
	}
}
