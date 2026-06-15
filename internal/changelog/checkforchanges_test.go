package changelog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Masterminds/semver/v3"

	"github.com/schmitthub/clawker/internal/state"
)

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

// serveChangelog points ChangelogURL at an httptest server returning body for
// the duration of the test, restoring the original URL on cleanup. The returned
// pointer counts requests, so a test can assert whether the network was hit.
func serveChangelog(t *testing.T, status int, body string) *int {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	orig := ChangelogURL
	ChangelogURL = srv.URL
	t.Cleanup(func() { ChangelogURL = orig })
	return &hits
}

func newTestState(t *testing.T) *state.State {
	t.Helper()
	st, err := state.New(state.WithStateDirOverride(t.TempDir()))
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	return st
}

// seedCursor sets the show-once cursor (a raw string, as it lives at rest) so a
// test exercises the diff path rather than the first-run bootstrap. The string
// is intentionally un-validated here: CheckForChanges owns parsing it, including
// the failure branch when it is not a version.
func seedCursor(t *testing.T, st *state.State, version string) {
	t.Helper()
	if err := st.SetLastSeenChangelog(version); err != nil {
		t.Fatalf("seed cursor: %v", err)
	}
}

// callerVersion parses the running-binary version the way Main does before
// handing it to CheckForChanges. CheckForChanges takes an already-parsed
// *semver.Version for current, so the caller (Main, or this test standing in for
// it) is responsible for parsing build.Version — a bad literal here is a
// test-author bug, not a prod path.
func callerVersion(t *testing.T, s string) *semver.Version {
	t.Helper()
	v, err := semver.NewVersion(s)
	if err != nil {
		t.Fatalf("caller parse %q: %v", s, err)
	}
	return v
}

