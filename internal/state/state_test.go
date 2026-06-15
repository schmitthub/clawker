package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// newStoreWithMigrations builds a CliState store rooted at dir with an extra
// probe migration, exercising the same load+migrate pipeline New uses. It lets
// the migration-wiring test inject a transformation without shipping a real
// migration in production code.
func newStoreWithMigrations(dir string, fns ...storage.Migration) (*storage.Store[CliState], error) {
	return storage.New[CliState]("",
		storage.WithFilenames(consts.CliStateFile),
		storage.WithMigrations(fns...),
		storage.WithLock(),
		storage.WithPaths(dir),
	)
}

// newTestState builds a file-backed State rooted at a temp dir so tests touch
// real storage (merge + atomic write) without the user's XDG state dir.
func newTestState(t *testing.T) (*State, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := New(WithStateDirOverride(dir))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return st, dir
}

func TestState_RecordUpdateCheck_RoundTrip(t *testing.T) {
	st, dir := newTestState(t)

	checkedAt := time.Now().Truncate(time.Second)
	if err := st.RecordUpdateCheck(checkedAt, "1.2.3", "1.0.0"); err != nil {
		t.Fatalf("RecordUpdateCheck: %v", err)
	}

	// Re-open from disk to prove persistence, not just in-memory snapshot.
	reopened, err := New(WithStateDirOverride(dir))
	if err != nil {
		t.Fatalf("reopen New: %v", err)
	}
	got := reopened.Read()
	if !got.CheckedAt.Equal(checkedAt) {
		t.Errorf("CheckedAt = %v, want %v", got.CheckedAt, checkedAt)
	}
	if got.LatestVersion != "1.2.3" {
		t.Errorf("LatestVersion = %q, want %q", got.LatestVersion, "1.2.3")
	}
	if got.CurrentVersion != "1.0.0" {
		t.Errorf("CurrentVersion = %q, want %q", got.CurrentVersion, "1.0.0")
	}
}

func TestState_SetLastSeenChangelog_RoundTrip(t *testing.T) {
	st, dir := newTestState(t)

	if err := st.SetLastSeenChangelog("0.12.2"); err != nil {
		t.Fatalf("SetLastSeenChangelog: %v", err)
	}

	reopened, err := New(WithStateDirOverride(dir))
	if err != nil {
		t.Fatalf("reopen New: %v", err)
	}
	if got := reopened.LastSeenChangelog(); got != "0.12.2" {
		t.Errorf("LastSeenChangelog = %q, want %q", got, "0.12.2")
	}
}

// TestState_SetLastSeenChangelog_DoesNotClobberUpdateFields is the core
// invariant: a cursor write must field-merge, leaving the update-check fields
// untouched (and vice versa). A whole-struct overwrite would zero them.
func TestState_FieldMerge_NoClobber(t *testing.T) {
	st, dir := newTestState(t)

	checkedAt := time.Now().Truncate(time.Second)
	if err := st.RecordUpdateCheck(checkedAt, "2.0.0", "1.5.0"); err != nil {
		t.Fatalf("RecordUpdateCheck: %v", err)
	}

	// Cursor write through a SEPARATE facade instance (mirrors the real split
	// between the background update goroutine and the foreground cursor).
	cursorFacade, err := New(WithStateDirOverride(dir))
	if err != nil {
		t.Fatalf("cursor New: %v", err)
	}
	if err := cursorFacade.SetLastSeenChangelog("1.5.0"); err != nil {
		t.Fatalf("SetLastSeenChangelog: %v", err)
	}

	reopened, err := New(WithStateDirOverride(dir))
	if err != nil {
		t.Fatalf("reopen New: %v", err)
	}
	got := reopened.Read()
	if got.LastSeenChangelog != "1.5.0" {
		t.Errorf("LastSeenChangelog = %q, want %q", got.LastSeenChangelog, "1.5.0")
	}
	// The update-check fields written earlier must survive the cursor write.
	if !got.CheckedAt.Equal(checkedAt) {
		t.Errorf("CheckedAt clobbered: got %v, want %v", got.CheckedAt, checkedAt)
	}
	if got.LatestVersion != "2.0.0" {
		t.Errorf("LatestVersion clobbered: got %q, want %q", got.LatestVersion, "2.0.0")
	}
	if got.CurrentVersion != "1.5.0" {
		t.Errorf("CurrentVersion clobbered: got %q, want %q", got.CurrentVersion, "1.5.0")
	}

	// And the reverse: an update-check write must not clobber the cursor.
	updateFacade, err := New(WithStateDirOverride(dir))
	if err != nil {
		t.Fatalf("update New: %v", err)
	}
	newCheckedAt := checkedAt.Add(48 * time.Hour)
	if err := updateFacade.RecordUpdateCheck(newCheckedAt, "3.0.0", "1.5.0"); err != nil {
		t.Fatalf("RecordUpdateCheck (2): %v", err)
	}
	final, err := New(WithStateDirOverride(dir))
	if err != nil {
		t.Fatalf("final New: %v", err)
	}
	if got := final.LastSeenChangelog(); got != "1.5.0" {
		t.Errorf("cursor clobbered by update-check write: got %q, want %q", got, "1.5.0")
	}
	if got := final.LatestVersion(); got != "3.0.0" {
		t.Errorf("LatestVersion = %q, want %q", got, "3.0.0")
	}
}

