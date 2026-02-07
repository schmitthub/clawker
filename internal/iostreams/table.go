package iostreams

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/lipgloss"

	"github.com/schmitthub/clawker/internal/text"
)

// TablePrinter renders tabular data to IOStreams.Out.
// When the output is a TTY with colors enabled, it renders styled headers
// and a divider. When piped or in non-TTY mode, it uses plain tabwriter
// for machine-friendly output.
type TablePrinter struct {
	ios     *IOStreams
	headers []string
	rows    [][]string
}

// NewTablePrinter creates a new table printer with the given column headers.
// The table writes to ios.Out when Render() is called.
func (ios *IOStreams) NewTablePrinter(headers ...string) *TablePrinter {
	return &TablePrinter{
		ios:     ios,
		headers: headers,
	}
}

// AddRow adds a data row to the table. If fewer columns are provided than
// headers, missing columns are treated as empty strings.
func (tp *TablePrinter) AddRow(cols ...string) {
	tp.rows = append(tp.rows, cols)
}

// Len returns the number of data rows (not including headers).
func (tp *TablePrinter) Len() int {
	return len(tp.rows)
}

// Render writes the table to the IOStreams output.
// Returns an error if writing fails.
func (tp *TablePrinter) Render() error {
	if len(tp.headers) == 0 {
		return nil
	}

	if tp.ios.IsOutputTTY() && tp.ios.ColorEnabled() {
		return tp.renderStyled()
	}
	return tp.renderPlain()
}

// renderPlain writes a tab-separated table using tabwriter.
func (tp *TablePrinter) renderPlain() error {
	w := tabwriter.NewWriter(tp.ios.Out, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, strings.Join(tp.headers, "\t"))

	for _, row := range tp.rows {
		cols := tp.normalizeRow(row)
		fmt.Fprintln(w, strings.Join(cols, "\t"))
	}

	return w.Flush()
}

// renderStyled writes a styled table with lipgloss formatting.
func (tp *TablePrinter) renderStyled() error {
	width := tp.ios.TerminalWidth()
	numCols := len(tp.headers)

	// Calculate column widths: distribute available space evenly,
	// accounting for gaps between columns.
	gap := 2
	totalGap := gap * (numCols - 1)
	available := width - totalGap
	if available < numCols {
		available = numCols // Minimum 1 char per column
	}
	colWidth := available / numCols

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)
	spacing := strings.Repeat(" ", gap)

	// Render header row
	var headerParts []string
	for _, h := range tp.headers {
		rendered := headerStyle.Width(colWidth).Render(text.Truncate(h, colWidth))
		headerParts = append(headerParts, rendered)
	}
	if _, err := fmt.Fprintln(tp.ios.Out, strings.Join(headerParts, spacing)); err != nil {
		return err
	}

	// Render divider
	var dividerParts []string
	for range tp.headers {
		dividerParts = append(dividerParts, strings.Repeat("─", colWidth))
	}
	divider := DividerStyle.Render(strings.Join(dividerParts, spacing))
	if _, err := fmt.Fprintln(tp.ios.Out, divider); err != nil {
		return err
	}

	// Render data rows
	cellStyle := lipgloss.NewStyle().Width(colWidth)
	for _, row := range tp.rows {
		cols := tp.normalizeRow(row)
		var parts []string
		for _, col := range cols {
			parts = append(parts, cellStyle.Render(text.Truncate(col, colWidth)))
		}
		if _, err := fmt.Fprintln(tp.ios.Out, strings.Join(parts, spacing)); err != nil {
			return err
		}
	}

	return nil
}

// normalizeRow pads or truncates a row to match the number of headers.
func (tp *TablePrinter) normalizeRow(row []string) []string {
	cols := make([]string, len(tp.headers))
	for i := range cols {
		if i < len(row) {
			cols[i] = row[i]
		}
	}
	return cols
}
