package iostreams

import (
	"regexp"
	"strings"
	"testing"
)

// sampleChangelogBody mirrors the production teaser body: arbitrary flat
// Keep-a-Changelog bullets (no "### " subsections — those render poorly) with an
// inline `code` span. Changelog entries carry no URLs (the teaser can't make
// them clickable and they wrap badly in a terminal), so the fixture has none.
const sampleChangelogBody = "- **Added: Command aliases.** Define shortcuts with `clawker` for the current project.\n" +
	"- **Added: Worktree shortcut.** Spin up a container on a new branch.\n" +
	"- **Fixed: Host repo masks.** Worktree containers protect the host repository."

// TestRenderMarkdown_NoColor asserts the no-color path renders the body content
// (bullets, inline code) as plain text with no ANSI escape codes. IOStreams from
// Test() is non-TTY, so ColorEnabled() is false and the ASCII base style is
// selected.
func TestRenderMarkdown_NoColor(t *testing.T) {
	ios, _, _, _ := Test()

	out := ios.RenderMarkdown(sampleChangelogBody)

	if strings.Contains(out, "\x1b[") {
		t.Errorf("no-color render leaked ANSI escape codes:\n%q", out)
	}
	for _, want := range []string{"Command aliases", "Host repo masks", "clawker"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q in:\n%s", want, out)
		}
	}
}

// TestRenderMarkdown_Color asserts the colored path emits ANSI styling and keeps
// the body content. glamour wraps at the readable width and right-pads wrapped
// lines, but the fill is wrapped in SGR escapes, so a rendered line never ends
// in a bare space — that is what the trailing-space check guards.
func TestRenderMarkdown_Color(t *testing.T) {
	ios, _, _, _ := Test()
	ios.SetColorEnabled(true)

	out := ios.RenderMarkdown(sampleChangelogBody)

	if !strings.Contains(out, "\x1b[") {
		t.Errorf("color render produced no ANSI styling:\n%q", out)
	}
	if !strings.Contains(out, "Command aliases") {
		t.Errorf("color render missing body content:\n%q", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(line, " ") {
			t.Errorf("rendered line ends in a bare trailing space (pad fill must stay wrapped in SGR):\n%q", out)
			break
		}
	}
}

// TestRenderMarkdown_InlineCodeNoPadding pins the compact style's inline-code
// fix: glamour's default Code style wraps a span in a leading/trailing space
// (the " code " chip), which reads as stray padding in a teaser. The compact
// style clears that, so the rendered span hugs the surrounding prose with no
// doubled spaces.
func TestRenderMarkdown_InlineCodeNoPadding(t *testing.T) {
	ios, _, _, _ := Test()
	ios.SetColorEnabled(true)

	// glamour right-pads wrapped lines out to the wrap width with trailing fill
	// spaces (invisible in a terminal). Strip per-line trailing space so this
	// targets padding *around the inline `code` span*, not the line fill.
	var sb strings.Builder
	for _, l := range strings.Split(stripANSI(ios.RenderMarkdown("run `clawker` now")), "\n") {
		sb.WriteString(strings.TrimRight(l, " "))
		sb.WriteByte('\n')
	}
	plain := sb.String()

	if !strings.Contains(plain, "run clawker now") {
		t.Errorf("inline code not rendered tight (want single spaces around it):\n%q", plain)
	}
	if strings.Contains(plain, "  ") {
		t.Errorf("inline code left doubled-space padding:\n%q", plain)
	}
}

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI removes SGR escape sequences so a test can assert on the visible
// text alone.
func stripANSI(s string) string {
	return strings.TrimSpace(ansiSeq.ReplaceAllString(s, ""))
}

// TestRenderMarkdown_EmptyBody guards against a panic/garbage on empty input.
func TestRenderMarkdown_EmptyBody(t *testing.T) {
	ios, _, _, _ := Test()
	if got := ios.RenderMarkdown(""); strings.TrimSpace(got) != "" {
		t.Errorf("empty body rendered non-empty: %q", got)
	}
}

// TestRenderMarkdown_WrapsAtTerminalWidth guards the regression where the
// renderer emitted full-length lines (glamour WithWordWrap(0)) and leaned on the
// terminal's blind soft-wrap: a long line must be word-wrapped to within the
// terminal width. Test() is non-TTY, so TerminalWidth() is the default fallback.
func TestRenderMarkdown_WrapsAtTerminalWidth(t *testing.T) {
	ios, _, _, _ := Test()

	width := ios.TerminalWidth()
	long := strings.Repeat("word ", width) // far wider than one line

	out := strings.TrimRight(ios.RenderMarkdown(long), "\n")
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected long input to wrap to multiple lines, got %d:\n%s", len(lines), out)
	}
	for i, l := range lines {
		if w := len([]rune(stripANSI(l))); w > width {
			t.Errorf("line %d exceeds terminal width %d (visible width %d): %q", i, width, w, l)
		}
	}
}
