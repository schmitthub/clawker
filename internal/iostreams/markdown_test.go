package iostreams

import (
	"regexp"
	"strings"
	"testing"
)

// sampleChangelogBody mirrors the production teaser body: arbitrary flat
// Keep-a-Changelog bullets (no "### " subsections — those render poorly), with
// an inline `code` span and an inline link to exercise both styling paths.
const sampleChangelogBody = "- **Added: Command aliases.** Define shortcuts with `clawker`. [docs](https://docs.clawker.dev/aliases)\n" +
	"- **Added: Worktree shortcut.** Spin up a container on a new branch.\n" +
	"- **Fixed: Host repo masks.** Worktree containers protect the host repository."

// TestRenderMarkdown_NoColor asserts the no-color path renders the body content
// (bullets, inline code, link targets) as plain text with no ANSI escape codes.
// IOStreams from Test() is non-TTY, so ColorEnabled() is false and the ASCII
// base style is selected.
func TestRenderMarkdown_NoColor(t *testing.T) {
	ios, _, _, _ := Test()

	out := ios.RenderMarkdown(sampleChangelogBody)

	if strings.Contains(out, "\x1b[") {
		t.Errorf("no-color render leaked ANSI escape codes:\n%q", out)
	}
	for _, want := range []string{"Command aliases", "Host repo masks", "clawker", "https://docs.clawker.dev/aliases"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q in:\n%s", want, out)
		}
	}
}

// TestRenderMarkdown_Color asserts the colored path emits ANSI styling, keeps
// the body content, and pads no line to a wrap width. WordWrap(0) is deliberate:
// hard-wrapping would balloon the output with trailing-space padding without
// buying any list indent (glamour drops wrapped top-level continuations to
// column 0 regardless), so no rendered line should end in a space.
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
			t.Errorf("rendered line padded with trailing space (WordWrap should be 0):\n%q", out)
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

	plain := stripANSI(ios.RenderMarkdown("run `clawker` now"))

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
