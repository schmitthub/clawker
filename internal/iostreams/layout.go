package iostreams

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/schmitthub/clawker/internal/text"
)

// Stack vertically stacks components with the given spacing between them.
// Empty strings are filtered out. Negative spacing is clamped to zero.
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

	spacer := strings.Repeat("\n", max(spacing, 0))
	return strings.Join(nonEmpty, "\n"+spacer)
}

// Row arranges components horizontally with the given spacing.
// Empty strings are filtered out. Negative spacing is clamped to zero.
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

	gap := strings.Repeat(" ", max(spacing, 0))
	return lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(nonEmpty, gap))
}

// FlexRow arranges items with flexible spacing to fill width.
// Left, center, and right content are distributed across the width.
func FlexRow(width int, left, center, right string) string {
	leftW := text.CountVisibleWidth(left)
	centerW := text.CountVisibleWidth(center)
	rightW := text.CountVisibleWidth(right)

	totalContent := leftW + centerW + rightW
	available := max(width-totalContent, 0)

	leftPad := available / 2
	rightPad := available - leftPad

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

// CenterInRect centers content within a rectangle of the given dimensions.
func CenterInRect(content string, width, height int) string {
	style := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Align(lipgloss.Center, lipgloss.Center)

	return style.Render(content)
}
