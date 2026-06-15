// Package changelog fetches the curated CHANGELOG.md (Keep a Changelog format)
// and surfaces the entries gained since a cursor version. The single exported
// entry point is CheckForChanges (changes.go); Entry is the parsed unit. The
// parser (parse.go) and the cursor range query (between) are pure, unexported
// helpers — they operate on bytes/slices and the package keeps them internal
// because nothing outside the package composes them independently.
package changelog

import "github.com/Masterminds/semver/v3"

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
