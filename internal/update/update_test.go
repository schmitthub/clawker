package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShouldCheckForUpdate_Suppressed(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
		version string
		want    bool
	}{
		{
			name:    "suppressed by CLAWKER_NO_UPDATE_NOTIFIER",
			envVars: map[string]string{"CLAWKER_NO_UPDATE_NOTIFIER": "1"},
			version: "1.0.0",
			want:    false,
		},
		{
			name:    "suppressed by CI",
			envVars: map[string]string{"CI": "true"},
			version: "1.0.0",
			want:    false,
		},
		{
			name:    "suppressed by DEV version",
			version: "DEV",
			want:    false,
		},
		{
			name:    "allowed when no suppression",
			version: "1.0.0",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean env
			for _, key := range []string{"CLAWKER_NO_UPDATE_NOTIFIER", "CI"} {
				t.Setenv(key, "")
				os.Unsetenv(key)
			}
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			got := ShouldCheckForUpdate("", tt.version)
			if got != tt.want {
				t.Errorf("ShouldCheckForUpdate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldCheckForUpdate_FreshCache(t *testing.T) {
	clearUpdateEnv(t)

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.yaml")

	entry := StateEntry{
		CheckedAt:      time.Now(),
		LatestVersion:  "1.0.0",
		CurrentVersion: "1.0.0",
	}
	if err := writeState(stateFile, entry); err != nil {
		t.Fatal(err)
	}

	got := ShouldCheckForUpdate(stateFile, "1.0.0")
	if got {
		t.Error("ShouldCheckForUpdate() = true, want false (fresh cache)")
	}
}

func TestShouldCheckForUpdate_StaleCache(t *testing.T) {
	clearUpdateEnv(t)

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.yaml")

	entry := StateEntry{
		CheckedAt:      time.Now().Add(-25 * time.Hour),
		LatestVersion:  "1.0.0",
		CurrentVersion: "1.0.0",
	}
	if err := writeState(stateFile, entry); err != nil {
		t.Fatal(err)
	}

	got := ShouldCheckForUpdate(stateFile, "1.0.0")
	if !got {
		t.Error("ShouldCheckForUpdate() = false, want true (stale cache)")
	}
}

func TestCheckForUpdate_NewerVersion(t *testing.T) {
	clearUpdateEnv(t)

	srv := newReleaseServer("v2.0.0", "https://github.com/schmitthub/clawker/releases/tag/v2.0.0")
	defer srv.Close()

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.yaml")

	result, err := checkForUpdateWithURL(t, stateFile, "1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected CheckResult, got nil")
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

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.yaml")

	result, err := checkForUpdateWithURL(t, stateFile, "1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil (same version), got %+v", result)
	}
}

func TestCheckForUpdate_OlderRemote(t *testing.T) {
	clearUpdateEnv(t)

	srv := newReleaseServer("v0.9.0", "https://github.com/schmitthub/clawker/releases/tag/v0.9.0")
	defer srv.Close()

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.yaml")

	result, err := checkForUpdateWithURL(t, stateFile, "1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil (current is newer), got %+v", result)
	}
}

func TestCheckForUpdate_APIError(t *testing.T) {
	clearUpdateEnv(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.yaml")

	result, err := checkForUpdateWithURL(t, stateFile, "1.0.0", srv.URL)
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

func TestCheckForUpdate_CacheWriteReadRoundTrip(t *testing.T) {
	clearUpdateEnv(t)

	srv := newReleaseServer("v2.0.0", "https://github.com/schmitthub/clawker/releases/tag/v2.0.0")
	defer srv.Close()

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.yaml")

	// First call — should hit API and write cache
	result, err := checkForUpdateWithURL(t, stateFile, "1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected CheckResult on first call, got nil")
	}

	// Verify cache was written
	entry, err := readState(stateFile)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if entry.LatestVersion != "2.0.0" {
		t.Errorf("cached LatestVersion = %q, want %q", entry.LatestVersion, "2.0.0")
	}
	if entry.CurrentVersion != "1.0.0" {
		t.Errorf("cached CurrentVersion = %q, want %q", entry.CurrentVersion, "1.0.0")
	}
	if time.Since(entry.CheckedAt) > time.Minute {
		t.Errorf("cached CheckedAt is too old: %v", entry.CheckedAt)
	}

	// Second call — should be suppressed by fresh cache
	shouldCheck := ShouldCheckForUpdate(stateFile, "1.0.0")
	if shouldCheck {
		t.Error("ShouldCheckForUpdate() = true after fresh write, want false")
	}
}

func TestCheckForUpdate_VPrefixHandling(t *testing.T) {
	clearUpdateEnv(t)

	srv := newReleaseServer("v2.0.0", "https://github.com/schmitthub/clawker/releases/tag/v2.0.0")
	defer srv.Close()

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.yaml")

	// Pass version with v prefix
	result, err := checkForUpdateWithURL(t, stateFile, "v1.0.0", srv.URL)
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

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input string
		want  []int
	}{
		{"1.2.3", []int{1, 2, 3}},
		{"0.1.3", []int{0, 1, 3}},
		{"10.20.30", []int{10, 20, 30}},
		{"1.2.3-beta.1", []int{1, 2, 3}}, // pre-release stripped
		{"invalid", nil},
		{"1.2", nil},
		{"1.2.3.4", nil},
		{"a.b.c", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseSemver(tt.input)
			if tt.want == nil {
				if got != nil {
					t.Errorf("parseSemver(%q) = %v, want nil", tt.input, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseSemver(%q) = nil, want %v", tt.input, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("parseSemver(%q)[%d] = %d, want %d", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestCheckForUpdate_StateFileIsValidYAML(t *testing.T) {
	clearUpdateEnv(t)

	srv := newReleaseServer("v2.0.0", "https://github.com/schmitthub/clawker/releases/tag/v2.0.0")
	defer srv.Close()

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "update-state.yaml")

	result, err := checkForUpdateWithURL(t, stateFile, "1.0.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected CheckResult, got nil")
	}

	// Verify state file is valid YAML (not JSON)
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}

	content := string(data)
	if strings.Contains(content, `"checked_at"`) {
		t.Errorf("state file looks like JSON, expected YAML:\n%s", content)
	}
	if !strings.Contains(content, "checked_at:") {
		t.Errorf("state file missing YAML key 'checked_at:':\n%s", content)
	}
	if !strings.Contains(content, "latest_version: 2.0.0") {
		t.Errorf("state file missing expected content:\n%s", content)
	}

	t.Logf("State file contents:\n%s", content)
}

func TestCheckForUpdate_GoroutineChannelPattern(t *testing.T) {
	clearUpdateEnv(t)

	srv := newReleaseServer("v3.0.0", "https://github.com/schmitthub/clawker/releases/tag/v3.0.0")
	defer srv.Close()

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "update-state.yaml")

	// Simulate the exact pattern used in Main():
	// cancellable context + unbuffered channel + blocking read
	ch := make(chan *CheckResult)
	go func() {
		rel, _ := checkForUpdateWithURL(t, stateFile, "1.0.0", srv.URL)
		ch <- rel
	}()

	result := <-ch

	if result == nil {
		t.Fatal("expected CheckResult from goroutine, got nil")
	}
	if result.LatestVersion != "3.0.0" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "3.0.0")
	}
	if result.CurrentVersion != "1.0.0" {
		t.Errorf("CurrentVersion = %q, want %q", result.CurrentVersion, "1.0.0")
	}
}

