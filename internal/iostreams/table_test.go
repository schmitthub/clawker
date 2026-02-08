package iostreams

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forceColorProfile sets lipgloss to emit ANSI escapes regardless of writer type.
// Restores the previous profile on cleanup.
func forceColorProfile(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestRenderStyledTable_Basic(t *testing.T) {
	forceColorProfile(t)
	tio := NewTestIOStreams()
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(80, 24)

	output := tio.IOStreams.RenderStyledTable(
		[]string{"NAME", "STATUS", "IMAGE"},
		[][]string{
			{"web", "running", "nginx:latest"},
			{"db", "stopped", "postgres:16"},
		},
		nil,
	)

	// Headers should be uppercase
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "STATUS")
	assert.Contains(t, output, "IMAGE")

	// Data should be present
	assert.Contains(t, output, "web")
	assert.Contains(t, output, "running")
	assert.Contains(t, output, "nginx:latest")
	assert.Contains(t, output, "db")
	assert.Contains(t, output, "stopped")
	assert.Contains(t, output, "postgres:16")

	// Should contain ANSI escapes (styled)
	assert.Contains(t, output, "\x1b[", "styled output should contain ANSI escapes")
}

func TestRenderStyledTable_UppercaseHeaders(t *testing.T) {
	forceColorProfile(t)
	tio := NewTestIOStreams()
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(80, 24)

	output := tio.IOStreams.RenderStyledTable(
		[]string{"name", "status"},
		[][]string{{"web", "ok"}},
		nil,
	)

	// Lower-case input should become uppercase in output
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "STATUS")
}

func TestRenderStyledTable_Empty(t *testing.T) {
	forceColorProfile(t)
	tio := NewTestIOStreams()
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(80, 24)

	output := tio.IOStreams.RenderStyledTable(
		[]string{"NAME", "STATUS"},
		nil,
		nil,
	)

	// Headers should still render
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "STATUS")
}

func TestRenderStyledTable_NoBorders(t *testing.T) {
	forceColorProfile(t)
	tio := NewTestIOStreams()
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(80, 24)

	output := tio.IOStreams.RenderStyledTable(
		[]string{"A", "B"},
		[][]string{{"1", "2"}},
		nil,
	)

	// Should not contain border characters
	for _, ch := range []string{"│", "─", "┌", "┐", "└", "┘", "├", "┤", "┬", "┴", "┼", "|", "+", "-"} {
		assert.NotContains(t, output, ch, "should have no borders")
	}
}

func TestRenderStyledTable_FitsTermWidth(t *testing.T) {
	forceColorProfile(t)
	tio := NewTestIOStreams()
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(60, 24)

	output := tio.IOStreams.RenderStyledTable(
		[]string{"IMAGE", "ID", "CREATED", "SIZE"},
		[][]string{
			{"clawker-fawker-demo:latest", "a1b2c3d4e5f6", "2 months ago", "256.00MB"},
			{"node:20-slim", "a1b2c3d4e5f6", "2 months ago", "256.00MB"},
		},
		nil,
	)

	// Each line should not exceed terminal width
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		require.LessOrEqual(t, lipgloss.Width(line), 60,
			"line exceeds terminal width: %q", line)
	}
}