// TestCheckForChanges_Ranges drives the (cursor, current] diff through the real
// entry point: the cursor is seeded as a raw string in state and parsed by
// CheckForChanges (prod), not pre-parsed by the test.
func TestCheckForChanges_Ranges(t *testing.T) {
	cases := []struct {
		name     string
		cursor   string // stored at rest; CheckForChanges parses it
		current  string // the caller (Main) parses this
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
			serveChangelog(t, http.StatusOK, changesFixture)
			st := newTestState(t)
			seedCursor(t, st, c.cursor)

			gained, err := CheckForChanges(context.Background(), st, callerVersion(t, c.current))
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

// TestCheckForChanges_FirstRunSeedsCursorNoFetch: with an empty cursor the first
// changelog-aware run seeds the cursor at current and returns nil WITHOUT
// hitting the network — there is no catch-up backfill.
func TestCheckForChanges_FirstRunSeedsCursorNoFetch(t *testing.T) {
	hits := serveChangelog(t, http.StatusOK, changesFixture)
	st := newTestState(t) // cursor starts empty

	gained, err := CheckForChanges(context.Background(), st, callerVersion(t, "0.12.0"))
	if err != nil {
		t.Fatalf("CheckForChanges: %v", err)
	}
	if len(gained) != 0 {
		t.Errorf("first run returned %v, want no entries (no backfill)", versions(gained))
	}
	if cur := st.LastSeenChangelog(); cur != "0.12.0" {
		t.Errorf("cursor = %q, want seeded to 0.12.0", cur)
	}
	if *hits != 0 {
		t.Errorf("first run hit the changelog endpoint %d times, want 0 (no fetch)", *hits)
	}
}

// TestCheckForChanges_GarbageCursorTreatedAsFirstRun exercises the cursor-parse
// FAILURE branch in prod: a non-version cursor string left in state must be
// treated as a first run (reseed at current, no fetch, no entries), not crash or
// diff against garbage.
func TestCheckForChanges_GarbageCursorTreatedAsFirstRun(t *testing.T) {
	hits := serveChangelog(t, http.StatusOK, changesFixture)
	st := newTestState(t)
	seedCursor(t, st, "not-a-version")

	gained, err := CheckForChanges(context.Background(), st, callerVersion(t, "0.12.0"))
	if err != nil {
		t.Fatalf("CheckForChanges: %v", err)
	}
	if len(gained) != 0 {
		t.Errorf("garbage cursor returned %v, want no entries (first-run reseed)", versions(gained))
	}
	if cur := st.LastSeenChangelog(); cur != "0.12.0" {
		t.Errorf("cursor = %q, want reseeded to 0.12.0", cur)
	}
	if *hits != 0 {
		t.Errorf("garbage-cursor run hit the endpoint %d times, want 0 (no fetch)", *hits)
	}
}

// TestCheckForChanges_AdvancesCursor: with a seeded cursor the cursor advances
// to current after a successful check. The persist gate is gone — CheckForChanges
// is only called on a non-suppressed run, so it always advances.
func TestCheckForChanges_AdvancesCursor(t *testing.T) {
	serveChangelog(t, http.StatusOK, changesFixture)
	st := newTestState(t)
	seedCursor(t, st, "0.10.0")

	gained, err := CheckForChanges(context.Background(), st, callerVersion(t, "0.12.0"))
	if err != nil {
		t.Fatalf("CheckForChanges: %v", err)
	}
	if len(gained) == 0 {
		t.Fatal("expected gained entries")
	}
	if cur := st.LastSeenChangelog(); cur != "0.12.0" {
		t.Errorf("cursor = %q, want advanced to 0.12.0", cur)
	}
}

// TestCheckForChanges_StoresCanonicalCursor: a current parsed from a v-prefixed
// string ("v0.12.0") is stored canonical (bare "0.12.0") via String(), not the
// v-prefixed Original(). Asserted on both cursor-store sites — the first-run
// seed and the advance.
func TestCheckForChanges_StoresCanonicalCursor(t *testing.T) {
	t.Run("first_run_seed", func(t *testing.T) {
		serveChangelog(t, http.StatusOK, changesFixture)
		st := newTestState(t) // empty cursor → first-run seed path

		if _, err := CheckForChanges(context.Background(), st, callerVersion(t, "v0.12.0")); err != nil {
			t.Fatalf("CheckForChanges: %v", err)
		}
		if cur := st.LastSeenChangelog(); cur != "0.12.0" {
			t.Errorf("seeded cursor = %q, want canonical 0.12.0 (not v-prefixed)", cur)
		}
	})

	t.Run("advance", func(t *testing.T) {
		serveChangelog(t, http.StatusOK, changesFixture)
		st := newTestState(t)
		seedCursor(t, st, "0.10.0") // diff path → advance

		if _, err := CheckForChanges(context.Background(), st, callerVersion(t, "v0.12.0")); err != nil {
			t.Fatalf("CheckForChanges: %v", err)
		}
		if cur := st.LastSeenChangelog(); cur != "0.12.0" {
			t.Errorf("advanced cursor = %q, want canonical 0.12.0 (not v-prefixed)", cur)
		}
	})
}

// TestCheckForChanges_NilStateNoOp: a nil state facade is a silent no-op — no
// panic, no entries, no fetch (the cursor lives in state, so there is nothing to
// diff against).
func TestCheckForChanges_NilStateNoOp(t *testing.T) {
	hits := serveChangelog(t, http.StatusOK, changesFixture)

	gained, err := CheckForChanges(context.Background(), nil, callerVersion(t, "0.12.0"))
	if err != nil {
		t.Fatalf("CheckForChanges: %v", err)
	}
	if len(gained) != 0 {
		t.Errorf("nil state returned %v, want no entries", versions(gained))
	}
	if *hits != 0 {
		t.Errorf("nil state hit the endpoint %d times, want 0", *hits)
	}
}

// TestCheckForChanges_FetchErrorNoAdvance: a non-200 surfaces an error and never
// advances the cursor.
func TestCheckForChanges_FetchErrorNoAdvance(t *testing.T) {
	serveChangelog(t, http.StatusInternalServerError, "boom")
	st := newTestState(t)
	seedCursor(t, st, "0.10.0")

	_, err := CheckForChanges(context.Background(), st, callerVersion(t, "0.12.0"))
	if err == nil {
		t.Fatal("expected error on non-200 response")
	}
	if cur := st.LastSeenChangelog(); cur != "0.10.0" {
		t.Errorf("cursor advanced to %q on fetch error, want untouched 0.10.0", cur)
	}
}
