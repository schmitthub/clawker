package changelog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/state"
)

const loaderFixture = `# Changelog

## [0.12.0] - 2026-06-11
<!-- clawker: tag=feature -->

### Added

- **A feature.**
`

// countingServer returns an httptest server serving loaderFixture and a pointer
// to the request counter so tests can assert whether the network was hit.
func countingServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(loaderFixture))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func newTestState(t *testing.T) *state.State {
	t.Helper()
	st, err := state.New(state.WithStateDirOverride(t.TempDir()))
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	return st
}

// TestLoader_ForceRefresh_Fetches asserts that forceRefresh always hits the
// network, writes the cache, and records the fetch timestamp.
func TestLoader_ForceRefresh_Fetches(t *testing.T) {
	srv, hits := countingServer(t)
	st := newTestState(t)
	cachePath := filepath.Join(t.TempDir(), "cache.md")
	l := NewLoader(srv.Client(), srv.URL, cachePath, st, DefaultTTL)

	entries, err := l.Load(context.Background(), true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 || entries[0].Version != "0.12.0" {
		t.Fatalf("entries = %+v, want one 0.12.0 entry", entries)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", hits.Load())
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Errorf("cache file not written: %v", err)
	}
	if st.ChangelogFetchedAt().IsZero() {
		t.Error("fetch timestamp not recorded")
	}
}

// TestLoader_FreshCache_NoFetch asserts that a fresh fetch timestamp makes Load
// read the cache without hitting the network.
func TestLoader_FreshCache_NoFetch(t *testing.T) {
	srv, hits := countingServer(t)
	st := newTestState(t)
	cachePath := filepath.Join(t.TempDir(), "cache.md")
	if err := os.WriteFile(cachePath, []byte(loaderFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordChangelogFetch(time.Now()); err != nil {
		t.Fatal(err)
	}
	l := NewLoader(srv.Client(), srv.URL, cachePath, st, DefaultTTL)

	entries, err := l.Load(context.Background(), false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want one", entries)
	}
	if hits.Load() != 0 {
		t.Errorf("hits = %d, want 0 (fresh cache should not fetch)", hits.Load())
	}
}

// TestLoader_FreshCache_FileMissing_Fetches asserts the recovery branch where
// the TTL gate considers the cache fresh (a fetch timestamp was recorded) but
// the cache file was deleted out-of-band: Load falls through to a network fetch,
// persists, and returns the parsed entries rather than failing.
func TestLoader_FreshCache_FileMissing_Fetches(t *testing.T) {
	srv, hits := countingServer(t)
	st := newTestState(t)
	// Cache path points at a file that does not exist.
	cachePath := filepath.Join(t.TempDir(), "missing.md")
	// Record a fresh fetch timestamp so the TTL gate reports the cache as fresh.
	if err := st.RecordChangelogFetch(time.Now()); err != nil {
		t.Fatal(err)
	}
	l := NewLoader(srv.Client(), srv.URL, cachePath, st, DefaultTTL)

	entries, err := l.Load(context.Background(), false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 || entries[0].Version != "0.12.0" {
		t.Fatalf("entries = %+v, want one 0.12.0 entry", entries)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1 (fresh-but-missing cache should fetch)", hits.Load())
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Errorf("cache file not written after recovery fetch: %v", err)
	}
}

// TestLoader_StaleCache_Fetches asserts that a stale fetch timestamp triggers a
// network fetch even without forceRefresh. The clock is injected so the staleness
// is deterministic.
func TestLoader_StaleCache_Fetches(t *testing.T) {
	srv, hits := countingServer(t)
	st := newTestState(t)
	cachePath := filepath.Join(t.TempDir(), "cache.md")
	if err := st.RecordChangelogFetch(time.Now()); err != nil {
		t.Fatal(err)
	}
	l := NewLoader(srv.Client(), srv.URL, cachePath, st, DefaultTTL)
	// Advance the clock past the TTL so the recorded fetch is stale.
	l.now = func() time.Time { return time.Now().Add(2 * DefaultTTL) }

	if _, err := l.Load(context.Background(), false); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1 (stale cache should fetch)", hits.Load())
	}
}

// TestLoader_FetchError_FallsBackToCache asserts that a network failure falls
// back to the on-disk cache when one exists.
func TestLoader_FetchError_FallsBackToCache(t *testing.T) {
	st := newTestState(t)
	cachePath := filepath.Join(t.TempDir(), "cache.md")
	if err := os.WriteFile(cachePath, []byte(loaderFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	// Unreachable URL → fetch fails; cache present → fallback succeeds.
	l := NewLoader(http.DefaultClient, "http://127.0.0.1:0/nope", cachePath, st, DefaultTTL)

	entries, err := l.Load(context.Background(), true)
	if err != nil {
		t.Fatalf("Load should fall back to cache, got error: %v", err)
	}
	if len(entries) != 1 || entries[0].Version != "0.12.0" {
		t.Fatalf("entries = %+v, want cached 0.12.0 entry", entries)
	}
}

// TestLoader_FetchError_NoCache_ReturnsError asserts that a network failure with
// no cache returns the error (the caller degrades to "no changelog to show").
func TestLoader_FetchError_NoCache_ReturnsError(t *testing.T) {
	st := newTestState(t)
	cachePath := filepath.Join(t.TempDir(), "missing.md")
	l := NewLoader(http.DefaultClient, "http://127.0.0.1:0/nope", cachePath, st, DefaultTTL)

	if _, err := l.Load(context.Background(), true); err == nil {
		t.Fatal("expected an error when fetch fails and no cache exists")
	}
}

// TestLoader_NilState_AlwaysFetches asserts that a nil state store disables the
// TTL gate (every Load fetches).
func TestLoader_NilState_AlwaysFetches(t *testing.T) {
	srv, hits := countingServer(t)
	cachePath := filepath.Join(t.TempDir(), "cache.md")
	l := NewLoader(srv.Client(), srv.URL, cachePath, nil, DefaultTTL)

	for range 2 {
		if _, err := l.Load(context.Background(), false); err != nil {
			t.Fatalf("Load: %v", err)
		}
	}
	if hits.Load() != 2 {
		t.Errorf("hits = %d, want 2 (nil state should always fetch)", hits.Load())
	}
}
