package iostreams

import (
	"strings"
	"testing"
)

const sampleChangelogBody = "### Added\n\n" +
	"- **Command aliases.** Define your own shortcuts. [docs](https://docs.clawker.dev/aliases)\n" +
	"- **Worktree shortcut.** Spin up a container on a new branch.\n\n" +
	"### Fixed\n\n" +
	"- **Host repo masks.** Worktree containers protect the host repository."

// TestRenderMarkdown_NoColor asserts the no-color path renders structure
// (headings, bullets, link targets) as plain text with no ANSI escape codes.
// IOStreams from Test() is non-TTY, so ColorEnabled() is false and the ASCII
// base style is selected.
func TestRenderMarkdown_NoColor(t *testing.T) {
	ios, _, _, _ := Test()

	out := ios.RenderMarkdown(sampleChangelogBody)

	if strings.Contains(out, "\x1b[") {
		t.Errorf("no-color render leaked ANSI escape codes:\n%q", out)
	}
	for _, want := range []string{"Added", "Fixed", "Command aliases", "Host repo masks", "https://docs.clawker.dev/aliases"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q in:\n%s", want, out)
		}
	}
	// The compact style drops the literal "### " heading prefix.
	if strings.Contains(out, "### ") {
		t.Errorf("render leaked literal heading prefix:\n%s", out)
	}
}

// TestRenderMarkdown_Color asserts the colored path emits ANSI styling and does
// not pad lines to the terminal width (WordWrap(0)) — width padding would
// balloon the output with styled trailing spaces.
func TestRenderMarkdown_Color(t *testing.T) {
	ios, _, _, _ := Test()
	ios.SetColorEnabled(true)

	out := ios.RenderMarkdown(sampleChangelogBody)

	if !strings.Contains(out, "\x1b[") {
		t.Errorf("color render produced no ANSI styling:\n%q", out)
	}
	// Width padding (the bug WordWrap(0) avoids) fills every line to the wrap
	// width with styled spaces, ballooning the output (~535 bytes tight vs
	// ~8800 padded). A tight render stays well under the threshold.
	if len(out) > 2000 {
		t.Errorf("color render unexpectedly large (%d bytes) — width padding regression?", len(out))
	}
}

// TestRenderMarkdown_EmptyBody guards against a panic/garbage on empty input.
func TestRenderMarkdown_EmptyBody(t *testing.T) {
	ios, _, _, _ := Test()
	if got := ios.RenderMarkdown(""); strings.TrimSpace(got) != "" {
		t.Errorf("empty body rendered non-empty: %q", got)
	}
}
