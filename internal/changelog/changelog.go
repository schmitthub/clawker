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

	"github.com/Masterminds/semver/v3"

	"github.com/schmitthub/clawker/internal/state"
)

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
//   - Otherwise: GETs the curated CHANGELOG.md (ChangelogURL) with the
//     caller-supplied client, parses it, returns the entries gained in
//     (cursor, current] (newest first), and advances the cursor to current.
//
// current is the running-binary version string; it is parsed here (v-tolerant),
// and an unparseable current — e.g. a non-release "DEV" build — returns an error
// so the caller shows nothing. The request is context-aware (cancel ctx to abort)
// and bounded by the supplied client's timeout; a non-200 is an error. A nil st
// is a programming error (the caller wires state) and returns an error. When the
// cursor advance fails after a successful fetch, the gained entries are still
// returned so the teaser can render.
func CheckForChanges(ctx context.Context, client *http.Client, st state.StateStore, current string) ([]Entry, error) {

	if st == nil {
		return nil, fmt.Errorf("state: CheckForChanges: nil StateStore")
	}

	cv, err := semver.NewVersion(current)
	if err != nil {
		return nil, err
	}

	cursor, err := semver.NewVersion(st.State().LastSeenChangelog)
	if err != nil {
		// First run of a changelog-aware binary: the cursor is empty (or an
		// unparseable leftover). Seed it at current and show nothing — the cursor
		// becomes the lower bound for the next run.
		if err := st.SetLastSeenChangelog(cv.String()); err != nil {
			return nil, fmt.Errorf("seeding changelog cursor: %w", err)
		}
		return nil, nil
	}

	entries, err := getChangelogEntries(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("getting changelog entries: %w", err)
	}

	gained, err := between(entries, cursor, cv)
	if err != nil {
		return nil, err
	}

	if err := st.SetLastSeenChangelog(cv.String()); err != nil {
		return gained, fmt.Errorf("advancing changelog cursor: %w", err)
	}
	return gained, nil
}

// getChangelogEntries GETs the curated CHANGELOG.md (ChangelogURL) with the
// supplied client and parses it into entries (newest-first, as authored). The
// request is context-aware and bounded by the client's own timeout; a non-200 is
// an error. It is the package's only network hop — CheckForChanges owns the
// cursor logic wrapped around it.
func getChangelogEntries(ctx context.Context, client *http.Client) ([]Entry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ChangelogURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
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

	return entries, nil
}

// between returns the entries with lo < version <= hi (semver comparison), in
// the same order as the input slice. It is the cursor range query: a
// v0.5.0→v0.12.0 jump returns every gained entry; v0.11.0→v0.12.0 returns one.
// lo and hi are already-parsed versions, so the bound check is a direct
// (*Version).Compare — no constraint string to build and re-parse. Each entry's
// Version was validated as a full semver by the parser, so a parse failure here
// is unexpected and surfaced as an error rather than silently skipped.
func between(entries []Entry, lo, hi *semver.Version) ([]Entry, error) {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		v, err := semver.NewVersion(e.Version)
		if err != nil {
			return nil, fmt.Errorf("parsing changelog entry version %q: %w", e.Version, err)
		}
		if v.Compare(lo) > 0 && v.Compare(hi) <= 0 { // lo < v <= hi
			out = append(out, e)
		}
	}
	return out, nil
}
