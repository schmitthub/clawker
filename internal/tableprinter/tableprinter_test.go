package tableprinter

import (
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestTablePrinter_New(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tp := New(tio.IOStreams, "NAME", "STATUS")
	if tp == nil {
		t.Fatal("New returned nil")
	}
	if tp.Len() != 0 {
		t.Errorf("Len() = %d, want 0", tp.Len())
	}
}

func TestTablePrinter_AddRow(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tp := New(tio.IOStreams, "NAME", "STATUS")
	tp.AddRow("web", "running")
	tp.AddRow("db", "stopped")
	if tp.Len() != 2 {
		t.Errorf("Len() = %d, want 2", tp.Len())
	}
}

func TestTablePrinter_Render_PlainMode(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	// Non-TTY by default → plain mode
	tp := New(tio.IOStreams, "NAME", "STATUS", "IMAGE")
	tp.AddRow("web", "running", "nginx:latest")
	tp.AddRow("db", "stopped", "postgres:16")

	if err := tp.Render(); err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	output := tio.OutBuf.String()

	// Plain mode should have tab-separated values
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + 2 rows), got %d: %q", len(lines), output)
	}

	// Header line should contain all headers
	if !strings.Contains(lines[0], "NAME") || !strings.Contains(lines[0], "STATUS") || !strings.Contains(lines[0], "IMAGE") {
		t.Errorf("header line missing columns: %q", lines[0])
	}

	// Data rows should contain values
	if !strings.Contains(lines[1], "web") || !strings.Contains(lines[1], "running") {
		t.Errorf("row 1 missing values: %q", lines[1])
	}
	if !strings.Contains(lines[2], "db") || !strings.Contains(lines[2], "stopped") {
		t.Errorf("row 2 missing values: %q", lines[2])
	}
}

func TestTablePrinter_Render_PlainMode_NoANSI(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tp := New(tio.IOStreams, "NAME", "STATUS")
	tp.AddRow("web", "running")

	if err := tp.Render(); err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	output := tio.OutBuf.String()

	// Plain mode should not contain ANSI escape sequences
	if strings.Contains(output, "\033[") {
		t.Errorf("plain mode output contains ANSI escapes: %q", output)
	}
}

func TestTablePrinter_Render_StyledMode(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(80, 24)

	tp := New(tio.IOStreams, "NAME", "STATUS")
	tp.AddRow("web", "running")

	if err := tp.Render(); err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	output := tio.OutBuf.String()

	// Styled mode should contain the data
	if !strings.Contains(output, "NAME") {
		t.Errorf("styled output missing header NAME: %q", output)
	}
	if !strings.Contains(output, "web") {
		t.Errorf("styled output missing value 'web': %q", output)
	}

	// Styled mode should contain a divider (─)
	if !strings.Contains(output, "─") {
		t.Errorf("styled output missing divider: %q", output)
	}
}

func TestTablePrinter_Render_EmptyTable(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tp := New(tio.IOStreams, "NAME", "STATUS")

	if err := tp.Render(); err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	output := tio.OutBuf.String()
	// Empty table should still render headers
	if !strings.Contains(output, "NAME") {
		t.Errorf("empty table should still have headers: %q", output)
	}
}

func TestTablePrinter_Render_MismatchedColumns(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tp := New(tio.IOStreams, "NAME", "STATUS", "IMAGE")
	tp.AddRow("web", "running") // Missing IMAGE column
	tp.AddRow("db")             // Only NAME

	if err := tp.Render(); err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	output := tio.OutBuf.String()
	// Should not panic or error — missing columns treated as empty
	if !strings.Contains(output, "web") {
		t.Errorf("output missing 'web': %q", output)
	}
	if !strings.Contains(output, "db") {
		t.Errorf("output missing 'db': %q", output)
	}
}

func TestTablePrinter_Render_StyledMode_WidthAware(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(40, 24) // Narrow terminal

	tp := New(tio.IOStreams, "NAME", "STATUS")
	tp.AddRow("very-long-container-name-that-exceeds-width", "running")

	if err := tp.Render(); err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	// Should complete without error (truncation handled internally)
	output := tio.OutBuf.String()
	if output == "" {
		t.Error("output should not be empty")
	}
}

func TestTablePrinter_Render_StyledMode_EmptyRows(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(80, 24)

	tp := New(tio.IOStreams, "NAME", "STATUS")
	// No rows added

	if err := tp.Render(); err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	output := tio.OutBuf.String()
	if !strings.Contains(output, "NAME") {
		t.Errorf("styled empty table should still render headers: %q", output)
	}
	if !strings.Contains(output, "─") {
		t.Errorf("styled empty table should render divider: %q", output)
	}
}

func TestTablePrinter_Render_ExtremelyNarrowWidth(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(5, 24) // Extremely narrow

	tp := New(tio.IOStreams, "NAME")
	tp.AddRow("value")

	if err := tp.Render(); err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	output := tio.OutBuf.String()
	if output == "" {
		t.Error("should produce output even at extreme width")
	}
}

func TestTablePrinter_Render_NoHeaders(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tp := New(tio.IOStreams)

	if err := tp.Render(); err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	output := tio.OutBuf.String()
	if output != "" {
		t.Errorf("no-headers table should produce empty output, got: %q", output)
	}
}

func TestTablePrinter_Render_WritesToOut(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tp := New(tio.IOStreams, "COL")
	tp.AddRow("val")

	if err := tp.Render(); err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	// Should write to Out, not ErrOut
	if tio.OutBuf.String() == "" {
		t.Error("expected output in OutBuf")
	}
	if tio.ErrBuf.String() != "" {
		t.Errorf("expected no output in ErrBuf, got: %q", tio.ErrBuf.String())
	}
}

func TestTablePrinter_Render_StyledMode_Unicode(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tio.SetInteractive(true)
	tio.SetColorEnabled(true)
	tio.SetTerminalSize(40, 24)

	tp := New(tio.IOStreams, "NAME", "STATUS")
	tp.AddRow("日本語テスト", "running")
	tp.AddRow("emoji-🎉-test", "stopped")

	if err := tp.Render(); err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	output := tio.OutBuf.String()
	// Should produce valid output without panicking on multi-byte content
	if output == "" {
		t.Error("expected non-empty output with Unicode content")
	}
}
