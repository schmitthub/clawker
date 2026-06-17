package changelog

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"

	"github.com/schmitthub/clawker/internal/httpmock"
	"github.com/schmitthub/clawker/internal/state"
	"github.com/schmitthub/clawker/internal/testenv"
)

// --- parser helpers ---

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

func versions(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Version
	}
	return out
}

// --- CheckForChanges helpers ---

// changesFixture is real Keep-a-Changelog markdown spanning the bounds the
// range tests assert against. CheckForChanges parses it through the package
// parser, so these tests exercise the real fetch→parse→diff seam.
const changesFixture = `# Changelog

## [0.12.0] - 2026-06-11

### Added

- **Command aliases.** Define your own shortcuts.

## [0.11.0] - 2026-06-10

### Fixed

- **Worktree masks.** Containers protect the host repository.

## [0.5.0] - 2026-03-20

### Added

- **Firewall.** Egress firewall stack.

[0.12.0]: https://github.com/schmitthub/clawker/releases/tag/v0.12.0
`

// changelogStub returns an httpmock registry that serves body (with status) for
// the CHANGELOG.md GET. The transport is the seam — production ChangelogURL is
// never swapped. Tests assert whether the network was hit via len(reg.Requests).
func changelogStub(status int, body string) *httpmock.Registry {
	reg := &httpmock.Registry{}
	reg.Register(
		httpmock.REST(http.MethodGet, "CHANGELOG.md"),
		httpmock.StatusStringResponse(status, body),
	)
	return reg
}

func newTestState(t *testing.T) state.StateStore {
	t.Helper()
	testenv.New(t)
	st, err := state.New()
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	return st
}

// seedCursor sets the show-once cursor (a raw string, as it lives at rest) so a
// test exercises the diff path rather than the first-run bootstrap. The string
// is intentionally un-validated here: CheckForChanges owns parsing it, including
// the failure branch when it is not a version.
func seedCursor(t *testing.T, st state.StateStore, version string) {
	t.Helper()
	if err := st.SetLastSeenChangelog(version); err != nil {
		t.Fatalf("seed cursor: %v", err)
	}
}

// --- parser tests ---

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

// --- CheckForChanges tests ---

// TestCheckForChanges_Ranges drives the (cursor, current] diff through the real
// entry point: the cursor is seeded as a raw string in state and parsed by
// CheckForChanges (prod), not pre-parsed by the test.
func TestCheckForChanges_Ranges(t *testing.T) {
	cases := []struct {
		name     string
		cursor   string // stored at rest; CheckForChanges parses it
		current  string // CheckForChanges parses this
		wantVers []string
	}{
		// Single-step upgrade returns only the newest.
		{"single_step", "0.11.0", "0.12.0", []string{"0.12.0"}},
		// A wide jump spans every gained entry above the exclusive lower bound.
		{"wide_jump", "0.10.0", "0.12.0", []string{"0.12.0", "0.11.0"}},
		// cursor is exclusive, current inclusive — equal bounds yield nothing new.
		{"equal_bounds", "0.12.0", "0.12.0", nil},
		// A leading-v cursor normalizes (NewVersion tolerates it).
		{"v_prefixed_cursor", "v0.10.0", "0.12.0", []string{"0.12.0", "0.11.0"}},
		// Lower than everything → whole series.
		{"from_zero", "0.0.0", "0.12.0", []string{"0.12.0", "0.11.0", "0.5.0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := changelogStub(http.StatusOK, changesFixture)
			st := newTestState(t)
			seedCursor(t, st, c.cursor)

			gained, err := CheckForChanges(context.Background(), reg.Client(), st, c.current)
			if err != nil {
				t.Fatalf("CheckForChanges: %v", err)
			}
			if got := versions(gained); len(got) != len(c.wantVers) {
				t.Fatalf("gained = %v, want %v", got, c.wantVers)
			}
			for i, v := range c.wantVers {
				if gained[i].Version != v {
					t.Errorf("entry %d = %q, want %q", i, gained[i].Version, v)
				}
			}
		})
	}
}

