package changelog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/semver"
)

// loadFixture reads testdata/CHANGELOG.md, which mirrors the real CHANGELOG.md
// shape (preamble + an "## [Unreleased]" section + header + metadata comment +
// Keep-a-Changelog subsections + trailing link references) so parser tests are
// stable regardless of the curated content.
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
	entries, err := Parse(loadFixture(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
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
		if !semver.IsValid(e.Version) {
			t.Errorf("entry has non-semver version %q", e.Version)
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

	// HTML comments (incl. the legacy clawker metadata line) and the link
	// reference block must not leak into the body.
	for _, bad := range []string{"<!--", "clawker:", "releases/tag"} {
		if strings.Contains(first.Body, bad) {
			t.Errorf("body leaked %q:\n%s", bad, first.Body)
		}
	}
}

// TestParse_HTMLCommentStripped asserts an HTML comment between the header and
// the body is dropped from the rendered body (so neither a plain note nor a
// legacy "<!-- clawker: -->" line leaks through), while the bullet survives.
func TestParse_HTMLCommentStripped(t *testing.T) {
	raw := []byte(`## [1.0.0] - 2026-01-01
<!-- not clawker metadata -->

### Added

- A thing.
`)
	entries, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if strings.Contains(entries[0].Body, "<!--") {
		t.Errorf("body leaked HTML comment: %q", entries[0].Body)
	}
	if !strings.Contains(entries[0].Body, "A thing.") {
		t.Errorf("body missing bullet: %q", entries[0].Body)
	}
}

// TestParse_PartialSemverHeaderSkipped guards the parser's HasPatch() check: a
// bracket token like "0.12" is loosely parseable by semver.Parse but lacks a
// patch component, so the version header must be skipped (no entry). This guards
// against someone "simplifying" the HasPatch() guard down to IsValid.
func TestParse_PartialSemverHeaderSkipped(t *testing.T) {
	raw := []byte(`## [0.12] - 2026-01-01
<!-- clawker: tag=feature -->

### Added

- A thing.
`)
	entries, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0 (partial-semver header must be skipped): %+v", len(entries), entries)
	}
}

func TestBetween_Range(t *testing.T) {
	all := parseFixture(t)

	cases := []struct {
		name     string
		lo, hi   string
		wantVers []string
	}{
		// A wide jump spans every gained entry.
		{"v0.5_to_v0.12", "0.5.0", "0.12.0", []string{"0.12.0", "0.11.0"}},
		// Single-step upgrade returns only the newest.
		{"v0.11_to_v0.12", "0.11.0", "0.12.0", []string{"0.12.0"}},
		// lo is exclusive, hi inclusive — equal bounds yield nothing new.
		{"v0.12_to_v0.12", "0.12.0", "0.12.0", nil},
		// Leading-v bounds normalize.
		{"v-prefixed", "v0.10.0", "v0.12.0", []string{"0.12.0", "0.11.0"}},
		// Lower than everything → whole series.
		{"from_zero", "0.0.0", "0.12.0", []string{"0.12.0", "0.11.0", "0.5.0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Between(all, c.lo, c.hi)
			if len(got) != len(c.wantVers) {
				t.Fatalf("got %d entries %v, want %d %v", len(got), versions(got), len(c.wantVers), c.wantVers)
			}
			for i, v := range c.wantVers {
				if got[i].Version != v {
					t.Errorf("entry %d = %q, want %q", i, got[i].Version, v)
				}
			}
		})
	}
}

func versions(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Version
	}
	return out
}
