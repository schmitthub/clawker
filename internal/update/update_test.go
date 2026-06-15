package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/state"
)

func TestShouldCheckForUpdate_FreshCache(t *testing.T) {
	got := shouldCheckForUpdate(time.Now())
	if got {
		t.Error("shouldCheckForUpdate() = true, want false (fresh check)")
	}
}

func TestShouldCheckForUpdate_StaleCache(t *testing.T) {
	got := shouldCheckForUpdate(time.Now().Add(-25 * time.Hour))
	if !got {
		t.Error("shouldCheckForUpdate() = false, want true (stale check)")
	}
}

func TestShouldCheckForUpdate_FutureTimestampStale(t *testing.T) {
	// A future lastCheckedAt (clock skew, later corrected) must not be treated
	// as fresh: time.Since goes negative and would spuriously satisfy < cacheTTL,
	// suppressing checks until wall-clock catches up.
	got := shouldCheckForUpdate(time.Now().Add(48 * time.Hour))
	if !got {
		t.Error("shouldCheckForUpdate() = false, want true (future timestamp = stale)")
	}
}

func TestShouldCheckForUpdate_ZeroTimeNeverChecked(t *testing.T) {
	got := shouldCheckForUpdate(time.Time{})
	if !got {
		t.Error("shouldCheckForUpdate() = false, want true (zero time = never checked)")
	}
}

func TestCheckForUpdate_NewerVersion(t *testing.T) {
	srv := newReleaseServer("v2.0.0", "https://github.com/schmitthub/clawker/releases/tag/v2.0.0")
	defer srv.Close()

	info, err := checkForUpdate(context.Background(), nil, "1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected *ReleaseInfo for a newer release, got nil")
	}
	if info.CurrentVersion != "1.0.0" {
		t.Errorf("CurrentVersion = %q, want %q", info.CurrentVersion, "1.0.0")
	}
	if info.LatestVersion != "2.0.0" {
		t.Errorf("LatestVersion = %q, want %q", info.LatestVersion, "2.0.0")
	}
	if info.ReleaseURL != "https://github.com/schmitthub/clawker/releases/tag/v2.0.0" {
		t.Errorf("ReleaseURL = %q", info.ReleaseURL)
	}
}

func TestCheckForUpdate_SameVersion(t *testing.T) {
	srv := newReleaseServer("v1.0.0", "https://github.com/schmitthub/clawker/releases/tag/v1.0.0")
	defer srv.Close()

	// Not newer → nil result (nil MEANS "no newer release").
	info, err := checkForUpdate(context.Background(), nil, "1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Errorf("expected nil for same version (not newer), got %+v", info)
	}
}

func TestCheckForUpdate_OlderRemote(t *testing.T) {
	srv := newReleaseServer("v0.9.0", "https://github.com/schmitthub/clawker/releases/tag/v0.9.0")
	defer srv.Close()

	// Current is newer → nil result.
	info, err := checkForUpdate(context.Background(), nil, "1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Errorf("expected nil when current is newer, got %+v", info)
	}
}

func TestCheckForUpdate_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	info, err := checkForUpdate(context.Background(), nil, "1.0.0", srv.URL)
	if err == nil {
		t.Error("expected error on API failure, got nil")
	}
	if info != nil {
		t.Errorf("expected nil result on API error, got %+v", info)
	}
}

func TestCheckForUpdate_ContextCancellation(t *testing.T) {
	// Server that blocks until context is cancelled
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	release, err := fetchLatestReleaseFromURL(ctx, srv.URL)
	if err == nil {
		t.Error("expected error on cancelled context, got nil")
	}
	if release != nil {
		t.Errorf("expected nil release, got %+v", release)
	}
}

func TestCheckForUpdate_VPrefixHandling(t *testing.T) {
	srv := newReleaseServer("v2.0.0", "https://github.com/schmitthub/clawker/releases/tag/v2.0.0")
	defer srv.Close()

	// Pass version with v prefix
	info, err := checkForUpdate(context.Background(), nil, "v1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected *ReleaseInfo, got nil")
	}
	if info.CurrentVersion != "1.0.0" {
		t.Errorf("CurrentVersion = %q, want %q (v prefix should be stripped)", info.CurrentVersion, "1.0.0")
	}
}

