package tui

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// TablePrinter renders tabular data with TTY-aware styling.
// In styled mode (TTY + color), delegates to iostreams.RenderStyledTable (lipgloss/table).
// In plain mode (non-TTY/piped), uses text/tabwriter for machine-friendly output.
type TablePrinter struct {
	tui       *TUI
	headers   []string
	rows      [][]string
	overrides *iostreams.TableStyleOverrides
}

// NewTable creates a TablePrinter bound to this TUI's IOStreams.
func (t *TUI) NewTable(headers ...string) *TablePrinter {
	return &TablePrinter{
		tui:     t,
		headers: headers,
	}
}

// WithHeaderStyle overrides the header row style. The function receives cell
// text and returns styled text. Pass nil to use the default (TableHeaderStyle).
func (tp *TablePrinter) WithHeaderStyle(fn func(string) string) *TablePrinter {
	tp.ensureOverrides()
	tp.overrides.Header = fn
	return tp
}

// WithPrimaryStyle overrides the first-column style. The function receives cell
// text and returns styled text. Pass nil to use the default (TablePrimaryColumnStyle).
func (tp *TablePrinter) WithPrimaryStyle(fn func(string) string) *TablePrinter {
	tp.ensureOverrides()
	tp.overrides.Primary = fn
	return tp
}

// WithCellStyle overrides the default cell style. The function receives cell
// text and returns styled text. Pass nil to use the default (plain with padding).
func (tp *TablePrinter) WithCellStyle(fn func(string) string) *TablePrinter {
	tp.ensureOverrides()
	tp.overrides.Cell = fn
	return tp
}

// AddRow appends a data row. Missing columns are padded with empty strings;
// extra columns beyond the header count are discarded.
func (tp *TablePrinter) AddRow(cols ...string) {
	tp.rows = append(tp.rows, tp.normalizeRow(cols))
}

// Len returns the number of data rows (excluding the header).
func (tp *TablePrinter) Len() int {
	return len(tp.rows)
}

// Render writes the table to ios.Out. Returns nil if there are no headers.
func (tp *TablePrinter) Render() error {
	if len(tp.headers) == 0 {
		return nil
	}
	ios := tp.tui.ios
	if ios.IsOutputTTY() && ios.ColorEnabled() {
		return tp.renderStyled()
	}
	return tp.renderPlain()
}

// renderPlain writes a tab-separated table using text/tabwriter.
// Machine-friendly output with no ANSI sequences.
func (tp *TablePrinter) renderPlain() error {
	ios := tp.tui.ios
	w := tabwriter.NewWriter(ios.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(tp.headers, "\t"))
	for _, row := range tp.rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	return w.Flush()
}

// renderStyled delegates styled table rendering to iostreams.RenderStyledTable,
// which uses lipgloss/table for per-cell styling (brand color first column,
// secondary uppercase headers) with content-aware column widths.
func (tp *TablePrinter) renderStyled() error {
	output := tp.tui.ios.RenderStyledTable(tp.headers, tp.rows, tp.overrides)
	_, err := fmt.Fprintln(tp.tui.ios.Out, output)
	return err
}

// normalizeRow pads or truncates the row to match the header count.
func (tp *TablePrinter) normalizeRow(cols []string) []string {
	n := len(tp.headers)
	if len(cols) >= n {
		return cols[:n]
	}
	row := make([]string, n)
	copy(row, cols)
	return row
}

// ensureOverrides lazily initializes the overrides struct.
func (tp *TablePrinter) ensureOverrides() {
	if tp.overrides == nil {
		tp.overrides = &iostreams.TableStyleOverrides{}
	}
}