// --- test helpers ---

// clearUpdateEnv unsets env vars that suppress update checks.
func clearUpdateEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"CLAWKER_NO_UPDATE_NOTIFIER", "CI"} {
		t.Setenv(key, "")
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

// checkForUpdateWithURL is a test helper that calls fetchLatestReleaseFromURL
// directly with the test server URL, mimicking CheckForUpdate's logic.
func checkForUpdateWithURL(t *testing.T, stateFilePath, currentVersion, apiURL string) (*CheckResult, error) {
	t.Helper()

	if !ShouldCheckForUpdate(stateFilePath, currentVersion) {
		return nil, nil
	}

	ctx := context.Background()
	release, err := fetchLatestReleaseFromURL(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	currentBare := strings.TrimPrefix(currentVersion, "v")

	entry := StateEntry{
		CheckedAt:      time.Now(),
		LatestVersion:  latestVersion,
		LatestURL:      release.HTMLURL,
		CurrentVersion: currentBare,
	}
	if stateFilePath != "" {
		_ = writeState(stateFilePath, entry)
	}

	if !isNewer(latestVersion, currentBare) {
		return nil, nil
	}

	return &CheckResult{
		CurrentVersion: currentBare,
		LatestVersion:  latestVersion,
		ReleaseURL:     release.HTMLURL,
	}, nil
}
