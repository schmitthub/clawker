// Package text provides pure text/string utility functions.
// All functions are ANSI-aware where relevant (counting visible width,
// truncation, padding). This is a leaf package with zero internal imports.
package text

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// ansiPattern matches ANSI escape sequences for stripping.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// Truncate shortens a string to width visible characters, adding "..." if truncated.
// ANSI-aware: counts visible characters only. When truncation occurs, ANSI codes
// are stripped from the result (reinserting codes at truncation boundaries is not supported).
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}

	visible := CountVisibleWidth(s)
	if visible <= width {
		return s
	}

	plain := StripANSI(s)
	runes := []rune(plain)

	if width <= 3 {
		return string(runes[:min(width, len(runes))])
	}

	return string(runes[:width-3]) + "..."
}

// TruncateMiddle shortens a string by removing characters from the middle.
// Useful for paths: "/Users/foo/very/long/path" -> "/Us.../path"
// ANSI-aware: when truncation occurs, ANSI codes are stripped from the result.
func TruncateMiddle(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if width <= 5 {
		return Truncate(s, width)
	}

	plain := StripANSI(s)
	runes := []rune(plain)
	length := len(runes)

	if length <= width {
		return s
	}

	ellipsis := "..."
	available := width - len(ellipsis)
	startLen := available / 2
	endLen := available - startLen

	return string(runes[:startLen]) + ellipsis + string(runes[length-endLen:])
}

// PadRight pads a string on the right to the specified width.
// ANSI-aware: counts visible characters only.
func PadRight(s string, width int) string {
	visible := CountVisibleWidth(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
}

// PadLeft pads a string on the left to the specified width.
// ANSI-aware: counts visible characters only.
func PadLeft(s string, width int) string {
	visible := CountVisibleWidth(s)
	if visible >= width {
		return s
	}
	return strings.Repeat(" ", width-visible) + s
}

// PadCenter centers a string within the specified width.
// ANSI-aware: counts visible characters only.
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
				currentLine.WriteString(word)
				currentLen = wordLen
			} else if currentLen+1+wordLen <= width {
				currentLine.WriteString(" ")
				currentLine.WriteString(word)
				currentLen += 1 + wordLen
			} else {
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

// Indent prefixes each non-empty line with the given number of spaces.
func Indent(s string, spaces int) string {
	if s == "" || spaces <= 0 {
		return s
	}

	prefix := strings.Repeat(" ", spaces)
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
