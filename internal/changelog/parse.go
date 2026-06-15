package changelog

import (
	"strings"

	"github.com/schmitthub/clawker/internal/semver"
)

// parse splits raw Keep-a-Changelog markdown into version entries. Each entry
// begins at a "## [x.y.z] - YYYY-MM-DD" header. The header may be followed by an
// HTML-comment metadata line ("<!-- clawker: tag=... docs=... -->"); everything
// up to the next version header (or a link-reference block) is the entry body.
func parse(raw string) ([]Entry, error) {
	var entries []Entry
	var cur *Entry
	var body []string
	var firstSubsection string

	flush := func() {
		if cur == nil {
			return
		}
		cur.Body = strings.Trim(strings.Join(body, "\n"), "\n")
		if cur.Tag == "" {
			cur.Tag = tagFromSubsection(firstSubsection)
		}
		cur.Title = titleFromBody(cur.Body)
		entries = append(entries, *cur)
	}

	for line := range strings.SplitSeq(raw, "\n") {
		trimmed := strings.TrimSpace(line)

		if version, date, ok := parseVersionHeader(trimmed); ok {
			flush()
			e := Entry{Version: version, Date: date}
			cur = &e
			body = nil
			firstSubsection = ""
			continue
		}

		if cur == nil {
			continue // preamble before the first version header
		}

		if tag, docs, ok := parseMetaComment(trimmed); ok {
			if tag != "" {
				cur.Tag = Tag(tag)
			}
			if docs != "" {
				cur.Docs = docs
			}
			continue // metadata comment is not part of the rendered body
		}

		// A link-reference definition block ("[0.12.3]: https://...") at the
		// file tail closes the current entry.
		if isLinkReference(trimmed) {
			flush()
			cur = nil
			continue
		}

		if firstSubsection == "" && strings.HasPrefix(trimmed, subsectionPrefix) {
			firstSubsection = strings.TrimSpace(strings.TrimPrefix(trimmed, subsectionPrefix))
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
	// "v"); a non-semver like "Unreleased" or a partial like "0.12" is skipped.
	version = strings.TrimPrefix(strings.TrimSpace(bracket), "v")
	if parsed, err := semver.Parse(version); err != nil || !parsed.HasPatch() {
		return "", "", false
	}
	after = strings.TrimSpace(after)
	if sep := strings.Index(after, strings.TrimSpace(dateSeparator)); sep >= 0 {
		date = strings.TrimSpace(after[sep+len(strings.TrimSpace(dateSeparator)):])
	}
	return version, date, true
}

// parseMetaComment extracts tag/docs from "<!-- clawker: tag=feature docs=<url> -->".
// A plain HTML comment (no "clawker:" keyword) returns ok=false.
func parseMetaComment(line string) (tag, docs string, ok bool) {
	if !strings.HasPrefix(line, metaCommentPrefix) || !strings.HasSuffix(line, metaCommentSuffix) {
		return "", "", false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(line, metaCommentPrefix), metaCommentSuffix)
	inner = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(inner), metaKeyword))
	for field := range strings.FieldsSeq(inner) {
		key, val, found := strings.Cut(field, "=")
		if !found {
			continue
		}
		switch key {
		case metaKeyTag:
			tag = val
		case metaKeyDocs:
			docs = val
		}
	}
	return tag, docs, true
}

// tagFromSubsection derives a tag from the first Keep-a-Changelog subsection
// heading when no explicit metadata tag is present.
func tagFromSubsection(subsection string) Tag {
	switch strings.ToLower(strings.TrimSpace(subsection)) {
	case sectionAdded:
		return TagFeature
	case sectionFixed:
		return TagFix
	case sectionRemoved, sectionDeprecated:
		return TagBreaking
	case sectionChanged:
		return TagChanged
	case sectionSecurity:
		return TagFix
	default:
		return ""
	}
}

// titleFromBody returns the first meaningful headline of an entry body: the
// text of the first bullet, with bold markers and the leading "- " stripped, up
// to the first sentence-ending period.
func titleFromBody(body string) string {
	for line := range strings.SplitSeq(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, subsectionPrefix) {
			continue
		}
		t = strings.TrimPrefix(t, "- ")
		t = strings.TrimPrefix(t, "* ")
		t = strings.ReplaceAll(t, "**", "")
		if dot := strings.IndexByte(t, '.'); dot >= 0 {
			t = t[:dot]
		}
		return strings.TrimSpace(t)
	}
	return ""
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
