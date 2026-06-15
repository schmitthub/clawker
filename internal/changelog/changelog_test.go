package changelog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
)

// loadFixture reads testdata/CHANGELOG.md, which mirrors the real CHANGELOG.md
// shape (preamble + an "## [Unreleased]" section + version headers +
// Keep-a-Changelog subsections + HTML comments + trailing link references) so
// parser tests are stable regardless of the curated content.
func loadFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

func parseFixture(t *testing.T) []Entry {
	t.Helper()
	entries, err := parse(string(loadFixture(t)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return entries
}

func TestParse_Headers(t *testing.T) {
	entries := parseFixture(t)
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}

	// Newest-first, version + date parsed from the header. The "## [Unreleased]"
	// section is skipped (non-semver token), so it never appears here.
	want := []struct{ ver, date string }{
		{"0.12.0", "2026-06-11"},
		{"0.11.0", "2026-06-10"},
		{"0.5.0", "2026-03-20"},
	}
	for i, w := range want {
		e := entries[i]
		if e.Version != w.ver {
			t.Errorf("entry %d version = %q, want %q", i, e.Version, w.ver)
		}
		if e.Date != w.date {
			t.Errorf("entry %d date = %q, want %q", i, e.Date, w.date)
		}
	}
}

// TestParse_SkipsUnreleased asserts that a non-semver version section
// ("## [Unreleased]") never yields an Entry.
func TestParse_SkipsUnreleased(t *testing.T) {
	entries := parseFixture(t)
	for _, e := range entries {
		if strings.EqualFold(e.Version, "Unreleased") || e.Version == "" {
			t.Fatalf("Unreleased section leaked as entry: %+v", e)
		}
		if _, err := semver.StrictNewVersion(e.Version); err != nil {
			t.Errorf("entry has non-semver version %q: %v", e.Version, err)
		}
	}
}

// TestParse_Body asserts the body preserves the full Keep-a-Changelog markdown
// — every section of a multi-kind release, the bullets, and inline links — while
// stripping HTML comments (incl. the legacy "<!-- clawker: -->" line) and the
// trailing link-reference block.
func TestParse_Body(t *testing.T) {
	entries := parseFixture(t)
	first := entries[0]

	// A release spans multiple kinds: both the Added and Fixed sections survive.
	for _, want := range []string{
		"### Added",
		"### Fixed",
		"**User-configurable command aliases.**",
		"[docs](https://docs.clawker.dev/aliases)", // inline link preserved verbatim
		"**Alias expansion order.**",
	} {
		if !strings.Contains(first.Body, want) {
			t.Errorf("body missing %q:\n%s", want, first.Body)
		}
	}

	// HTML comments (both the legacy "<!-- clawker: -->" metadata line and a
	// plain note) and the link-reference block must not leak into the body. The
	// "<!--" guard catches any comment flavor; "plain release note" pins the
	// non-clawker comment specifically.
	for _, bad := range []string{"<!--", "clawker:", "plain release note", "releases/tag"} {
		if strings.Contains(first.Body, bad) {
			t.Errorf("body leaked %q:\n%s", bad, first.Body)
		}
	}
}

// TestParse_PartialSemverHeaderSkipped guards the parser's StrictNewVersion
// check: a bracket token like "0.12" lacks a patch component, which
// StrictNewVersion rejects, so the version header must be skipped (no entry).
// This guards against someone swapping StrictNewVersion for the coercing
// NewVersion (which would accept "0.12" as "0.12.0").
func TestParse_PartialSemverHeaderSkipped(t *testing.T) {
	raw := `## [0.12] - 2026-01-01

### Added

- A thing.
`
	entries, err := parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0 (partial-semver header must be skipped): %+v", len(entries), entries)
	}
}

func versions(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Version
	}
	return out
}
