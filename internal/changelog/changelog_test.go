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
	entries, err := Parse(string(loadFixture(t)))
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

// fixtureEntries parses the shared changelog fixture into the entry slice the
// caller now supplies to CheckForChanges. It goes through the real parser
// rather than hand-built structs so the diff tests stay honest about the shape
// a live fetch produces.
func fixtureEntries(t *testing.T) []Entry {
	t.Helper()
	entries, err := Parse(changesFixture)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return entries
}

// checkFixture runs CheckForChanges over the fixture entries and fails the test
// on error. Every success-path test wants the gained entries and treats an
// error as fatal, so folding the check in here keeps the assertions in each
// test about behavior rather than error plumbing.
func checkFixture(t *testing.T, st state.StateStore, current string) []Entry {
	t.Helper()
	gained, err := CheckForChanges(fixtureEntries(t), st, current)
	if err != nil {
		t.Fatalf("CheckForChanges(current=%q): %v", current, err)
	}
	return gained
}

// fixtureCurrent is the newest version in changesFixture. It is the running
// version every cursor test checks against, and therefore the value the cursor
// must hold afterwards — whether it got there by first-run seed or by advance.
const fixtureCurrent = "0.12.0"

// assertCursorAtCurrent asserts the show-once cursor landed on fixtureCurrent in
// canonical bare-semver form (never v-prefixed, whatever form current arrived
// in). This is the one post-condition both cursor-store sites share.
func assertCursorAtCurrent(t *testing.T, st state.StateStore) {
	t.Helper()
	if cur := st.LastSeenChangelog(); cur != fixtureCurrent {
		t.Errorf("cursor = %q, want %q", cur, fixtureCurrent)
	}
}

// assertGained asserts the gained entries by version, newest-first. A nil want
// asserts nothing was gained.
func assertGained(t *testing.T, gained []Entry, want []string) {
	t.Helper()
	got := versions(gained)
	if len(got) != len(want) {
		t.Fatalf("gained = %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("entry %d = %q, want %q", i, got[i], v)
		}
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
	entries, err := Parse(raw)
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
			st := newTestState(t)
			seedCursor(t, st, c.cursor)

			assertGained(t, checkFixture(t, st, c.current), c.wantVers)
		})
	}
}

// TestCheckForChanges_FirstRunReseed covers the two inputs that prod treats as
// a first run — an empty cursor and a non-version (garbage) cursor left in
// state. Both must reseed the cursor at current and return nil: there is no
// catch-up backfill, and a garbage cursor must not crash or diff against
// itself. Entries are supplied by the caller now, so "did it fetch" is no
// longer this function's concern.
func TestCheckForChanges_FirstRunReseed(t *testing.T) {
	cases := []struct {
		name   string
		cursor string // stored at rest as-is; "" is the true-first-run cursor
	}{
		{"empty_cursor", ""},
		{"garbage_cursor", "not-a-version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestState(t)
			seedCursor(t, st, c.cursor)

			assertGained(t, checkFixture(t, st, "0.12.0"), nil)
			assertCursorAtCurrent(t, st)
		})
	}
}

// TestCheckForChanges_AdvancesCursor: with a seeded cursor the cursor advances
// to current after a successful check. The persist gate is gone — CheckForChanges
// is only called on a non-suppressed run, so it always advances.
func TestCheckForChanges_AdvancesCursor(t *testing.T) {
	st := newTestState(t)
	seedCursor(t, st, "0.10.0")

	assertGained(t, checkFixture(t, st, "0.12.0"), []string{"0.12.0", "0.11.0"})
	assertCursorAtCurrent(t, st)
}

// TestCheckForChanges_StoresCanonicalCursor: a current parsed from a v-prefixed
// string ("v0.12.0") is stored canonical (bare "0.12.0") via String(), not the
// v-prefixed Original(). Asserted on both cursor-store sites — the first-run
// seed and the advance.
func TestCheckForChanges_StoresCanonicalCursor(t *testing.T) {
	t.Run("first_run_seed", func(t *testing.T) {
		st := newTestState(t) // empty cursor → first-run seed path

		checkFixture(t, st, "v0.12.0")
		assertCursorAtCurrent(t, st)
	})

	t.Run("advance", func(t *testing.T) {
		st := newTestState(t)
		seedCursor(t, st, "0.10.0") // diff path → advance

		checkFixture(t, st, "v0.12.0")
		assertCursorAtCurrent(t, st)
	})
}

// TestCheckForChanges_NilStateError: a nil state facade is a programming error —
// it returns the typed nil-StateStore error with no entries (the cursor lives
// in state, so there is nothing to diff against).
func TestCheckForChanges_NilStateError(t *testing.T) {
	gained, err := CheckForChanges(fixtureEntries(t), nil, "0.12.0")
	wantErr := "state: CheckForChanges: nil StateStore"
	if err == nil || err.Error() != wantErr {
		t.Fatalf("CheckForChanges error = %v, want %q", err, wantErr)
	}
	if len(gained) != 0 {
		t.Errorf("nil state returned %v, want no entries", versions(gained))
	}
}

// TestGetChangelogEntries_NonOKError: a non-200 from the changelog endpoint is
// an error, not an empty entry list. The caller never reaches CheckForChanges
// on this path, so the cursor cannot advance on a failed fetch.
func TestGetChangelogEntries_NonOKError(t *testing.T) {
	reg := changelogStub(http.StatusInternalServerError, "boom")

	entries, err := GetChangelogEntries(context.Background(), reg.Client())
	if err == nil {
		t.Fatal("expected error on non-200 response")
	}
	if len(entries) != 0 {
		t.Errorf("returned %v on non-200, want no entries", versions(entries))
	}
}

// TestGetChangelogEntries_ParsesFixture: the happy path fetches and parses the
// curated changelog into newest-first entries.
func TestGetChangelogEntries_ParsesFixture(t *testing.T) {
	reg := changelogStub(http.StatusOK, changesFixture)

	entries, err := GetChangelogEntries(context.Background(), reg.Client())
	if err != nil {
		t.Fatalf("GetChangelogEntries: %v", err)
	}
	if got, want := versions(entries), []string{"0.12.0", "0.11.0", "0.5.0"}; len(got) != len(want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if len(reg.Requests) != 1 {
		t.Errorf("hit the changelog endpoint %d times, want 1", len(reg.Requests))
	}
}
