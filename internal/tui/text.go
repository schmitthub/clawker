package tui

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// ansiPattern matches ANSI escape sequences for stripping.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// Truncate shortens a string to maxLen characters, adding "..." if truncated.
// It handles ANSI escape codes by counting visible characters only.
func Truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if maxLen <= 3 {
		return s[:MinInt(maxLen, len(s))]
	}

	visible := CountVisibleWidth(s)
	if visible <= maxLen {
		return s
	}

	// Strip ANSI codes to truncate visible text
	plain := StripANSI(s)
	if utf8.RuneCountInString(plain) <= maxLen {
		return s
	}

	// Truncate plain text and add ellipsis
	runes := []rune(plain)
	return string(runes[:maxLen-3]) + "..."
}

// TruncateMiddle shortens a string by removing characters from the middle.
// Useful for paths: "/Users/foo/very/long/path" -> "/Users/...path"
func TruncateMiddle(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if maxLen <= 5 {
		return Truncate(s, maxLen)
	}

	plain := StripANSI(s)
	runes := []rune(plain)
	length := len(runes)

	if length <= maxLen {
		return s
	}

	// Split available space between start and end
	ellipsis := "..."
	available := maxLen - len(ellipsis)
	startLen := available / 2
	endLen := available - startLen

	return string(runes[:startLen]) + ellipsis + string(runes[length-endLen:])
}

// PadRight pads a string on the right to the specified width.
func PadRight(s string, width int) string {
	visible := CountVisibleWidth(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
}

// PadLeft pads a string on the left to the specified width.
func PadLeft(s string, width int) string {
	visible := CountVisibleWidth(s)
	if visible >= width {
		return s
	}
	return strings.Repeat(" ", width-visible) + s
}

// PadCenter centers a string within the specified width.
func PadCenter(s string, width int) string {
	visible := CountVisibleWidth(s)
	if visible >= width {
		return s
	}

	padding := width - visible
	leftPad := padding / 2
	rightPad := padding - leftPad

	return strings.Repeat(" ", leftPad) + s + strings.Repeat(" ", rightPad)
}

// WordWrap wraps text to the specified width, breaking on word boundaries.
func WordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}

	lines := WrapLines(s, width)
	return strings.Join(lines, "\n")
}

// WrapLines wraps text to the specified width and returns individual lines.
func WrapLines(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}

	var lines []string
	paragraphs := strings.Split(s, "\n")

	for _, para := range paragraphs {
		if para == "" {
			lines = append(lines, "")
			continue
		}

		words := strings.Fields(para)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}

		var currentLine strings.Builder
		currentLen := 0

		for _, word := range words {
			wordLen := utf8.RuneCountInString(word)

			if currentLen == 0 {
				// First word on line
				currentLine.WriteString(word)
				currentLen = wordLen
			} else if currentLen+1+wordLen <= width {
				// Word fits on current line
				currentLine.WriteString(" ")
				currentLine.WriteString(word)
				currentLen += 1 + wordLen
			} else {
				// Word doesn't fit, start new line
				lines = append(lines, currentLine.String())
				currentLine.Reset()
				currentLine.WriteString(word)
				currentLen = wordLen
			}
		}

		if currentLine.Len() > 0 {
			lines = append(lines, currentLine.String())
		}
	}

	return lines
}

// CountVisibleWidth returns the visible width of a string, excluding ANSI codes.
func CountVisibleWidth(s string) int {
	plain := StripANSI(s)
	return utf8.RuneCountInString(plain)
}

// StripANSI removes all ANSI escape sequences from a string.
func StripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// Indent prefixes each line in s with the given prefix.
func Indent(s string, prefix string) string {
	if s == "" {
		return ""
	}

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

// JoinNonEmpty joins non-empty strings with the given separator.
func JoinNonEmpty(sep string, parts ...string) string {
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, sep)
}

// Repeat returns s repeated n times. Returns empty string if n <= 0.
func Repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(s, n)
}

// FirstLine returns the first line of a multi-line string.
func FirstLine(s string) string {
	if first, _, found := strings.Cut(s, "\n"); found {
		return first
	}
	return s
}

// LineCount returns the number of lines in a string.
func LineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
