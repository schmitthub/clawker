package changelog

import (
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Parse splits raw Keep-a-Changelog markdown into version entries. Each entry
// begins at a "## [x.y.z] - YYYY-MM-DD" header; everything up to the next
// version header (or the trailing link-reference block) is the entry body,
// preserved as markdown. HTML-comment lines are dropped so they never render.
//
// Exported so tooling can render a local CHANGELOG.md through the same path the
// teaser uses (see the changelog-preview make target); CheckForChanges is the
// fetch+cursor entry point that callers normally use.
func Parse(raw string) ([]Entry, error) {
	var entries []Entry
	var cur *Entry
	var body []string

	flush := func() {
		if cur == nil {
			return
		}
		cur.Body = strings.Trim(strings.Join(body, "\n"), "\n")
		entries = append(entries, *cur)
	}

	for line := range strings.SplitSeq(raw, "\n") {
		trimmed := strings.TrimSpace(line)

		if version, date, ok := parseVersionHeader(trimmed); ok {
			flush()
			cur = &Entry{Version: version, Date: date}
			body = nil
			continue
		}

		if cur == nil {
			continue // preamble before the first version header
		}

		if isHTMLComment(trimmed) {
			continue // comments (incl. legacy "<!-- clawker: -->") never render
		}

		// A link-reference definition block ("[0.12.3]: https://...") at the
		// file tail closes the current entry.
		if isLinkReference(trimmed) {
			flush()
			cur = nil
			continue
		}

		body = append(body, line)
	}
	flush()

	return entries, nil
}

// parseVersionHeader extracts version + date from "## [x.y.z] - YYYY-MM-DD".
// The date is optional ("## [Unreleased]" style sections yield ok=false because
// the bracketed token is not a semver).
func parseVersionHeader(line string) (version, date string, ok bool) {
	if !strings.HasPrefix(line, versionHeaderPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(line, versionHeaderPrefix)
	bracket, after, ok := strings.Cut(rest, "]")
	if !ok {
		return "", "", false
	}
	// The bracket token must be a full "x.y.z" semver (tolerating a leading
	// "v"); StrictNewVersion rejects both a non-semver like "Unreleased" and a
	// partial like "0.12" (it requires all three components), so this skips them.
	version = strings.TrimPrefix(strings.TrimSpace(bracket), "v")
	if _, err := semver.StrictNewVersion(version); err != nil {
		return "", "", false
	}
	// The remainder is "- YYYY-MM-DD" (or empty for an undated header). Split on
	// the first dash and take what follows; a date's own dashes are left intact.
	if _, tail, found := strings.Cut(after, dateDash); found {
		date = strings.TrimSpace(tail)
	}
	return version, date, true
}

// isHTMLComment reports whether a trimmed line is a single-line HTML comment
// ("<!-- ... -->"). Such lines (including the legacy "<!-- clawker: ... -->"
// metadata convention) are dropped from the body so they never render.
func isHTMLComment(line string) bool {
	return strings.HasPrefix(line, htmlCommentPrefix) && strings.HasSuffix(line, htmlCommentSuffix)
}

// isLinkReference reports whether a line is a markdown link-reference
// definition ("[label]: url"), used at the file tail for version links.
func isLinkReference(line string) bool {
	if !strings.HasPrefix(line, "[") {
		return false
	}
	close := strings.IndexByte(line, ']')
	return close > 0 && strings.HasPrefix(line[close+1:], ":")
}
