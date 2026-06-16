package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedTime is a stable RFC3339 instant for round-trip assertions — avoids the
// monotonic-clock / sub-second precision drift of time.Now().
var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// TestState_New_CreatesFileAtDefaultLocation proves the create-if-missing path:
// a fresh store with no file on disk writes to the resolved state dir under
// consts.CLIStateFile, and the values read back after reopening from disk. This
// is the whole reason WithFilenames is load-bearing.
func TestState_New_CreatesFileAtDefaultLocation(t *testing.T) {
	env := testenv.New(t)
	want := filepath.Join(env.Dirs.State, consts.CLIStateFile)

	// No file exists yet.
	_, err := os.Stat(want)
	require.True(t, os.IsNotExist(err), "state file should not exist before first write")

	st, err := New()
	require.NoError(t, err)
	require.NoError(t, st.RecordUpdateCheck(fixedTime, "1.2.3"))

	// First write created the file at the default location.
	_, err = os.Stat(want)
	require.NoError(t, err, "state file should exist at %s after first write", want)

	// Reopening from disk reads the persisted values back.
	reopened, err := New()
	require.NoError(t, err)
	got := reopened.State()
	assert.True(t, got.CheckedAt.Equal(fixedTime), "CheckedAt: want %v, got %v", fixedTime, got.CheckedAt)
	assert.Equal(t, "1.2.3", got.LatestVersion)
	assert.Empty(t, got.LastSeenChangelog)
}

// TestState_New_ReadsLegacyFileInPlace proves an existing on-disk state file
// from an older binary is read in place: schema fields carry forward, dropped
// keys (current_version, latest_url, no longer in the schema) are ignored, and
// the changelog cursor starts empty. Real FS: exercises discovery + load over
// the migration runner, the read-in-place contract the package guarantees.
func TestState_New_ReadsLegacyFileInPlace(t *testing.T) {
	env := testenv.New(t)
	legacy := "current_version: 0.9.0\n" +
		"latest_url: https://example.test/old\n" +
		"checked_at: 2026-01-02T03:04:05Z\n" +
		"latest_version: 1.2.3\n"
	path := filepath.Join(env.Dirs.State, consts.CLIStateFile)
	require.NoError(t, os.WriteFile(path, []byte(legacy), 0o644))

	st, err := New()
	require.NoError(t, err)

	got := st.State()
	assert.True(t, got.CheckedAt.Equal(fixedTime), "CheckedAt: want %v, got %v", fixedTime, got.CheckedAt)
	assert.Equal(t, "1.2.3", got.LatestVersion)
	assert.Empty(t, got.LastSeenChangelog, "changelog cursor starts unseeded")
}

// TestStateMigrations walks the full legacy chain. One row per historical
// on-disk shape ever shipped — add a row when you add a migration. Per row, real
// FS: write the legacy file, load via New(), and assert (a) the typed read, (b)
// the cleaned on-disk keys, and (c) idempotency (a second load leaves the file
// byte-stable). Storage runs every migration on every load, so an oldest-shape
// row implicitly exercises the whole chain.
func TestStateMigrations(t *testing.T) {
	cases := []struct {
		name       string
		legacy     string   // on-disk YAML as some past binary wrote it
		want       State    // expected snapshot after the chain runs
		absentKeys []string // keys that must be gone from the re-saved file
	}{
		{
			name: "pre-store update checker drops latest_url + current_version",
			legacy: "checked_at: 2026-01-02T03:04:05Z\n" +
				"latest_version: 1.2.3\n" +
				"latest_url: https://example.test/old\n" +
				"current_version: 0.9.0\n",
			want:       State{CheckedAt: fixedTime, LatestVersion: "1.2.3"},
			absentKeys: []string{"latest_url", "current_version"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testenv.New(t)
			path := filepath.Join(env.Dirs.State, consts.CLIStateFile)
			require.NoError(t, os.WriteFile(path, []byte(tc.legacy), 0o644))

			// Load runs the migration chain and re-saves the cleaned file.
			st, err := New()
			require.NoError(t, err)

			// (a) typed read through the chain.
			got := st.State()
			assert.True(t, got.CheckedAt.Equal(tc.want.CheckedAt), "CheckedAt: want %v, got %v", tc.want.CheckedAt, got.CheckedAt)
			assert.Equal(t, tc.want.LatestVersion, got.LatestVersion)
			assert.Equal(t, tc.want.LastSeenChangelog, got.LastSeenChangelog)

			// (b) on-disk cleanliness: dead keys stripped from the re-saved file.
			migrated, err := os.ReadFile(path)
			require.NoError(t, err)
			for _, k := range tc.absentKeys {
				assert.NotContains(t, string(migrated), k, "key %q should be stripped from the file", k)
			}

			// (c) idempotency: a second load fires no migration, leaves the file stable.
			_, err = New()
			require.NoError(t, err)
			stable, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, string(migrated), string(stable), "second load must leave the file byte-stable")
		})
	}
}

