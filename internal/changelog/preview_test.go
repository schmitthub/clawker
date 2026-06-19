package changelog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// TestPreviewLatestChangelogEntry renders the newest entry of the repo's real
// CHANGELOG.md exactly as the show-once teaser does (internal/clawker
// printChangelogTeaser: bold "v<ver> — <date>" header + RenderMarkdown(body)),
// so you can eyeball how a release section — alerts, bullets, inline code —
// actually looks in a terminal before shipping it. It is the only faithful way
// to preview glamour's rendering (e.g. whether a `> [!IMPORTANT]` block renders
// as a callout or leaks its literal token).
//
// Opt-in so it never adds noise to a normal package run:
//
//	CLAWKER_PREVIEW_CHANGELOG=1 go test ./internal/changelog/ \
//	    -run TestPreviewLatestChangelogEntry -v
//
// Uses iostreams.System() with color forced on, so the host terminal's real
// color/theme detection drives the output (truecolor where available).
func TestPreviewLatestChangelogEntry(t *testing.T) {
	if os.Getenv("CLAWKER_PREVIEW_CHANGELOG") == "" {
		t.Skip("set CLAWKER_PREVIEW_CHANGELOG=1 to preview the latest changelog entry")
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	require.NoError(t, err, "read repo CHANGELOG.md")

	entries, err := parse(string(raw))
	require.NoError(t, err)
	require.NotEmpty(t, entries, "CHANGELOG.md has no version entries")

	// File is authored newest-first, so the first parsed entry is the latest.
	latest := entries[0]

	ios := iostreams.System()
	ios.SetColorEnabled(true)
	cs := ios.ColorScheme()

	// Mirror internal/clawker.printChangelogTeaser so the preview matches what a
	// user sees post-upgrade. Kept in sync by eye — the body render (the part
	// that matters for previewing markdown/alerts) is the identical
	// ios.RenderMarkdown call.
	header := "v" + latest.Version
	if latest.Date != "" {
		header += " — " + latest.Date
	}
	fmt.Fprintf(ios.ErrOut, "\n%s What's new in clawker:\n", "📣")
	fmt.Fprintf(ios.ErrOut, "\n%s\n", cs.Bold(header))
	fmt.Fprintln(ios.ErrOut, strings.TrimRight(ios.RenderMarkdown(latest.Body), "\n"))
}
