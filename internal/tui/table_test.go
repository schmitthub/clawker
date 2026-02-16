package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forceColorProfile sets lipgloss to emit ANSI escapes regardless of writer type.
// Required because lipgloss/table uses the default lipgloss renderer, which
// auto-detects bytes.Buffer as no-color. Restores the previous profile on cleanup.
func forceColorProfile(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func newTestTUI(t *testing.T) (*TUI, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()
	return NewTUI(tio.IOStreams), tio
}

func TestTablePrinter_NewTable(t *testing.T) {
	tui, _ := newTestTUI(t)
	tp := tui.NewTable("NAME", "STATUS")

	assert.NotNil(t, tp)
	assert.Equal(t, 0, tp.Len())
}

func TestTablePrinter_AddRow(t *testing.T) {
	tui, _ := newTestTUI(t)
	tp := tui.NewTable("NAME", "STATUS")

	tp.AddRow("web", "running")
	tp.AddRow("db", "stopped")
	assert.Equal(t, 2, tp.Len())
}

func TestTablePrinter_AddRow_NormalizesColumns(t *testing.T) {
	tui, _ := newTestTUI(t)
	tp := tui.NewTable("NAME", "STATUS", "IMAGE")

	// Fewer columns than headers — should pad
	tp.AddRow("web")
	assert.Equal(t, 1, tp.Len())

	// More columns than headers — should truncate
	tp.AddRow("db", "running", "nginx", "extra")
	assert.Equal(t, 2, tp.Len())
}

func TestTablePrinter_RenderPlain(t *testing.T) {
	tui, tio := newTestTUI(t)
	// TestIOStreams is non-TTY by default → plain mode
	tp := tui.NewTable("NAME", "STATUS", "IMAGE")
	tp.AddRow("web", "running", "nginx:latest")
	tp.AddRow("db", "stopped", "postgres:16")

	err := tp.Render()
	require.NoError(t, err)

	output := tio.OutBuf.String()

	// Verify headers present
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "STATUS")
	assert.Contains(t, output, "IMAGE")

	// Verify data present
	assert.Contains(t, output, "web")
	assert.Contains(t, output, "running")
	assert.Contains(t, output, "nginx:latest")
	assert.Contains(t, output, "db")
	assert.Contains(t, output, "stopped")
	assert.Contains(t, output, "postgres:16")

	// Verify output goes to Out (stdout), not ErrOut
	assert.Empty(t, tio.ErrBuf.String())
}

func TestTablePrinter_RenderPlain_NoANSI(t *testing.T) {
	tui, tio := newTestTUI(t)
	tp := tui.NewTable("NAME", "STATUS")
	tp.AddRow("web", "running")

	err := tp.Render()
	require.NoError(t, err)

	output := tio.OutBuf.String()
	assert.NotContains(t, output, "\x1b[", "plain mode should not contain ANSI escapes")
}

func TestTablePrinter_RenderStyled(t *testing.T) {
	forceColorProfile(t)
	tui, tio := newTestTUI(t)
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(80, 24)

	tp := tui.NewTable("NAME", "STATUS", "IMAGE")
	tp.AddRow("web", "running", "nginx:latest")
	tp.AddRow("db", "stopped", "postgres:16")

	err := tp.Render()
	require.NoError(t, err)

	output := tio.OutBuf.String()
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "web")
	assert.Contains(t, output, "nginx:latest")
	// Styled mode should contain ANSI escapes
	assert.Contains(t, output, "\x1b[", "styled mode should contain ANSI escapes")
}

func TestTablePrinter_RenderEmpty(t *testing.T) {
	tui, tio := newTestTUI(t)
	tp := tui.NewTable("NAME", "STATUS")

	err := tp.Render()
	require.NoError(t, err)

	output := tio.OutBuf.String()
	// Headers should still render even with no rows
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "STATUS")
}

func TestTablePrinter_RenderNoHeaders(t *testing.T) {
	tui, tio := newTestTUI(t)
	tp := tui.NewTable()

	err := tp.Render()
	require.NoError(t, err)
	assert.Empty(t, tio.OutBuf.String())
}

func TestTablePrinter_RenderStyled_Unicode(t *testing.T) {
	forceColorProfile(t)
	tui, tio := newTestTUI(t)
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(80, 24)

	tp := tui.NewTable("名前", "状態")
	tp.AddRow("ウェブ", "実行中")

	err := tp.Render()
	require.NoError(t, err)

	output := tio.OutBuf.String()
	assert.Contains(t, output, "名前")
	assert.Contains(t, output, "ウェブ")
}

func TestTablePrinter_WithStyleOverrides(t *testing.T) {
	forceColorProfile(t)
	tui, tio := newTestTUI(t)
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(80, 24)

	called := map[string]bool{}
	tp := tui.NewTable("NAME", "STATUS").
		WithHeaderStyle(func(s string) string {
			called["header"] = true
			return "[H:" + s + "]"
		}).
		WithPrimaryStyle(func(s string) string {
			called["primary"] = true
			return "[P:" + s + "]"
		}).
		WithCellStyle(func(s string) string {
			called["cell"] = true
			return "[C:" + s + "]"
		})
	tp.AddRow("web", "running")

	err := tp.Render()
	require.NoError(t, err)

	output := tio.OutBuf.String()
	assert.True(t, called["header"], "header override should be called")
	assert.True(t, called["primary"], "primary override should be called")
	assert.True(t, called["cell"], "cell override should be called")

	assert.Contains(t, output, "[H:NAME]")
	assert.Contains(t, output, "[P:web]")
	assert.Contains(t, output, "[C:running]")
}

func TestTablePrinter_AddRow_NoSliceAliasing(t *testing.T) {
	tui, _ := newTestTUI(t)
	tp := tui.NewTable("NAME", "STATUS", "IMAGE")

	// Pass a slice via spread — normalizeRow must copy, not alias the backing array.
	cols := []string{"web", "running", "nginx:latest", "extra"}
	tp.AddRow(cols...)

	// Mutate the original slice after AddRow.
	cols[0] = "MUTATED"
	cols[1] = "MUTATED"
	cols[2] = "MUTATED"

	// The stored row must be unchanged.
	require.Equal(t, 1, tp.Len())
	assert.Equal(t, []string{"web", "running", "nginx:latest"}, tp.rows[0])
}

func TestTablePrinter_WithStyleOverrides_Partial(t *testing.T) {
	forceColorProfile(t)
	tui, tio := newTestTUI(t)
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(80, 24)

	// Only override header — primary and cell should use defaults
	tp := tui.NewTable("NAME", "STATUS").
		WithHeaderStyle(func(s string) string {
			return "[H:" + s + "]"
		})
	tp.AddRow("web", "running")

	err := tp.Render()
	require.NoError(t, err)

	output := tio.OutBuf.String()
	assert.Contains(t, output, "[H:NAME]")
	// Data should still render (default styles)
	assert.Contains(t, output, "web")
	assert.Contains(t, output, "running")
}
