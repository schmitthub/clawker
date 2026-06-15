package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestShouldCheckForUpdate_Suppressed(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
		want    bool
	}{
		{
			name:    "suppressed by CLAWKER_NO_UPDATE_NOTIFIER",
			envVars: map[string]string{"CLAWKER_NO_UPDATE_NOTIFIER": "1"},
			want:    false,
		},
		{
			name:    "suppressed by CI",
			envVars: map[string]string{"CI": "true"},
			want:    false,
		},
		{
			name: "allowed when no suppression",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearUpdateEnv(t)
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			// Zero lastCheckedAt → "never checked", so TTL never suppresses.
			got := ShouldCheckForUpdate(time.Time{})
			if got != tt.want {
				t.Errorf("ShouldCheckForUpdate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldCheckForUpdate_FreshCache(t *testing.T) {
	clearUpdateEnv(t)

	got := ShouldCheckForUpdate(time.Now())
	if got {
		t.Error("ShouldCheckForUpdate() = true, want false (fresh check)")
	}
}

func TestShouldCheckForUpdate_StaleCache(t *testing.T) {
	clearUpdateEnv(t)

	got := ShouldCheckForUpdate(time.Now().Add(-25 * time.Hour))
	if !got {
		t.Error("ShouldCheckForUpdate() = false, want true (stale check)")
	}
}

func TestShouldCheckForUpdate_FutureTimestampStale(t *testing.T) {
	clearUpdateEnv(t)

	// A future lastCheckedAt (clock skew, later corrected) must not be treated
	// as fresh: time.Since goes negative and would spuriously satisfy < CacheTTL,
	// suppressing checks until wall-clock catches up.
	got := ShouldCheckForUpdate(time.Now().Add(48 * time.Hour))
	if !got {
		t.Error("ShouldCheckForUpdate() = false, want true (future timestamp = stale)")
	}
}

func TestShouldCheckForUpdate_ZeroTimeNeverChecked(t *testing.T) {
	clearUpdateEnv(t)

	got := ShouldCheckForUpdate(time.Time{})
	if !got {
		t.Error("ShouldCheckForUpdate() = false, want true (zero time = never checked)")
	}
}

func TestCheckForUpdate_NewerVersion(t *testing.T) {
	clearUpdateEnv(t)

	srv := newReleaseServer("v2.0.0", "https://github.com/schmitthub/clawker/releases/tag/v2.0.0")
	defer srv.Close()

	result, err := checkForUpdate(context.Background(), time.Time{}, "1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected CheckResult, got nil")
	}
	if !result.IsNewer {
		t.Error("expected IsNewer = true for 2.0.0 over 1.0.0")
	}
	if result.CurrentVersion != "1.0.0" {
		t.Errorf("CurrentVersion = %q, want %q", result.CurrentVersion, "1.0.0")
	}
	if result.LatestVersion != "2.0.0" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "2.0.0")
	}
	if result.ReleaseURL != "https://github.com/schmitthub/clawker/releases/tag/v2.0.0" {
		t.Errorf("ReleaseURL = %q", result.ReleaseURL)
	}
}

func TestCheckForUpdate_SameVersion(t *testing.T) {
	clearUpdateEnv(t)

	srv := newReleaseServer("v1.0.0", "https://github.com/schmitthub/clawker/releases/tag/v1.0.0")
	defer srv.Close()

	result, err := checkForUpdate(context.Background(), time.Time{}, "1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected CheckResult (with IsNewer=false), got nil")
	}
	if result.IsNewer {
		t.Errorf("expected IsNewer = false for same version, got %+v", result)
	}
	if result.LatestVersion != "1.0.0" {
		t.Errorf("LatestVersion = %q, want %q (fetched data always populated)", result.LatestVersion, "1.0.0")
	}
}

func TestCheckForUpdate_OlderRemote(t *testing.T) {
	clearUpdateEnv(t)

	srv := newReleaseServer("v0.9.0", "https://github.com/schmitthub/clawker/releases/tag/v0.9.0")
	defer srv.Close()

	result, err := checkForUpdate(context.Background(), time.Time{}, "1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected CheckResult (with IsNewer=false), got nil")
	}
	if result.IsNewer {
		t.Errorf("expected IsNewer = false (current is newer), got %+v", result)
	}
}

func TestCheckForUpdate_APIError(t *testing.T) {
	clearUpdateEnv(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	result, err := checkForUpdate(context.Background(), time.Time{}, "1.0.0", srv.URL)
	if err == nil {
		t.Error("expected error on API failure, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result on API error, got %+v", result)
	}
}

func TestCheckForUpdate_ContextCancellation(t *testing.T) {
	clearUpdateEnv(t)

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
	clearUpdateEnv(t)

	srv := newReleaseServer("v2.0.0", "https://github.com/schmitthub/clawker/releases/tag/v2.0.0")
	defer srv.Close()

	// Pass version with v prefix
	result, err := checkForUpdate(context.Background(), time.Time{}, "v1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected CheckResult, got nil")
	}
	if result.CurrentVersion != "1.0.0" {
		t.Errorf("CurrentVersion = %q, want %q (v prefix should be stripped)", result.CurrentVersion, "1.0.0")
	}
}

func TestCheckForUpdate_SuppressedReturnsNil(t *testing.T) {
	clearUpdateEnv(t)
	t.Setenv("CLAWKER_NO_UPDATE_NOTIFIER", "1")

	srv := newReleaseServer("v2.0.0", "https://example.com")
	defer srv.Close()

	result, err := CheckForUpdate(context.Background(), nil, "1.0.0", "schmitthub/clawker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when suppressed, got %+v", result)
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
		// so no explicit dev-build gate is needed in ShouldCheckForUpdate.
		{"invalid", "1.0.0", false},
		{"1.0.0", "invalid", false},
		{"invalid", "invalid", false},
		{"foo", "bar", false},
		{"nightly", "1.0.0", false},
		{"2.0.0", "DEV", false},
	}

	for _, tt := range tests {
		t.Run(tt.latest+"_vs_"+tt.current, func(t *testing.T) {
			got := IsNewer(tt.latest, tt.current)
			if got != tt.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
			}
		})
	}
}

func TestCheckForUpdate_MalformedJSON(t *testing.T) {
	clearUpdateEnv(t)

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
	clearUpdateEnv(t)

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

// clearUpdateEnv unsets env vars that suppress update checks.
// Uses manual save/restore instead of t.Setenv to properly unset (not empty) vars.
func clearUpdateEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"CLAWKER_NO_UPDATE_NOTIFIER", "CI"} {
		if orig, ok := os.LookupEnv(key); ok {
			t.Cleanup(func() { os.Setenv(key, orig) })
		} else {
			t.Cleanup(func() { os.Unsetenv(key) })
		}
		os.Unsetenv(key)
	}
}

// newReleaseServer returns an httptest server that responds with a GitHub release.
func newReleaseServer(tagName, htmlURL string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{
			TagName: tagName,
			HTMLURL: htmlURL,
		})
	}))
}
