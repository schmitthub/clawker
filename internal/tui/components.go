package tui

import (
	"fmt"
	"strings"
)

// HeaderConfig configures a header component.
type HeaderConfig struct {
	Title     string
	Subtitle  string
	Timestamp string
	Width     int
}

// RenderHeader renders a header with title, optional subtitle, and timestamp.
func RenderHeader(cfg HeaderConfig) string {
	title := HeaderTitleStyle.Render(cfg.Title)

	var parts []string
	parts = append(parts, title)

	if cfg.Subtitle != "" {
		parts = append(parts, HeaderSubtitleStyle.Render(cfg.Subtitle))
	}

	left := strings.Join(parts, " ")

	if cfg.Timestamp != "" {
		return FlexRow(cfg.Width, left, "", MutedStyle.Render(cfg.Timestamp))
	}

	return left
}

// StatusConfig configures a status indicator.
type StatusConfig struct {
	Status string
	Label  string
}

// RenderStatus renders a status indicator like "● RUNNING".
func RenderStatus(cfg StatusConfig) string {
	rendered, symbol := StatusIndicator(cfg.Status)
	_ = rendered // rendered indicator already has the symbol styled
	if cfg.Label == "" {
		cfg.Label = strings.ToUpper(cfg.Status)
	}
	// Use the style from iostreams directly to render symbol + label together
	style, _ := statusIndicatorStyle(cfg.Status)
	return style(symbol + " " + cfg.Label)
}

// statusIndicatorStyle returns a render function for a status string.
func statusIndicatorStyle(status string) (func(string) string, string) {
	switch status {
	case "running":
		return func(s string) string { return StatusRunningStyle.Render(s) }, "\u25cf" // ●
	case "stopped", "exited":
		return func(s string) string { return StatusStoppedStyle.Render(s) }, "\u25cb" // ○
	case "error", "failed":
		return func(s string) string { return StatusErrorStyle.Render(s) }, "\u2717" // ✗
	case "warning":
		return func(s string) string { return StatusWarningStyle.Render(s) }, "\u26a0" // ⚠
	case "pending", "waiting":
		return func(s string) string { return StatusInfoStyle.Render(s) }, "\u25cb" // ○
	default:
		return func(s string) string { return MutedStyle.Render(s) }, "\u25cb" // ○
	}
}

// RenderBadge renders text as a styled badge.
// If no render function is provided, uses BadgeStyle.
func RenderBadge(text string, render ...func(string) string) string {
	if len(render) > 0 {
		return render[0](text)
	}
	return BadgeStyle.Render(text)
}

// RenderCountBadge renders a count with a label, like "3 tasks".
func RenderCountBadge(count int, label string) string {
	countStr := CountStyle.Render(fmt.Sprintf("%d", count))
	return countStr + " " + MutedStyle.Render(label)
}

// ProgressConfig configures a progress indicator.
type ProgressConfig struct {
	Current int
	Total   int
	Width   int
	ShowBar bool
}

// RenderProgress renders a progress indicator.
// If ShowBar is true, renders a progress bar.
// Otherwise renders "current/total".
func RenderProgress(cfg ProgressConfig) string {
	if cfg.Total <= 0 {
		return MutedStyle.Render("-")
	}

	if !cfg.ShowBar {
		return fmt.Sprintf("%d/%d", cfg.Current, cfg.Total)
	}

	// Render progress bar
	width := cfg.Width
	if width < 3 {
		width = 10
	}

	barWidth := width - 2 // Account for brackets
	filled := 0
	if cfg.Total > 0 {
		filled = (cfg.Current * barWidth) / cfg.Total
	}
	filled = ClampInt(filled, 0, barWidth)
	empty := barWidth - filled

	bar := "[" +
		SuccessStyle.Render(strings.Repeat("=", filled)) +
		MutedStyle.Render(strings.Repeat("-", empty)) +
		"]"

	return bar
}

// RenderDivider renders a horizontal divider line.
func RenderDivider(width int) string {
	if width <= 0 {
		return ""
	}
	return DividerStyle.Render(strings.Repeat("\u2500", width)) // ─
}

// RenderLabeledDivider renders a divider with a centered label.
func RenderLabeledDivider(label string, width int) string {
	if width <= 0 {
		return ""
	}

	labelLen := CountVisibleWidth(label)
	if labelLen+4 >= width {
		return RenderDivider(width)
	}

	leftWidth := (width - labelLen - 2) / 2
	rightWidth := width - labelLen - 2 - leftWidth

	left := strings.Repeat("\u2500", leftWidth) // ─
	right := strings.Repeat("\u2500", rightWidth)

	return DividerStyle.Render(left) + " " + MutedStyle.Render(label) + " " + DividerStyle.Render(right)
}

