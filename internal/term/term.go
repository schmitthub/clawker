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
	is256enabled bool
	hasTrueColor bool
	width        int
	widthPercent int
}

// FromEnv creates a Term by reading from the real system environment.
func FromEnv() Term {
	var stdoutIsTTY bool
	var isColorEnabled bool
	var termWidthOverride int
	var termWidthPercentage int

	stdoutIsTTY = IsTerminal(os.Stdout)
	isColorEnabled = IsColorForced() || (!IsColorDisabled() && stdoutIsTTY)

	isVirtualTerminal := false
	if stdoutIsTTY {
		if err := enableVirtualTerminalProcessing(os.Stdout); err == nil {
			isVirtualTerminal = true
		}
	}

	return Term{
		in:           os.Stdin,
		out:          os.Stdout,
		errOut:       os.Stderr,
		isTTY:        stdoutIsTTY,
		colorEnabled: isColorEnabled,
		is256enabled: isVirtualTerminal || is256ColorSupported(),
		hasTrueColor: isVirtualTerminal || isTrueColorSupported(),
		width:        termWidthOverride,
		widthPercent: termWidthPercentage,
	}
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
	return t.is256enabled
}

// IsTrueColorSupported returns true if the terminal supports 24-bit true color.
func (t *Term) IsTrueColorSupported() bool {
	return t.hasTrueColor
}

// IsColorDisabled returns true if environment variables NO_COLOR or CLICOLOR prohibit usage of color codes
// in terminal output.
func IsColorDisabled() bool {
	return os.Getenv("NO_COLOR") != "" || os.Getenv("CLICOLOR") == "0"
}

// IsColorForced returns true if environment variable CLICOLOR_FORCE is set to force colored terminal output.
func IsColorForced() bool {
	return os.Getenv("CLICOLOR_FORCE") != "" && os.Getenv("CLICOLOR_FORCE") != "0"
}

// Size returns the width and height of the terminal that the current process is attached to.
// In case of errors, the numeric values returned are -1.
func (t Term) Size() (int, int, error) {
	if t.width > 0 {
		return t.width, -1, nil
	}

	ttyOut := t.out
	if ttyOut == nil || !IsTerminal(ttyOut) {
		if f, err := openTTY(); err == nil {
			defer f.Close()
			ttyOut = f
		} else {
			return -1, -1, err
		}
	}

	width, height, err := terminalSize(ttyOut)
	if err == nil && t.widthPercent > 0 {
		return int(float64(width) * float64(t.widthPercent) / 100), height, nil
	}

	return width, height, err
}

// IsTerminal reports whether a file descriptor is connected to a terminal.
func IsTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

func terminalSize(f *os.File) (int, int, error) {
	return term.GetSize(int(f.Fd()))
}

func is256ColorSupported() bool {
	return isTrueColorSupported() ||
		strings.Contains(os.Getenv("TERM"), "256") ||
		strings.Contains(os.Getenv("COLORTERM"), "256")
}

func isTrueColorSupported() bool {
	term := os.Getenv("TERM")
	colorterm := os.Getenv("COLORTERM")

	return strings.Contains(term, "24bit") ||
		strings.Contains(term, "truecolor") ||
		strings.Contains(colorterm, "24bit") ||
		strings.Contains(colorterm, "truecolor")
}
