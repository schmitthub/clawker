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

func TestParse_HeadersAndMetadata(t *testing.T) {
	entries := parseFixture(t)
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}

	// Newest-first, version + date parsed from the header. The "## [Unreleased]"
	// section is skipped (non-semver token), so it never appears here.
	want := []struct{ ver, date, tag, docs string }{
		{"0.12.0", "2026-06-11", TagFeature, "https://docs.clawker.dev/aliases"},
		{"0.11.0", "2026-06-10", TagFix, ""},
		{"0.5.0", "2026-03-20", TagFeature, ""}, // tag derived from "### Added"
	}
	for i, w := range want {
		e := entries[i]
		if e.Version != w.ver {
			t.Errorf("entry %d version = %q, want %q", i, e.Version, w.ver)
		}
		if e.Date != w.date {
			t.Errorf("entry %d date = %q, want %q", i, e.Date, w.date)
		}
		if e.Tag != w.tag {
			t.Errorf("entry %d tag = %q, want %q", i, e.Tag, w.tag)
		}
		if e.Docs != w.docs {
			t.Errorf("entry %d docs = %q, want %q", i, e.Docs, w.docs)
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

func TestParse_TitleAndBody(t *testing.T) {
	entries := parseFixture(t)
	first := entries[0]
	if first.Title != "User-configurable command aliases" {
		t.Errorf("title = %q", first.Title)
	}
	// The metadata comment and link references must not leak into the body.
	if strings.Contains(first.Body, metaKeyword) {
		t.Errorf("body leaked metadata comment: %q", first.Body)
	}
	if strings.Contains(first.Body, "releases/tag") {
		t.Errorf("body leaked link reference: %q", first.Body)
	}
	if !strings.Contains(first.Body, subsectionPrefix+"Added") {
		t.Errorf("body missing subsection heading: %q", first.Body)
	}
}

func TestParse_PlainHTMLCommentIgnored(t *testing.T) {
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
	if entries[0].Tag != TagFeature {
		t.Errorf("tag = %q, want derived %q", entries[0].Tag, TagFeature)
	}
	if entries[0].Docs != "" {
		t.Errorf("docs = %q, want empty", entries[0].Docs)
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

func TestForVersion_HitAndMiss(t *testing.T) {
	all := parseFixture(t)

	if e, ok := ForVersion(all, "0.11.0"); !ok {
		t.Errorf("0.11.0 not found")
	} else if e.Tag != TagFix {
		t.Errorf("0.11.0 tag = %q", e.Tag)
	}
	// Leading v normalizes.
	if _, ok := ForVersion(all, "v0.12.0"); !ok {
		t.Errorf("v0.12.0 not found")
	}
	// Miss.
	if _, ok := ForVersion(all, "9.9.9"); ok {
		t.Errorf("9.9.9 unexpectedly found")
	}
}

func versions(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Version
	}
	return out
}