// RenderEmptyState renders a centered empty state message.
func RenderEmptyState(message string, width, height int) string {
	return CenterInRect(EmptyStateStyle.Render(message), width, height)
}

// RenderError renders an error message.
func RenderError(err error, width int) string {
	if err == nil {
		return ""
	}

	errorText := ErrorStyle.Render("\u2717 Error: ") + err.Error()
	if width > 0 {
		wrapped := WordWrap(errorText, width)
		return wrapped
	}
	return errorText
}

// RenderLabelValue renders a label-value pair.
func RenderLabelValue(label, value string) string {
	return LabelStyle.Render(label+":") + " " + ValueStyle.Render(value)
}

// KeyValuePair represents a key-value pair for display.
type KeyValuePair struct {
	Key   string
	Value string
}

// RenderKeyValueTable renders a table of key-value pairs.
func RenderKeyValueTable(pairs []KeyValuePair, width int) string {
	if len(pairs) == 0 {
		return ""
	}

	// Find max key width
	maxKeyLen := 0
	for _, p := range pairs {
		keyLen := CountVisibleWidth(p.Key)
		if keyLen > maxKeyLen {
			maxKeyLen = keyLen
		}
	}

	// Render pairs with aligned colons
	var lines []string
	for _, p := range pairs {
		key := LabelStyle.Width(maxKeyLen + 1).Render(p.Key + ":")
		val := ValueStyle.Render(p.Value)
		lines = append(lines, key+" "+val)
	}

	return strings.Join(lines, "\n")
}

// RenderTable renders a simple table with headers and rows.
type TableConfig struct {
	Headers   []string
	Rows      [][]string
	ColWidths []int
	Width     int
}

// RenderTable renders a simple table.
func RenderTable(cfg TableConfig) string {
	if len(cfg.Headers) == 0 {
		return ""
	}

	// Calculate column widths if not specified
	colWidths := cfg.ColWidths
	if len(colWidths) == 0 {
		colWidths = make([]int, len(cfg.Headers))
		available := cfg.Width - (len(cfg.Headers) - 1) // Gap between columns
		colWidth := available / len(cfg.Headers)
		for i := range colWidths {
			colWidths[i] = colWidth
		}
	}

	var lines []string

	// Render header
	var headerParts []string
	for i, h := range cfg.Headers {
		width := colWidths[i]
		headerParts = append(headerParts, HeaderStyle.Width(width).Render(Truncate(h, width)))
	}
	lines = append(lines, strings.Join(headerParts, " "))

	// Render divider
	var dividerParts []string
	for _, w := range colWidths {
		dividerParts = append(dividerParts, strings.Repeat("\u2500", w))
	}
	lines = append(lines, DividerStyle.Render(strings.Join(dividerParts, " ")))

	// Render rows
	for _, row := range cfg.Rows {
		var rowParts []string
		for i := range cfg.Headers {
			width := colWidths[i]
			val := ""
			if i < len(row) {
				val = row[i]
			}
			rowParts = append(rowParts, PadRight(Truncate(val, width), width))
		}
		lines = append(lines, strings.Join(rowParts, " "))
	}

	return strings.Join(lines, "\n")
}

// RenderPercentage renders a percentage value with appropriate styling.
func RenderPercentage(value float64) string {
	style := MutedStyle
	if value >= 80 {
		style = ErrorStyle
	} else if value >= 60 {
		style = WarningStyle
	}
	return style.Render(fmt.Sprintf("%.1f%%", value))
}

// RenderBytes renders a byte count in human-readable format.
func RenderBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// RenderTag renders a tag-like element.
// If no render function is provided, uses TagStyle.
func RenderTag(text string, render ...func(string) string) string {
	if len(render) > 0 {
		return render[0](text)
	}
	return TagStyle.Render(text)
}

// RenderTags renders multiple tags inline.
// If no render function is provided, uses TagStyle.
func RenderTags(tags []string, render ...func(string) string) string {
	if len(tags) == 0 {
		return ""
	}

	var rendered []string
	for _, tag := range tags {
		rendered = append(rendered, RenderTag(tag, render...))
	}

	return strings.Join(rendered, " ")
}
