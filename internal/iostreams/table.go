package iostreams

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// TableStyleFunc renders a cell's text with custom styling.
// Used by tui.TablePrinter to override default table styles without importing lipgloss.
type TableStyleFunc func(string) string

// TableStyleOverrides holds optional per-cell style overrides for table rendering.
// Zero value means "use default". When set, the override function controls visual
// styling (color, bold, etc.) while consistent cell padding is always applied by
// the base style.
type TableStyleOverrides struct {
	Header  TableStyleFunc // header row cells (default: TableHeaderStyle)
	Primary TableStyleFunc // first column cells (default: TablePrimaryColumnStyle)
	Cell    TableStyleFunc // all other cells (default: plain with padding)
}

// RenderStyledTable renders a styled table string using lipgloss/table.
// Headers are rendered as uppercase. The first column uses brand color.
// All borders are disabled. Column widths are auto-sized to fit termWidth.
// Optional overrides replace the default cell styles.
func (ios *IOStreams) RenderStyledTable(headers []string, rows [][]string, overrides *TableStyleOverrides) string {
	termWidth := ios.TerminalWidth()

	// Uppercase headers for visual distinction.
	upperHeaders := make([]string, len(headers))
	for i, h := range headers {
		upperHeaders[i] = strings.ToUpper(h)
	}

	cellStyle := lipgloss.NewStyle().Padding(0, 1)
	headerStyle := TableHeaderStyle.Padding(0, 1)
	primaryStyle := TablePrimaryColumnStyle.Padding(0, 1).Align(lipgloss.Left)

	t := table.New().
		Headers(upperHeaders...).
		Rows(rows...).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderHeader(false).
		BorderColumn(false).
		BorderRow(false).
		Width(termWidth).
		StyleFunc(func(row, col int) lipgloss.Style {
			if overrides != nil {
				if fn := overrideFor(overrides, row, col); fn != nil {
					// Wrap the override func as a lipgloss StyleFunc transform.
					// Padding is applied by the base style; the override controls color/bold/etc.
					return cellStyle.Transform(fn)
				}
			}
			switch {
			case row == table.HeaderRow:
				return headerStyle
			case col == 0:
				return primaryStyle
			default:
				return cellStyle
			}
		})

	return t.String()
}

// overrideFor returns the matching override function for a cell, or nil.
func overrideFor(o *TableStyleOverrides, row, col int) TableStyleFunc {
	switch {
	case row == table.HeaderRow && o.Header != nil:
		return o.Header
	case row != table.HeaderRow && col == 0 && o.Primary != nil:
		return o.Primary
	case row != table.HeaderRow && col != 0 && o.Cell != nil:
		return o.Cell
	default:
		return nil
	}
}