// TestCheckForChanges_FirstRunReseedNoFetch covers the two inputs that prod
// treats as a first run — an empty cursor and a non-version (garbage) cursor
// left in state. Both must reseed the cursor at current and return nil WITHOUT
// hitting the network: there is no catch-up backfill, and a garbage cursor must
// not crash or diff against itself.
func TestCheckForChanges_FirstRunReseedNoFetch(t *testing.T) {
	cases := []struct {
		name   string
		cursor string // "" = leave empty (true first run); else seeded as-is
	}{
		{"empty_cursor", ""},
		{"garbage_cursor", "not-a-version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := changelogStub(http.StatusOK, changesFixture)
			st := newTestState(t)
			if c.cursor != "" {
				seedCursor(t, st, c.cursor)
			}

			gained, err := CheckForChanges(context.Background(), reg.Client(), st, "0.12.0")
			if err != nil {
				t.Fatalf("CheckForChanges: %v", err)
			}
			if len(gained) != 0 {
				t.Errorf("returned %v, want no entries (first-run reseed)", versions(gained))
			}
			if cur := st.State().LastSeenChangelog; cur != "0.12.0" {
				t.Errorf("cursor = %q, want reseeded to 0.12.0", cur)
			}
			if len(reg.Requests) != 0 {
				t.Errorf("hit the changelog endpoint %d times, want 0 (no fetch)", len(reg.Requests))
			}
		})
	}
}

// TestCheckForChanges_AdvancesCursor: with a seeded cursor the cursor advances
// to current after a successful check. The persist gate is gone — CheckForChanges
// is only called on a non-suppressed run, so it always advances.
func TestCheckForChanges_AdvancesCursor(t *testing.T) {
	reg := changelogStub(http.StatusOK, changesFixture)
	st := newTestState(t)
	seedCursor(t, st, "0.10.0")

	gained, err := CheckForChanges(context.Background(), reg.Client(), st, "0.12.0")
	if err != nil {
		t.Fatalf("CheckForChanges: %v", err)
	}
	if len(gained) == 0 {
		t.Fatal("expected gained entries")
	}
	if cur := st.State().LastSeenChangelog; cur != "0.12.0" {
		t.Errorf("cursor = %q, want advanced to 0.12.0", cur)
	}
}

// TestCheckForChanges_StoresCanonicalCursor: a current parsed from a v-prefixed
// string ("v0.12.0") is stored canonical (bare "0.12.0") via String(), not the
// v-prefixed Original(). Asserted on both cursor-store sites — the first-run
// seed and the advance.
func TestCheckForChanges_StoresCanonicalCursor(t *testing.T) {
	t.Run("first_run_seed", func(t *testing.T) {
		reg := changelogStub(http.StatusOK, changesFixture)
		st := newTestState(t) // empty cursor → first-run seed path

		if _, err := CheckForChanges(context.Background(), reg.Client(), st, "v0.12.0"); err != nil {
			t.Fatalf("CheckForChanges: %v", err)
		}
		if cur := st.State().LastSeenChangelog; cur != "0.12.0" {
			t.Errorf("seeded cursor = %q, want canonical 0.12.0 (not v-prefixed)", cur)
		}
	})

	t.Run("advance", func(t *testing.T) {
		reg := changelogStub(http.StatusOK, changesFixture)
		st := newTestState(t)
		seedCursor(t, st, "0.10.0") // diff path → advance

		if _, err := CheckForChanges(context.Background(), reg.Client(), st, "v0.12.0"); err != nil {
			t.Fatalf("CheckForChanges: %v", err)
		}
		if cur := st.State().LastSeenChangelog; cur != "0.12.0" {
			t.Errorf("advanced cursor = %q, want canonical 0.12.0 (not v-prefixed)", cur)
		}
	})
}

// TestCheckForChanges_NilStateError: a nil state facade is a programming error —
// it returns the typed nil-StateStore error with no entries and no fetch (the
// cursor lives in state, so there is nothing to diff against).
func TestCheckForChanges_NilStateError(t *testing.T) {
	reg := changelogStub(http.StatusOK, changesFixture)

	gained, err := CheckForChanges(context.Background(), reg.Client(), nil, "0.12.0")
	wantErr := "state: CheckForChanges: nil StateStore"
	if err == nil || err.Error() != wantErr {
		t.Fatalf("CheckForChanges error = %v, want %q", err, wantErr)
	}
	if len(gained) != 0 {
		t.Errorf("nil state returned %v, want no entries", versions(gained))
	}
	if len(reg.Requests) != 0 {
		t.Errorf("nil state hit the endpoint %d times, want 0", len(reg.Requests))
	}
}

// TestCheckForChanges_FetchErrorNoAdvance: a non-200 surfaces an error and never
// advances the cursor.
func TestCheckForChanges_FetchErrorNoAdvance(t *testing.T) {
	reg := changelogStub(http.StatusInternalServerError, "boom")
	st := newTestState(t)
	seedCursor(t, st, "0.10.0")

	_, err := CheckForChanges(context.Background(), reg.Client(), st, "0.12.0")
	if err == nil {
		t.Fatal("expected error on non-200 response")
	}
	if cur := st.State().LastSeenChangelog; cur != "0.10.0" {
		t.Errorf("cursor advanced to %q on fetch error, want untouched 0.10.0", cur)
	}
}
