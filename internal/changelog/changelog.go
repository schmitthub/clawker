// Package changelog parses and transforms the curated CHANGELOG.md (Keep a
// Changelog format). The core Parse/Between/ForVersion functions are pure: they
// operate entirely on caller-supplied bytes and perform no I/O. Network fetch
// (fetch.go) and the fetch+cache+TTL+parse orchestration (loader.go) are kept
// in separate files; the parser/transformer here never imports net/http or os.
//
// The typical flow: a Loader fetches the CHANGELOG.md content over the network
// (caching it on disk), Parse turns the bytes into entries, and Between/
// ForVersion filter them.
package changelog

import "github.com/schmitthub/clawker/internal/semver"

// Entry is one curated changelog version section, parsed from CHANGELOG.md.
type Entry struct {
	Version string // "0.12.2" (bare, no leading v) — the semver anchor
	Date    string // "2026-06-11"
	Tag     string // "feature" | "fix" | "breaking" | "perf" | "changed" — from metadata, else derived from the ### subsection
	Title   string // first headline line of the body (without the leading bullet/bold markers)
	Body    string // markdown body for the entry, rendered verbatim by the CLI
	Docs    string // optional docs URL from metadata
}

// Parse parses raw CHANGELOG.md bytes (Keep a Changelog format) into version
// entries, newest-first (file order — the file is authored newest-first).
// Non-semver version sections (e.g. "## [Unreleased]") are skipped.
func Parse(raw []byte) ([]Entry, error) {
	return parse(string(raw))
}

// Between returns the entries with lo < version <= hi (semver comparison), in
// the same order as the input slice. It is the cursor range query: a
// v0.5.0→v0.12.0 jump returns every gained entry; v0.11.0→v0.12.0 returns one.
// Either bound may be passed with or without a leading "v". It does not
// re-parse — callers pass already-parsed entries.
func Between(entries []Entry, lo, hi string) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if semver.CompareStrings(e.Version, lo) > 0 && semver.CompareStrings(e.Version, hi) <= 0 {
			out = append(out, e)
		}
	}
	return out
}

// ForVersion returns the single entry whose Version equals v (with or without a
// leading "v"). The bool is false when no curated entry exists for that version.
// It does not re-parse — callers pass already-parsed entries.
func ForVersion(entries []Entry, v string) (Entry, bool) {
	for _, e := range entries {
		if semver.CompareStrings(e.Version, v) == 0 {
			return e, true
		}
	}
	return Entry{}, false
}
