package term

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFromEnv_256Color(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "")

	tm := FromEnv()

	assert.True(t, tm.IsColorEnabled(), "color should be enabled for xterm-256color")
	assert.True(t, tm.Is256ColorSupported(), "256 color should be supported for xterm-256color")
	assert.False(t, tm.IsTrueColorSupported(), "truecolor should not be supported without COLORTERM")
}

func TestFromEnv_TrueColor(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")

	tm := FromEnv()

	assert.True(t, tm.IsColorEnabled(), "color should be enabled")
	assert.True(t, tm.Is256ColorSupported(), "256 color should be supported")
	assert.True(t, tm.IsTrueColorSupported(), "truecolor should be supported")
}

func TestFromEnv_TrueColor24bit(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("COLORTERM", "24bit")

	tm := FromEnv()

	assert.True(t, tm.IsColorEnabled(), "color should be enabled")
	assert.True(t, tm.Is256ColorSupported(), "truecolor implies 256 color support")
	assert.True(t, tm.IsTrueColorSupported(), "24bit should be recognized as truecolor")
}

func TestFromEnv_DumbTerminal(t *testing.T) {
	t.Setenv("TERM", "dumb")
	t.Setenv("COLORTERM", "")

	tm := FromEnv()

	assert.False(t, tm.IsColorEnabled(), "color should be disabled for dumb terminal")
	assert.False(t, tm.Is256ColorSupported(), "256 color should not be supported for dumb terminal")
	assert.False(t, tm.IsTrueColorSupported(), "truecolor should not be supported for dumb terminal")
}

func TestFromEnv_EmptyTerm(t *testing.T) {
	t.Setenv("TERM", "")
	t.Setenv("COLORTERM", "")

	tm := FromEnv()

	assert.False(t, tm.IsColorEnabled(), "color should be disabled for empty TERM")
	assert.False(t, tm.Is256ColorSupported(), "256 color should not be supported for empty TERM")
	assert.False(t, tm.IsTrueColorSupported(), "truecolor should not be supported for empty TERM")
}

func TestFromEnv_BasicXterm(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("COLORTERM", "")

	tm := FromEnv()

	// Basic color requires TTY + non-empty/non-dumb TERM.
	// In test environments stdout is typically not a TTY, so color
	// is only enabled when the test runner provides a real terminal.
	if tm.IsTTY() {
		assert.True(t, tm.IsColorEnabled(), "color should be enabled for xterm on a TTY")
	} else {
		assert.False(t, tm.IsColorEnabled(), "color disabled when stdout is not a TTY")
	}
	assert.False(t, tm.Is256ColorSupported(), "256 color should not be supported for plain xterm")
	assert.False(t, tm.IsTrueColorSupported(), "truecolor should not be supported for plain xterm")
}

func TestFromEnv_Width(t *testing.T) {
	tm := FromEnv()

	assert.Greater(t, tm.Width(), 0, "width should be greater than 0")
}

func TestFromEnv_FileDescriptors(t *testing.T) {
	tm := FromEnv()

	assert.Equal(t, os.Stdin, tm.in, "in should be os.Stdin")
	assert.Equal(t, os.Stdout, tm.out, "out should be os.Stdout")
	assert.Equal(t, os.Stderr, tm.errOut, "errOut should be os.Stderr")
}