// TestState_RecordChangelogFetch_DoesNotClobber proves the new fetch-timestamp
// writer field-merges: it persists changelog_fetched_at without disturbing the
// changelog cursor or the update-check fields, mirroring the invariant the other
// writers guarantee.
func TestState_RecordChangelogFetch_DoesNotClobber(t *testing.T) {
	st, dir := newTestState(t)

	checkedAt := time.Now().Truncate(time.Second)
	if err := st.RecordUpdateCheck(checkedAt, "2.0.0", "1.5.0"); err != nil {
		t.Fatalf("RecordUpdateCheck: %v", err)
	}
	if err := st.SetLastSeenChangelog("1.5.0"); err != nil {
		t.Fatalf("SetLastSeenChangelog: %v", err)
	}

	// Fetch-timestamp write through a SEPARATE facade instance (mirrors the
	// loader writing while the update goroutine / cursor own other fields).
	fetchFacade, err := New(WithStateDirOverride(dir))
	if err != nil {
		t.Fatalf("fetch New: %v", err)
	}
	fetchedAt := checkedAt.Add(time.Hour)
	if err := fetchFacade.RecordChangelogFetch(fetchedAt); err != nil {
		t.Fatalf("RecordChangelogFetch: %v", err)
	}

	reopened, err := New(WithStateDirOverride(dir))
	if err != nil {
		t.Fatalf("reopen New: %v", err)
	}
	got := reopened.Read()
	if !got.ChangelogFetchedAt.Equal(fetchedAt) {
		t.Errorf("ChangelogFetchedAt = %v, want %v", got.ChangelogFetchedAt, fetchedAt)
	}
	// The cursor and update-check fields must survive the fetch-timestamp write.
	if got.LastSeenChangelog != "1.5.0" {
		t.Errorf("LastSeenChangelog clobbered: got %q, want %q", got.LastSeenChangelog, "1.5.0")
	}
	if !got.CheckedAt.Equal(checkedAt) {
		t.Errorf("CheckedAt clobbered: got %v, want %v", got.CheckedAt, checkedAt)
	}
	if got.LatestVersion != "2.0.0" {
		t.Errorf("LatestVersion clobbered: got %q, want %q", got.LatestVersion, "2.0.0")
	}
}

// TestState_ReadsLegacyUpdateStateFile proves the migration-in-place: a file
// written by the legacy update checker (no last_seen_changelog key, with a now-
// dropped latest_url key) is read in place, its shared fields carried forward
// and the cursor starting empty.
func TestState_ReadsLegacyUpdateStateFile(t *testing.T) {
	dir := t.TempDir()
	legacy := "" +
		"checked_at: 2026-06-01T10:00:00Z\n" +
		"latest_version: 0.11.0\n" +
		"latest_url: https://github.com/schmitthub/clawker/releases/tag/v0.11.0\n" +
		"current_version: 0.10.0\n"
	path := filepath.Join(dir, consts.CliStateFile)
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := New(WithStateDirOverride(dir))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := st.Read()
	if got.LatestVersion != "0.11.0" {
		t.Errorf("LatestVersion = %q, want %q", got.LatestVersion, "0.11.0")
	}
	if got.CurrentVersion != "0.10.0" {
		t.Errorf("CurrentVersion = %q, want %q", got.CurrentVersion, "0.10.0")
	}
	if got.CheckedAt.IsZero() {
		t.Error("CheckedAt = zero, want the legacy timestamp carried forward")
	}
	if got.LastSeenChangelog != "" {
		t.Errorf("LastSeenChangelog = %q, want empty (not yet seeded)", got.LastSeenChangelog)
	}
}

// TestMigrations_Wired proves the migration scaffold is plumbed into the store
// load pipeline: a migration that rewrites a field runs on the discovered file
// and its result is reflected in the loaded snapshot (and re-saved). This guards
// the additive-migration contract even though the shipped Migrations() list is
// currently empty.
func TestMigrations_Wired(t *testing.T) {
	dir := t.TempDir()
	// Seed a file with a stale current_version the probe migration will rewrite.
	seed := "current_version: 0.0.1\nlast_seen_changelog: 0.0.1\n"
	path := filepath.Join(dir, consts.CliStateFile)
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := newStoreWithMigrations(dir, func(raw map[string]any) bool {
		if raw["current_version"] == "0.0.1" {
			raw["current_version"] = "9.9.9"
			return true
		}
		return false
	})
	if err != nil {
		t.Fatalf("newStoreWithMigrations: %v", err)
	}

	got := store.Read()
	if got.CurrentVersion != "9.9.9" {
		t.Errorf("migration did not run: CurrentVersion = %q, want %q", got.CurrentVersion, "9.9.9")
	}

	// Migration returning true must have triggered an atomic re-save.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if !strings.Contains(string(data), "9.9.9") {
		t.Errorf("migration not persisted; file:\n%s", data)
	}
}
