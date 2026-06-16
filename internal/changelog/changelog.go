// Package changelog fetches the curated CHANGELOG.md (Keep a Changelog format)
// and surfaces the entries gained since a cursor version. The single exported
// entry point is CheckForChanges (changelog.go); Entry is the parsed unit. The
// parser (parse.go) and the cursor range query (between) are pure, unexported
// helpers — they operate on bytes/slices and the package keeps them internal
// because nothing outside the package composes them independently.
package changelog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/schmitthub/clawker/internal/state"
)

// fetchTimeout bounds the CHANGELOG.md request independently of the caller's
// context, matching internal/update's httpTimeout.
const fetchTimeout = 5 * time.Second

// Entry is one curated changelog version section, parsed from CHANGELOG.md.
// A release is a set of changes of mixed kinds spanning many merged PRs, so an
// Entry carries no single classifying kind or headline: the whole
// Keep-a-Changelog body — section headings, bullets, and inline links — is the
// unit, rendered as markdown at display time.
type Entry struct {
	Version string // "0.12.2" (bare, no leading v) — the semver anchor
	Date    string // "2026-06-11"
	Body    string // the Keep-a-Changelog markdown body (### sections + bullets), rendered verbatim
}

// CheckForChanges owns the show-once changelog cursor end to end. It reads the
// cursor from CLI state and:
//
//   - First run (no cursor, or an unparseable one): seeds the cursor at current
//     and returns nil — there is NO catch-up backfill across a changelog-blind
//     upgrade; the cursor IS "last seen" from here on.
//   - Otherwise: GETs the curated CHANGELOG.md (ChangelogURL), parses it,
//     returns the entries gained in (cursor, current] (newest first), and
//     advances the cursor to current.
//
// The request is context-aware (cancel ctx to abort) and bounded by
// fetchTimeout; a non-200 is an error.
//
// This function is only ever called when notifications are NOT suppressed, so
// it always advances the cursor — there is no persist gate. A nil st (state
// store unavailable) is a silent no-op. The cursor write is best-effort: a
// write failure is returned for the caller to log, but any gained entries are
// still returned so the teaser can render.
func CheckForChanges(ctx context.Context, st state.State, current *semver.Version) ([]Entry, error) {
	if st == nil {
		return nil, nil
	}

	cursor, err := semver.NewVersion(st.LastSeenChangelog())
	if err != nil {
		// First run of a changelog-aware binary: the cursor is empty (or an
		// unparseable leftover). Seed it at current and show nothing — the cursor
		// becomes the lower bound for the next run.
		if err := st.SetLastSeenChangelog(current.String()); err != nil {
			return nil, fmt.Errorf("seeding changelog cursor: %w", err)
		}
		return nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ChangelogURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := (&http.Client{Timeout: fetchTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching changelog: %s returned %d", ChangelogURL, resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading changelog response: %w", err)
	}

	entries, err := parse(string(raw))
	if err != nil {
		return nil, err
	}
	gained := between(entries, cursor, current)

	if err := st.SetLastSeenChangelog(current.String()); err != nil {
		return gained, fmt.Errorf("advancing changelog cursor: %w", err)
	}
	return gained, nil
}

// between returns the entries with lo < version <= hi (semver comparison), in
// the same order as the input slice. It is the cursor range query: a
// v0.5.0→v0.12.0 jump returns every gained entry; v0.11.0→v0.12.0 returns one.
// Each entry's Version was validated as a full semver by the parser, so a parse
// failure here is not expected; such an entry is skipped defensively.
func between(entries []Entry, lo, hi *semver.Version) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		v, err := semver.NewVersion(e.Version)
		if err != nil {
			continue
		}
		if v.Compare(lo) > 0 && v.Compare(hi) <= 0 {
			out = append(out, e)
		}
	}
	return out
}
