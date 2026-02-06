package iostreams

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// KeyValuePair represents a label-value pair for RenderKeyValueBlock.
type KeyValuePair struct {
	Key   string
	Value string
}

// RenderHeader writes a styled header to Out.
// With colors: bold primary title, optional muted subtitle.
// Without colors: "TITLE" or "TITLE — subtitle".
func (ios *IOStreams) RenderHeader(title string, subtitle ...string) {
	cs := ios.ColorScheme()

	if cs.Enabled() {
		header := TitleStyle.Render(title)
		if len(subtitle) > 0 && subtitle[0] != "" {
			header += " " + SubtitleStyle.Render(subtitle[0])
		}
		fmt.Fprintln(ios.Out, header)
	} else {
		if len(subtitle) > 0 && subtitle[0] != "" {
			fmt.Fprintf(ios.Out, "%s — %s\n", title, subtitle[0])
		} else {
			fmt.Fprintln(ios.Out, title)
		}
	}
}

// RenderDivider writes a horizontal divider line to Out.
// Uses the terminal width for the divider length.
func (ios *IOStreams) RenderDivider() {
	width := ios.TerminalWidth()
	cs := ios.ColorScheme()

	divider := strings.Repeat("─", width)
	if cs.Enabled() {
		divider = DividerStyle.Render(divider)
	}
	fmt.Fprintln(ios.Out, divider)
}

// RenderLabeledDivider writes a divider with a centered label to Out.
// Example: "──── Section ────"
func (ios *IOStreams) RenderLabeledDivider(label string) {
	width := ios.TerminalWidth()
	cs := ios.ColorScheme()

	labelLen := len(label)
	if labelLen+4 >= width {
		// Label too long for divider, just render a plain divider
		ios.RenderDivider()
		return
	}

	leftWidth := (width - labelLen - 2) / 2
	rightWidth := width - labelLen - 2 - leftWidth

	left := strings.Repeat("─", leftWidth)
	right := strings.Repeat("─", rightWidth)

	if cs.Enabled() {
		fmt.Fprintln(ios.Out, DividerStyle.Render(left)+" "+cs.Muted(label)+" "+DividerStyle.Render(right))
	} else {
		fmt.Fprintf(ios.Out, "%s %s %s\n", left, label, right)
	}
}

// RenderBadge writes a badge-styled label to Out.
// With colors: styled badge (default BadgeStyle or custom).
// Without colors: [TEXT]
func (ios *IOStreams) RenderBadge(text string, style ...lipgloss.Style) {
	cs := ios.ColorScheme()

	if cs.Enabled() {
		s := BadgeStyle
		if len(style) > 0 {
			s = style[0]
		}
		fmt.Fprintln(ios.Out, s.Render(text))
	} else {
		fmt.Fprintf(ios.Out, "[%s]\n", text)
	}
}

// RenderKeyValue writes a single key: value line to Out.
// With colors: muted key, bright value.
// Without colors: "key: value"
func (ios *IOStreams) RenderKeyValue(label, value string) {
	cs := ios.ColorScheme()

	if cs.Enabled() {
		fmt.Fprintln(ios.Out, LabelStyle.Render(label+":")+
			" "+ValueStyle.Render(value))
	} else {
		fmt.Fprintf(ios.Out, "%s: %s\n", label, value)
	}
}

// RenderKeyValueBlock writes multiple key-value pairs with aligned colons to Out.
// Returns without output if no pairs are provided.
func (ios *IOStreams) RenderKeyValueBlock(pairs ...KeyValuePair) {
	if len(pairs) == 0 {
		return
	}

	cs := ios.ColorScheme()

	// Find max key length for alignment
	maxKeyLen := 0
	for _, p := range pairs {
		if len(p.Key) > maxKeyLen {
			maxKeyLen = len(p.Key)
		}
	}

	for _, p := range pairs {
		if cs.Enabled() {
			key := LabelStyle.Width(maxKeyLen + 1).Render(p.Key + ":")
			val := ValueStyle.Render(p.Value)
			fmt.Fprintln(ios.Out, key+" "+val)
		} else {
			fmt.Fprintf(ios.Out, "%-*s %s\n", maxKeyLen+1, p.Key+":", p.Value)
		}
	}
}

// RenderStatus writes a status indicator with a label to Out.
// Uses StatusIndicator to select the appropriate icon and color.
func (ios *IOStreams) RenderStatus(label, status string) {
	cs := ios.ColorScheme()

	style, symbol := StatusIndicator(status)
	statusLabel := strings.ToUpper(status)

	if cs.Enabled() {
		fmt.Fprintln(ios.Out, label+" "+style.Render(symbol+" "+statusLabel))
	} else {
		fmt.Fprintf(ios.Out, "%s %s\n", label, statusLabel)
	}
}

// RenderEmptyState writes an empty state message to Out.
// It writes to Out (not ErrOut) because it is a structural data render —
// it replaces where table data would normally appear.
// Use PrintEmpty() for status notifications to ErrOut.
// With colors: muted italic text. Without colors: plain text.
func (ios *IOStreams) RenderEmptyState(message string) {
	cs := ios.ColorScheme()

	if cs.Enabled() {
		fmt.Fprintln(ios.Out, EmptyStateStyle.Render(message))
	} else {
		fmt.Fprintln(ios.Out, message)
	}
}

// RenderError writes a styled error to ErrOut.
// With colors: red ✗ prefix.
// Without colors: "Error: message"
// Does nothing if err is nil.
func (ios *IOStreams) RenderError(err error) {
	if err == nil {
		return
	}

	cs := ios.ColorScheme()

	if cs.Enabled() {
		fmt.Fprintln(ios.ErrOut, cs.Red("✗")+" "+cs.Error(err.Error()))
	} else {
		fmt.Fprintf(ios.ErrOut, "Error: %s\n", err.Error())
	}
}
