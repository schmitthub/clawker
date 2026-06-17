package update

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/httpmock"
	"github.com/schmitthub/clawker/internal/state"
	statemocks "github.com/schmitthub/clawker/internal/state/mocks"
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
	reg := releaseStub("v2.0.0", "https://github.com/schmitthub/clawker/releases/tag/v2.0.0")
	st := statemocks.NewBlankState()

	info, err := CheckForUpdate(context.Background(), reg.Client(), st, "1.0.0", consts.GitHubRepo)
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
	reg := releaseStub("v1.0.0", "https://github.com/schmitthub/clawker/releases/tag/v1.0.0")
	st := statemocks.NewBlankState()

	// Not newer → nil result (nil MEANS "no newer release").
	info, err := CheckForUpdate(context.Background(), reg.Client(), st, "1.0.0", consts.GitHubRepo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Errorf("expected nil for same version (not newer), got %+v", info)
	}
}

func TestCheckForUpdate_OlderRemote(t *testing.T) {
	reg := releaseStub("v0.9.0", "https://github.com/schmitthub/clawker/releases/tag/v0.9.0")
	st := statemocks.NewBlankState()

	// Current is newer → nil result.
	info, err := CheckForUpdate(context.Background(), reg.Client(), st, "1.0.0", consts.GitHubRepo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Errorf("expected nil when current is newer, got %+v", info)
	}
}

func TestCheckForUpdate_APIError(t *testing.T) {
	reg := &httpmock.Registry{}
	reg.Register(
		httpmock.REST(http.MethodGet, "/releases/latest"),
		httpmock.StatusStringResponse(http.StatusInternalServerError, ""),
	)
	st := statemocks.NewBlankState()

	info, err := CheckForUpdate(context.Background(), reg.Client(), st, "1.0.0", consts.GitHubRepo)
	if err == nil {
		t.Error("expected error on API failure, got nil")
	}
	if info != nil {
		t.Errorf("expected nil result on API error, got %+v", info)
	}
}

func TestCheckForUpdate_ContextCancellation(t *testing.T) {
	reg := releaseStub("v2.0.0", "https://example.com")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	release, err := getLatestReleaseInfo(ctx, reg.Client(), consts.GitHubRepo)
	if err == nil {
		t.Error("expected error on cancelled context, got nil")
	}
	if release != nil {
		t.Errorf("expected nil release, got %+v", release)
	}
}

func TestCheckForUpdate_VPrefixHandling(t *testing.T) {
	reg := releaseStub("v2.0.0", "https://github.com/schmitthub/clawker/releases/tag/v2.0.0")
	st := statemocks.NewBlankState()

	// Pass version with v prefix
	info, err := CheckForUpdate(context.Background(), reg.Client(), st, "v1.0.0", consts.GitHubRepo)
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
// before any fetch: the registry records zero requests and nothing is persisted.
func TestCheckForUpdate_TTLFreshSuppresses(t *testing.T) {
	reg := releaseStub("v2.0.0", "https://example.com")

	// TTL-fresh state: CheckedAt is "now", so the freshness gate suppresses
	// before any fetch or persist.
	m := statemocks.NewBlankState()
	m.StateFunc = func() *state.State { return &state.State{CheckedAt: time.Now()} }

	info, err := CheckForUpdate(context.Background(), reg.Client(), m, "1.0.0", consts.GitHubRepo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Errorf("expected nil result when TTL-fresh, got %+v", info)
	}
	if got := len(reg.Requests); got != 0 {
		t.Errorf("fetch happened on TTL-fresh state: %d requests, want 0", got)
	}
	if got := len(m.RecordUpdateCheckCalls()); got != 0 {
		t.Errorf("RecordUpdateCheck calls = %d, want 0 (no fetch, no persist)", got)
	}
}

// TestCheckForUpdate_NotNewerAdvancesCheckedAt is the regression guard for the
// persist-on-fetch-success contract: a NOT-NEWER fetch must still advance
// checked_at (and record latest_version). If persistence were keyed on the
// newer/not-newer comparison, checked_at would never advance on the common
// not-newer path, the TTL gate would never throttle, and clawker would hit the
// GitHub API every run.
func TestCheckForUpdate_NotNewerAdvancesCheckedAt(t *testing.T) {
	reg := releaseStub("v1.0.0", "https://github.com/schmitthub/clawker/releases/tag/v1.0.0")

	// Blank state's CheckedAt is zero → never checked → the freshness gate lets
	// the check run.
	m := statemocks.NewBlankState()

	// Same version as current → not newer → nil result, but the fetch succeeded.
	info, err := CheckForUpdate(context.Background(), reg.Client(), m, "1.0.0", consts.GitHubRepo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Fatalf("expected nil result (not newer), got %+v", info)
	}

	// The mutation this guards: moving RecordUpdateCheck after the not-newer
	// return would record zero calls. Persistence must fire on the not-newer
	// fetch, before the newer/not-newer decision.
	calls := m.RecordUpdateCheckCalls()
	if len(calls) != 1 {
		t.Fatalf("RecordUpdateCheck calls = %d, want 1 (persist on not-newer fetch)", len(calls))
	}
	if calls[0].LatestVersion != "1.0.0" {
		t.Errorf("recorded latest_version = %q, want %q", calls[0].LatestVersion, "1.0.0")
	}
}

func TestCheckForUpdate_MalformedJSON(t *testing.T) {
	reg := &httpmock.Registry{}
	reg.Register(
		httpmock.REST(http.MethodGet, "/releases/latest"),
		httpmock.StringResponse("not json at all"),
	)

	release, err := getLatestReleaseInfo(context.Background(), reg.Client(), consts.GitHubRepo)
	if err == nil {
		t.Error("expected error on malformed JSON, got nil")
	}
	if release != nil {
		t.Errorf("expected nil release, got %+v", release)
	}
}

// TestCheckForUpdate_EmptyTagName: an empty tag_name from the API is rejected at
// the semver parse inside CheckForUpdate (there is no separate empty-tag guard —
// semver.NewVersion("") fails), so the public contract still surfaces an error
// and never reports a release.
func TestCheckForUpdate_EmptyTagName(t *testing.T) {
	reg := releaseStub("", "https://example.com")
	st := statemocks.NewBlankState()

	info, err := CheckForUpdate(context.Background(), reg.Client(), st, "1.0.0", consts.GitHubRepo)
	if err == nil {
		t.Error("expected error on empty tag_name, got nil")
	}
	if info != nil {
		t.Errorf("expected nil result on empty tag_name, got %+v", info)
	}
}

// --- test helpers ---

// releaseStub registers a GitHub "latest release" responder on a fresh httpmock
// registry and returns it. reg.Client() is injected into CheckForUpdate, so the
// test stays off live api.github.com with no URL seam in production code.
func releaseStub(tagName, htmlURL string) *httpmock.Registry {
	reg := &httpmock.Registry{}
	reg.Register(
		httpmock.REST(http.MethodGet, "/releases/latest"),
		httpmock.JSONResponse(githubRelease{TagName: tagName, HTMLURL: htmlURL}),
	)
	return reg
}