// TestCheckForUpdate_TTLFreshSuppresses proves a TTL-fresh state short-circuits
// before any fetch: the server is wired to fail the test if hit.
func TestCheckForUpdate_TTLFreshSuppresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("fetch should not happen when state is TTL-fresh")
	}))
	defer srv.Close()

	st, err := state.New(state.WithStateDirOverride(t.TempDir()))
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	// Record a fresh check so the TTL gate suppresses.
	if err := st.RecordUpdateCheck(time.Now(), "1.0.0"); err != nil {
		t.Fatalf("RecordUpdateCheck: %v", err)
	}

	info, err := checkForUpdate(context.Background(), st, "1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Errorf("expected nil result when TTL-fresh, got %+v", info)
	}
}

// TestCheckForUpdate_NotNewerAdvancesCheckedAt is the regression guard for the
// persist-on-fetch-success contract: a NOT-NEWER fetch must still advance
// checked_at (and record latest_version). If persistence were keyed on isNewer,
// checked_at would never advance on the common not-newer path, the TTL gate
// would never throttle, and clawker would hit the GitHub API every run.
func TestCheckForUpdate_NotNewerAdvancesCheckedAt(t *testing.T) {
	srv := newReleaseServer("v1.0.0", "https://github.com/schmitthub/clawker/releases/tag/v1.0.0")
	defer srv.Close()

	st, err := state.New(state.WithStateDirOverride(t.TempDir()))
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	if !st.LastCheckedAt().IsZero() {
		t.Fatalf("precondition: LastCheckedAt should be zero, got %v", st.LastCheckedAt())
	}

	before := time.Now()
	// Same version as current → not newer → nil result, but the fetch succeeded.
	info, err := checkForUpdate(context.Background(), st, "1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Fatalf("expected nil result (not newer), got %+v", info)
	}

	// checked_at must have advanced despite the not-newer outcome.
	got := st.LastCheckedAt()
	if got.IsZero() {
		t.Fatal("checked_at did not advance on a not-newer fetch (persist skipped)")
	}
	if got.Before(before) {
		t.Errorf("checked_at = %v, want >= %v", got, before)
	}
	if st.LatestVersion() != "1.0.0" {
		t.Errorf("latest_version = %q, want %q", st.LatestVersion(), "1.0.0")
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest  string
		current string
		want    bool
	}{
		{"2.0.0", "1.0.0", true},
		{"1.1.0", "1.0.0", true},
		{"1.0.1", "1.0.0", true},
		{"1.0.0", "1.0.0", false},
		{"0.9.0", "1.0.0", false},
		{"1.0.0", "2.0.0", false},
		{"0.2.0", "0.1.3", true},
		{"0.1.4", "0.1.3", true},
		{"0.1.3", "0.1.3", false},
		// Unparseable versions — fallback returns false (don't claim newer).
		// This is also where a non-release build is handled: an unparseable
		// current ("DEV" placeholder, "nightly", etc.) never reports an upgrade,
		// so no explicit dev-build gate is needed in shouldCheckForUpdate.
		{"invalid", "1.0.0", false},
		{"1.0.0", "invalid", false},
		{"invalid", "invalid", false},
		{"foo", "bar", false},
		{"nightly", "1.0.0", false},
		{"2.0.0", "DEV", false},
	}

	for _, tt := range tests {
		t.Run(tt.latest+"_vs_"+tt.current, func(t *testing.T) {
			got := isNewer(tt.latest, tt.current)
			if got != tt.want {
				t.Errorf("isNewer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
			}
		})
	}
}

func TestCheckForUpdate_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer srv.Close()

	ctx := context.Background()
	release, err := fetchLatestReleaseFromURL(ctx, srv.URL)
	if err == nil {
		t.Error("expected error on malformed JSON, got nil")
	}
	if release != nil {
		t.Errorf("expected nil release, got %+v", release)
	}
}

func TestCheckForUpdate_EmptyTagName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{TagName: "", HTMLURL: "https://example.com"})
	}))
	defer srv.Close()

	ctx := context.Background()
	release, err := fetchLatestReleaseFromURL(ctx, srv.URL)
	if err == nil {
		t.Error("expected error on empty tag_name, got nil")
	}
	if release != nil {
		t.Errorf("expected nil release, got %+v", release)
	}
	if err != nil && !strings.Contains(err.Error(), "empty tag_name") {
		t.Errorf("expected error to mention 'empty tag_name', got: %v", err)
	}
}

// --- test helpers ---

// newReleaseServer returns an httptest server that responds with a GitHub release.
func newReleaseServer(tagName, htmlURL string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{
			TagName: tagName,
			HTMLURL: htmlURL,
		})
	}))
}
