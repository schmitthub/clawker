package iostreams

import (
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/muesli/termenv"
)

// markdownWrapWidth caps rendered markdown at a readable prose width so wide
// terminals don't wrap changelog and help text edge-to-edge.
const markdownWrapWidth = 80

// RenderMarkdown renders a markdown string to a styled, width-wrapped terminal
// string using a compact glamour style. The compact style strips glamour's
// default document margin and block padding so short snippets (changelog
// teasers, help bodies) render inline without the page-document look glamour
// uses by default. Color profile and dark/light theme follow this IOStreams'
// own capability detection rather than glamour's global termenv probe, so the
// SetColorEnabled test override and NO_COLOR are honored.
//
// Rendering is best-effort: on any error the raw input is returned unchanged so
// callers always have printable text.
func (s *IOStreams) RenderMarkdown(body string) string {
	width := min(s.TerminalWidth(), markdownWrapWidth)
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(compactMarkdownStyle(s.TerminalTheme(), s.ColorEnabled())),
		glamour.WithColorProfile(s.markdownColorProfile()),
		// Wrap on word boundaries at the readable width (terminal width, capped
		// to markdownWrapWidth) using glamour's own wrapper. glamour right-pads
		// wrapped lines out to the wrap width with fill spaces; that fill is
		// invisible in the terminal.
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return body
	}
	out, err := r.Render(body)
	if err != nil {
		return body
	}
	return out
}

// markdownColorProfile maps this IOStreams' color capabilities to a termenv
// profile for glamour. A disabled color scheme (NO_COLOR, non-TTY, explicit
// override) collapses to Ascii so the rendered output carries structure but no
// escape codes.
func (s *IOStreams) markdownColorProfile() termenv.Profile {
	switch {
	case !s.ColorEnabled():
		return termenv.Ascii
	case s.IsTrueColorSupported():
		return termenv.TrueColor
	case s.Is256ColorSupported():
		return termenv.ANSI256
	default:
		return termenv.ANSI
	}
}

// compactMarkdownStyle derives a margin-free variant of a glamour built-in
// style. When color is disabled it builds on the ASCII (no-escape-code) base so
// the output is plain text; otherwise it uses the dark/light style for the
// detected theme. Either base then has its document margin, leading/trailing
// document blank lines, and literal "### " heading prefixes stripped so a
// changelog body reads as tight, plain-headed bullets instead of a full
// rendered document.
func compactMarkdownStyle(theme string, colorEnabled bool) ansi.StyleConfig {
	var cfg ansi.StyleConfig
	switch {
	case !colorEnabled:
		cfg = styles.ASCIIStyleConfig
	case theme == "light":
		cfg = styles.LightStyleConfig
	default:
		cfg = styles.DarkStyleConfig
	}

	noMargin := uint(0)
	cfg.Document.Margin = &noMargin
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""

	// Drop the "## "/"### " literal prefixes; headings stay bold + colored.
	cfg.H1.Prefix = ""
	cfg.H1.Suffix = ""
	cfg.H1.BackgroundColor = nil
	cfg.H2.Prefix = ""
	cfg.H3.Prefix = ""
	cfg.H4.Prefix = ""
	cfg.H5.Prefix = ""
	cfg.H6.Prefix = ""

	// Blockquote: glamour v1 reserves Indent columns (1) when wrapping quote
	// content but then prefixes each line with the two-column "│ " token and
	// repeats the token Indent times, so full-width lines overflow by one
	// column and the trailing reflow orphans their last word onto a bare,
	// unprefixed line. A single-column token makes the reserved width match
	// the rendered width exactly. "▌" occupies the left half of its cell, so
	// the quote bar keeps a visual gap without costing a second column.
	blockQuoteToken := "▌"
	cfg.BlockQuote.IndentToken = &blockQuoteToken

	// Inline code: the built-in style pads with a leading/trailing space and a
	// background block, which renders as stray spaces around each `code` span.
	// Drop the padding and background; the foreground color alone marks it.
	cfg.Code.Prefix = ""
	cfg.Code.Suffix = ""
	cfg.Code.BackgroundColor = nil

	return cfg
}
