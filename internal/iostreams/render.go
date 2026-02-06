package iostreams

import (
	"fmt"
	"strings"
)

// KeyValuePair represents a label-value pair for RenderKeyValueBlock.
type KeyValuePair struct {
	Key   string
	Value string
}

// RenderHeader writes a styled header to Out.
// With colors: bold primary title, optional muted subtitle.
// Without colors: "TITLE" or "TITLE — subtitle".
func (ios *IOStreams) RenderHeader(title string, subtitle ...string) error {
	cs := ios.ColorScheme()

	if cs.Enabled() {
		header := TitleStyle.Render(title)
		if len(subtitle) > 0 && subtitle[0] != "" {
			header += " " + SubtitleStyle.Render(subtitle[0])
		}
		_, err := fmt.Fprintln(ios.Out, header)
		return err
	}

	if len(subtitle) > 0 && subtitle[0] != "" {
		_, err := fmt.Fprintf(ios.Out, "%s — %s\n", title, subtitle[0])
		return err
	}
	_, err := fmt.Fprintln(ios.Out, title)
	return err
}

// RenderDivider writes a horizontal divider line to Out.
// Uses the terminal width for the divider length.
func (ios *IOStreams) RenderDivider() error {
	width := ios.TerminalWidth()
	cs := ios.ColorScheme()

	divider := strings.Repeat("─", width)
	if cs.Enabled() {
		divider = DividerStyle.Render(divider)
	}
	_, err := fmt.Fprintln(ios.Out, divider)
	return err
}

// RenderLabeledDivider writes a divider with a centered label to Out.
// Example: "──── Section ────"
func (ios *IOStreams) RenderLabeledDivider(label string) error {
	width := ios.TerminalWidth()
	cs := ios.ColorScheme()

	labelLen := len(label)
	if labelLen+4 >= width {
		// Label too long for divider, just render a plain divider
		return ios.RenderDivider()
	}

	leftWidth := (width - labelLen - 2) / 2
	rightWidth := width - labelLen - 2 - leftWidth

	left := strings.Repeat("─", leftWidth)
	right := strings.Repeat("─", rightWidth)

	var err error
	if cs.Enabled() {
		_, err = fmt.Fprintln(ios.Out, DividerStyle.Render(left)+" "+cs.Muted(label)+" "+DividerStyle.Render(right))
	} else {
		_, err = fmt.Fprintf(ios.Out, "%s %s %s\n", left, label, right)
	}
	return err
}

// RenderBadge writes a badge-styled label to Out.
// With colors: styled badge (default BadgeStyle or custom render function).
// Without colors: [TEXT]
func (ios *IOStreams) RenderBadge(text string, render ...func(string) string) error {
	cs := ios.ColorScheme()

	var err error
	if cs.Enabled() {
		if len(render) > 0 {
			_, err = fmt.Fprintln(ios.Out, render[0](text))
		} else {
			_, err = fmt.Fprintln(ios.Out, BadgeStyle.Render(text))
		}
	} else {
		_, err = fmt.Fprintf(ios.Out, "[%s]\n", text)
	}
	return err
}

// RenderKeyValue writes a single key: value line to Out.
// With colors: muted key, bright value.
// Without colors: "key: value"
func (ios *IOStreams) RenderKeyValue(label, value string) error {
	cs := ios.ColorScheme()

	var err error
	if cs.Enabled() {
		_, err = fmt.Fprintln(ios.Out, LabelStyle.Render(label+":")+
			" "+ValueStyle.Render(value))
	} else {
		_, err = fmt.Fprintf(ios.Out, "%s: %s\n", label, value)
	}
	return err
}

// RenderKeyValueBlock writes multiple key-value pairs with aligned colons to Out.
// Returns without output if no pairs are provided.
func (ios *IOStreams) RenderKeyValueBlock(pairs ...KeyValuePair) error {
	if len(pairs) == 0 {
		return nil
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
		var err error
		if cs.Enabled() {
			key := LabelStyle.Width(maxKeyLen + 1).Render(p.Key + ":")
			val := ValueStyle.Render(p.Value)
			_, err = fmt.Fprintln(ios.Out, key+" "+val)
		} else {
			_, err = fmt.Fprintf(ios.Out, "%-*s %s\n", maxKeyLen+1, p.Key+":", p.Value)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// RenderStatus writes a status indicator with a label to Out.
// Uses StatusIndicator to select the appropriate icon and color.
func (ios *IOStreams) RenderStatus(label, status string) error {
	cs := ios.ColorScheme()

	style, symbol := StatusIndicator(status)
	statusLabel := strings.ToUpper(status)

	var err error
	if cs.Enabled() {
		_, err = fmt.Fprintln(ios.Out, label+" "+style.Render(symbol+" "+statusLabel))
	} else {
		_, err = fmt.Fprintf(ios.Out, "%s %s\n", label, statusLabel)
	}
	return err
}

// RenderEmptyState writes an empty state message to Out.
// It writes to Out (not ErrOut) because it is a structural data render —
// it replaces where table data would normally appear.
// Use PrintEmpty() for status notifications to ErrOut.
// With colors: muted italic text. Without colors: plain text.
func (ios *IOStreams) RenderEmptyState(message string) error {
	cs := ios.ColorScheme()

	if cs.Enabled() {
		_, err := fmt.Fprintln(ios.Out, EmptyStateStyle.Render(message))
		return err
	}
	_, err := fmt.Fprintln(ios.Out, message)
	return err
}

// RenderError writes a styled error to ErrOut.
// With colors: red ✗ prefix.
// Without colors: "Error: message"
// Does nothing if err is nil.
func (ios *IOStreams) RenderError(err error) error {
	if err == nil {
		return nil
	}

	cs := ios.ColorScheme()

	var werr error
	if cs.Enabled() {
		_, werr = fmt.Fprintln(ios.ErrOut, cs.Red("✗")+" "+cs.Error(err.Error()))
	} else {
		_, werr = fmt.Fprintf(ios.ErrOut, "Error: %s\n", err.Error())
	}
	return werr
}
