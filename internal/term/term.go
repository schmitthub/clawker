package term

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// Term holds detected terminal capabilities and file descriptors.
type Term struct {
	in           *os.File
	out          *os.File
	errOut       *os.File
	isTTY        bool
	colorEnabled bool
	is256Enabled bool
	hasTrueColor bool
	width        int

	// widthOverride allows callers to override the detected terminal width.
	// Reserved for future use (e.g., responsive table formatting).
	widthOverride int

	// widthPercent expresses a percentage of terminal width for layout calculations.
	// Reserved for future use (e.g., column width allocation in table output).
	widthPercent int
}

// FromEnv creates a Term by reading from the real system environment.
func FromEnv() *Term {
	t := &Term{
		in:     os.Stdin,
		out:    os.Stdout,
		errOut: os.Stderr,
	}

	t.isTTY = term.IsTerminal(int(os.Stdout.Fd()))

	termEnv := os.Getenv("TERM")
	colorTerm := os.Getenv("COLORTERM")

	// Detect truecolor: COLORTERM is "truecolor" or "24bit"
	t.hasTrueColor = colorTerm == "truecolor" || colorTerm == "24bit"

	// Detect 256 color: TERM contains "256color", or truecolor implies 256
	t.is256Enabled = strings.Contains(termEnv, "256color") || t.hasTrueColor

	// Detect basic color: TTY with non-empty, non-dumb TERM, or 256 implies color
	t.colorEnabled = (t.isTTY && termEnv != "" && termEnv != "dumb") || t.is256Enabled

	// NO_COLOR is a standard convention (https://no-color.org/)
	// Overrides capability detection when user explicitly disables colors
	if os.Getenv("NO_COLOR") != "" {
		t.colorEnabled = false
	}

	// Detect terminal width, fallback to 80
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		w = 80
	}
	t.width = w

	return t
}

// IsTTY returns true if stdout is a terminal.
func (t *Term) IsTTY() bool {
	return t.isTTY
}

// IsColorEnabled returns true if basic color output is supported.
func (t *Term) IsColorEnabled() bool {
	return t.colorEnabled
}

// Is256ColorSupported returns true if the terminal supports 256 colors.
func (t *Term) Is256ColorSupported() bool {
	return t.is256Enabled
}

// IsTrueColorSupported returns true if the terminal supports 24-bit true color.
func (t *Term) IsTrueColorSupported() bool {
	return t.hasTrueColor
}

// Width returns the detected terminal width in columns.
func (t *Term) Width() int {
	return t.width
}