// TestState_SeedFromString covers the seed seam: NewFromString merges a YAML
// string as the virtual layer, so State() reflects it without touching disk.
// This is how consumer tests inject arbitrary starting state.
func TestState_SeedFromString(t *testing.T) {
	cases := []struct {
		name        string
		seed        string
		wantChecked time.Time // zero value => expect IsZero
		wantLatest  string
		wantSeen    string
	}{
		{
			name: "empty seed is all zero",
			seed: "",
		},
		{
			name:       "latest version only",
			seed:       "latest_version: 1.2.3\n",
			wantLatest: "1.2.3",
		},
		{
			name:        "all fields populated",
			seed:        "checked_at: 2026-01-02T03:04:05Z\nlatest_version: 1.2.3\nlast_seen_changelog: 0.13.0\n",
			wantChecked: fixedTime,
			wantLatest:  "1.2.3",
			wantSeen:    "0.13.0",
		},
		{
			name:       "legacy current_version key is ignored",
			seed:       "current_version: 0.9.0\nlatest_version: 1.2.3\n",
			wantLatest: "1.2.3",
		},
		{
			name:        "absent cursor reads empty",
			seed:        "checked_at: 2026-01-02T03:04:05Z\nlatest_version: 1.2.3\n",
			wantChecked: fixedTime,
			wantLatest:  "1.2.3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testenv.New(t)

			st, err := NewFromString(tc.seed)
			require.NoError(t, err)

			got := st.State()
			if tc.wantChecked.IsZero() {
				assert.True(t, got.CheckedAt.IsZero(), "CheckedAt should be zero, got %v", got.CheckedAt)
			} else {
				assert.True(t, got.CheckedAt.Equal(tc.wantChecked), "CheckedAt: want %v, got %v", tc.wantChecked, got.CheckedAt)
			}
			assert.Equal(t, tc.wantLatest, got.LatestVersion)
			assert.Equal(t, tc.wantSeen, got.LastSeenChangelog)
		})
	}
}

// TestState_WritersDoNotClobber proves the disjoint-by-ownership invariant: the
// update-check writer and the changelog-cursor writer touch separate fields, so
// two independent store instances writing the same file (the real production
// shape — background update goroutine + foreground teaser) never wipe each
// other's data. A whole-struct overwrite would fail this; field-merge passes.
func TestState_WritersDoNotClobber(t *testing.T) {
	cases := []struct {
		name   string
		first  func(t *testing.T, st StateStore)
		second func(t *testing.T, st StateStore)
	}{
		{
			name:   "update check then cursor",
			first:  func(t *testing.T, st StateStore) { require.NoError(t, st.RecordUpdateCheck(fixedTime, "1.2.3")) },
			second: func(t *testing.T, st StateStore) { require.NoError(t, st.SetLastSeenChangelog("0.13.0")) },
		},
		{
			name:   "cursor then update check",
			first:  func(t *testing.T, st StateStore) { require.NoError(t, st.SetLastSeenChangelog("0.13.0")) },
			second: func(t *testing.T, st StateStore) { require.NoError(t, st.RecordUpdateCheck(fixedTime, "1.2.3")) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testenv.New(t)

			first, err := New()
			require.NoError(t, err)
			tc.first(t, first)

			// A separate facade over the same file performs the second write.
			second, err := New()
			require.NoError(t, err)
			tc.second(t, second)

			// Reopen from disk: both writers' fields survive.
			got, err := New()
			require.NoError(t, err)
			st := got.State()
			assert.True(t, st.CheckedAt.Equal(fixedTime), "CheckedAt: want %v, got %v", fixedTime, st.CheckedAt)
			assert.Equal(t, "1.2.3", st.LatestVersion)
			assert.Equal(t, "0.13.0", st.LastSeenChangelog)
		})
	}
}
