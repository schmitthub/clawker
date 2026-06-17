package update

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/httpmock"
	"github.com/schmitthub/clawker/internal/state"
	statemocks "github.com/schmitthub/clawker/internal/state/mocks"
)

func TestShouldCheckForUpdate(t *testing.T) {
	tests := []struct {
		name        string
		lastChecked time.Time
		want        bool
	}{
		{"fresh check within TTL suppresses", time.Now(), false},
		{"stale check past TTL runs", time.Now().Add(-25 * time.Hour), true},
		// A future lastCheckedAt (clock skew, later corrected) must NOT be treated
		// as fresh: time.Since goes negative and would spuriously satisfy < cacheTTL,
		// suppressing checks until wall-clock catches up.
		{"future timestamp treated as stale", time.Now().Add(48 * time.Hour), true},
		{"zero time never checked", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldCheckForUpdate(tt.lastChecked); got != tt.want {
				t.Errorf("shouldCheckForUpdate() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCheckForUpdate_VersionComparison drives the newer/same/older decision (and
// v-prefix stripping) through the public CheckForUpdate: a non-nil *ReleaseInfo
// MEANS "strictly newer", nil means "not newer". Each case uses a blank state so
// the freshness gate lets the fetch run.
func TestCheckForUpdate_VersionComparison(t *testing.T) {
	const newerURL = "https://github.com/schmitthub/clawker/releases/tag/v2.0.0"
	tests := []struct {
		name    string
		tag     string
		htmlURL string
		current string
		want    *ReleaseInfo // nil = expect no newer release
		wantErr error        // non-nil = expect error (e.g. unparseable current version)
	}{
		{
			name:    "newer version returns release info",
			tag:     "v2.0.0",
			htmlURL: newerURL,
			current: "1.0.0",
			want:    &ReleaseInfo{CurrentVersion: "1.0.0", LatestVersion: "2.0.0", ReleaseURL: newerURL},
		},
		{
			name:    "same version is not newer",
			tag:     "v1.0.0",
			htmlURL: "https://github.com/schmitthub/clawker/releases/tag/v1.0.0",
			current: "1.0.0",
			want:    nil,
		},
		{
			name:    "older remote is not newer",
			tag:     "v0.9.0",
			htmlURL: "https://github.com/schmitthub/clawker/releases/tag/v0.9.0",
			current: "1.0.0",
			want:    nil,
		},
		{
			name:    "v-prefixed current version is stripped",
			tag:     "v2.0.0",
			htmlURL: newerURL,
			current: "v1.0.0",
			want:    &ReleaseInfo{CurrentVersion: "1.0.0", LatestVersion: "2.0.0", ReleaseURL: newerURL},
		},
		{
			name:    "dirty tag pre-release is newer than current",
			tag:     "0.12.3-26-g1476a75f",
			htmlURL: newerURL,
			current: "0.12.2",
			want:    &ReleaseInfo{CurrentVersion: "0.12.2", LatestVersion: "0.12.3-26-g1476a75f", ReleaseURL: newerURL},
		},
		{
			name:    "alpha pre-release is newer than current",
			tag:     "0.12.3-alpha",
			htmlURL: newerURL,
			current: "0.12.2",
			want:    &ReleaseInfo{CurrentVersion: "0.12.2", LatestVersion: "0.12.3-alpha", ReleaseURL: newerURL},
		},
		{
			name:    "rc pre-release is newer than current",
			tag:     "0.12.3-rc.1",
			htmlURL: newerURL,
			current: "0.12.2",
			want:    &ReleaseInfo{CurrentVersion: "0.12.2", LatestVersion: "0.12.3-rc.1", ReleaseURL: newerURL},
		},
		{
			name:    "dev tag",
			tag:     "DEV",
			htmlURL: newerURL,
			current: "0.12.2",
			wantErr: semver.ErrInvalidSemVer, // DEV is not parseable semver, so CheckForUpdate returns an error (not a nil *ReleaseInfo)
		},
		{
			name:    "rc pre-release is newer than current",
			tag:     "(devel)",
			htmlURL: newerURL,
			current: "0.12.2",
			wantErr: semver.ErrInvalidSemVer, // (devel) is not parseable semver, so CheckForUpdate returns an error (not a nil *ReleaseInfo)
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := releaseStub(tt.tag, tt.htmlURL)
			st := statemocks.NewBlankState()

			info, err := CheckForUpdate(context.Background(), reg.Client(), st, tt.current, consts.GitHubRepo)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.want == nil {
				if info != nil {
					t.Errorf("expected nil (not newer), got %+v", info)
				}
				return
			}
			if info == nil {
				t.Fatal("expected *ReleaseInfo, got nil")
			}
			if *info != *tt.want {
				t.Errorf("ReleaseInfo = %+v, want %+v", *info, *tt.want)
			}
		})
	}
}

// TestCheckForUpdate_Errors covers fetch/parse failures that the public
// CheckForUpdate must surface as (nil, error).
func TestCheckForUpdate_Errors(t *testing.T) {
	tests := []struct {
		name string
		reg  func() *httpmock.Registry
	}{
		{
			name: "API 500 surfaces error",
			reg: func() *httpmock.Registry {
				reg := &httpmock.Registry{}
				reg.Register(
					httpmock.REST(http.MethodGet, "/releases/latest"),
					httpmock.StatusStringResponse(http.StatusInternalServerError, ""),
				)
				return reg
			},
		},
		{
			// An empty tag_name is rejected at the semver parse inside CheckForUpdate
			// (there is no separate empty-tag guard — semver.NewVersion("") fails).
			name: "empty tag_name fails semver parse",
			reg:  func() *httpmock.Registry { return releaseStub("", "https://example.com") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := tt.reg()
			st := statemocks.NewBlankState()

			info, err := CheckForUpdate(context.Background(), reg.Client(), st, "1.0.0", consts.GitHubRepo)
			if err == nil {
				t.Error("expected error, got nil")
			}
			if info != nil {
				t.Errorf("expected nil result on error, got %+v", info)
			}
		})
	}
}

// TestGetLatestReleaseInfo_Errors covers the decode/transport layer directly:
// the unexported getLatestReleaseInfo must return (nil, error) on a bad body or
// a dead context.
func TestGetLatestReleaseInfo_Errors(t *testing.T) {
	tests := []struct {
		name string
		reg  func() *httpmock.Registry
		ctx  func() context.Context
	}{
		{
			name: "malformed JSON fails decode",
			reg: func() *httpmock.Registry {
				reg := &httpmock.Registry{}
				reg.Register(
					httpmock.REST(http.MethodGet, "/releases/latest"),
					httpmock.StringResponse("not json at all"),
				)
				return reg
			},
			ctx: context.Background,
		},
		{
			name: "cancelled context fails fetch",
			reg:  func() *httpmock.Registry { return releaseStub("v2.0.0", "https://example.com") },
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel() // Cancel immediately
				return ctx
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release, err := getLatestReleaseInfo(tt.ctx(), tt.reg().Client(), consts.GitHubRepo)
			if err == nil {
				t.Error("expected error, got nil")
			}
			if release != nil {
				t.Errorf("expected nil release, got %+v", release)
			}
		})
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
