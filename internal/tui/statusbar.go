package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// StatusBarModel represents a single-line status bar with left/center/right sections.
type StatusBarModel struct {
	left   string
	center string
	right  string
	width  int
	style  lipgloss.Style
}

// NewStatusBar creates a new status bar with the given width.
func NewStatusBar(width int) StatusBarModel {
	return StatusBarModel{
		width: width,
		style: lipgloss.NewStyle().
			Background(ColorBgAlt).
			Foreground(lipgloss.Color("#FFFFFF")).
			Padding(0, 1),
	}
}

// SetLeft sets the left section content.
func (m StatusBarModel) SetLeft(s string) StatusBarModel {
	m.left = s
	return m
}

// SetCenter sets the center section content.
func (m StatusBarModel) SetCenter(s string) StatusBarModel {
	m.center = s
	return m
}

// SetRight sets the right section content.
func (m StatusBarModel) SetRight(s string) StatusBarModel {
	m.right = s
	return m
}

// SetWidth sets the status bar width.
func (m StatusBarModel) SetWidth(width int) StatusBarModel {
	m.width = width
	return m
}

// SetStyle sets the status bar style.
func (m StatusBarModel) SetStyle(style lipgloss.Style) StatusBarModel {
	m.style = style
	return m
}

// View renders the status bar.
func (m StatusBarModel) View() string {
	// Calculate content with padding removed for space calculation
	innerWidth := m.width - 2 // Account for padding

	content := FlexRow(innerWidth, m.left, m.center, m.right)
	return m.style.Width(m.width).Render(content)
}

// Left returns the left section content.
func (m StatusBarModel) Left() string {
	return m.left
}

// Center returns the center section content.
func (m StatusBarModel) Center() string {
	return m.center
}

// Right returns the right section content.
func (m StatusBarModel) Right() string {
	return m.right
}

// Width returns the status bar width.
func (m StatusBarModel) Width() int {
	return m.width
}

// RenderStatusBar is a convenience function that renders a status bar.
func RenderStatusBar(left, center, right string, width int) string {
	return NewStatusBar(width).
		SetLeft(left).
		SetCenter(center).
		SetRight(right).
		View()
}

// StatusBarSection represents a styled section of the status bar.
type StatusBarSection struct {
	Content string
	Style   lipgloss.Style
}

// RenderStatusBarWithSections renders a status bar with styled sections.
func RenderStatusBarWithSections(sections []StatusBarSection, width int) string {
	var parts []string
	totalContent := 0

	for _, s := range sections {
		content := s.Style.Render(s.Content)
		parts = append(parts, content)
		totalContent += CountVisibleWidth(s.Content)
	}

	// Calculate spacing
	gaps := max(len(sections)-1, 0)
	availableSpace := max(width-totalContent-gaps, 0)

	// Distribute space between sections
	if gaps > 0 {
		spacePerGap := availableSpace / gaps
		remainder := availableSpace % gaps

		var result strings.Builder
		for i, part := range parts {
			result.WriteString(part)
			if i < len(parts)-1 {
				space := spacePerGap
				if i < remainder {
					space++
				}
				result.WriteString(strings.Repeat(" ", space))
			}
		}
		return result.String()
	}

	return strings.Join(parts, "")
}

// ModeIndicator creates a styled mode indicator for the status bar.
func ModeIndicator(mode string, active bool) string {
	var style lipgloss.Style
	if active {
		style = BadgeStyle
	} else {
		style = BadgeMutedStyle
	}
	return style.Render(strings.ToUpper(mode))
}

// ConnectionIndicator creates a connection status indicator.
func ConnectionIndicator(connected bool) string {
	if connected {
		return StatusRunningStyle.Render("\u25cf Connected") // ●
	}
	return StatusErrorStyle.Render("\u25cb Disconnected") // ○
}

// TimerIndicator creates a timer display for the status bar.
func TimerIndicator(label string, value string) string {
	return MutedStyle.Render(label+": ") + ValueStyle.Render(value)
}

// CounterIndicator creates a counter display for the status bar.
func CounterIndicator(label string, current, total int) string {
	return MutedStyle.Render(label+": ") +
		CountStyle.Render(strconv.Itoa(current)) +
		MutedStyle.Render("/") +
		MutedStyle.Render(strconv.Itoa(total))
}
