package clawker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/changelog"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// TestPreviewLatestChangelogEntry renders the newest entry of the repo's real
// CHANGELOG.md through the actual teaser renderer (printChangelogTeaser), so you
// can eyeball how a release section — alerts, bullets, code spans — looks in a
// terminal before shipping it. This is the only faithful way to preview
// glamour's output (e.g. whether a `> [!IMPORTANT]` block renders as a callout
// or leaks its literal token); it drives the same code path a user hits
// post-upgrade rather than a hand-rolled copy of it.
//
// Opt-in so it never adds noise to a normal package run:
//
//	CLAWKER_PREVIEW_CHANGELOG=1 go test ./internal/clawker/ \
//	    -run TestPreviewLatestChangelogEntry -v
//
// Or via the make target: `make changelog-preview`. Uses iostreams.System()
// with color forced on, so the host terminal's real color/theme detection
// drives the output (truecolor where available).
func TestPreviewLatestChangelogEntry(t *testing.T) {
	if os.Getenv("CLAWKER_PREVIEW_CHANGELOG") == "" {
		t.Skip("set CLAWKER_PREVIEW_CHANGELOG=1 to preview the latest changelog entry")
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	require.NoError(t, err, "read repo CHANGELOG.md")

	entries, err := changelog.Parse(string(raw))
	require.NoError(t, err)
	require.NotEmpty(t, entries, "CHANGELOG.md has no version entries")

	ios := iostreams.System()
	ios.SetColorEnabled(true)

	// File is authored newest-first, so entries[:1] is the latest release —
	// rendered through the real teaser, identical to the post-upgrade notice.
	printChangelogTeaser(ios, entries[:1])
}
