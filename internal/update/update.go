// Package update checks GitHub for newer clawker releases.
//
// CheckForUpdate reads the last-checked timestamp from the CLI state facade
// (internal/state) for freshness gating and persists the check result there
// (RecordUpdateCheck — a field merge that never touches the changelog cursor,
// so it cannot clobber a concurrent cursor write). The version-comparison core
// (checkForUpdate / ShouldCheckForUpdate) stays pure — it takes a plain
// timestamp and does no I/O.
//
// Designed for background use — the caller launches CheckForUpdate in a
// goroutine with a cancellable context. Context cancellation cleanly aborts the
// HTTP request when the CLI command finishes before the check completes.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/state"
)

// CacheTTL is how long a recorded check result is considered fresh. The caller
// compares it against the persisted last-checked timestamp.
const CacheTTL = 24 * time.Hour

// httpTimeout is the maximum time for the GitHub API request.
const httpTimeout = 5 * time.Second

// CheckResult is the outcome of an update check. It always carries the fetched
// latest version and release URL so the caller can persist them, regardless of
// whether an upgrade is available. IsNewer reports whether LatestVersion is a
// newer semver than CurrentVersion (the only case worth notifying the user).
type CheckResult struct {
	CurrentVersion string
	LatestVersion  string
	ReleaseURL     string
	IsNewer        bool
}

// githubRelease is a partial response from the GitHub releases API.
type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// ShouldCheckForUpdate returns false if update checks should be suppressed.
// Suppression conditions:
//   - consts.EnvNoUpdateNotifier env var is set (non-empty)
//   - CI env var is set (non-empty) — standard CI detection
//   - lastCheckedAt is within CacheTTL (a check ran recently)
//
// A non-release build whose version does not parse as semver naturally produces
// no upgrade notification (IsNewer returns false), so no explicit dev-build gate
// is needed — opting out is the env var's job.
//
// lastCheckedAt is the timestamp of the last recorded check, supplied by the
// caller from persisted state; a zero value means "never checked".
func ShouldCheckForUpdate(lastCheckedAt time.Time) bool {
	if os.Getenv(consts.EnvNoUpdateNotifier) != "" {
		return false
	}
	// "CI" is the canonical cross-tool CI-detection env var (not a clawker
	// const) — kept as a literal, matching every other tool's convention.
	if os.Getenv("CI") != "" {
		return false
	}
	// A future lastCheckedAt (clock skew at write time, later corrected) must
	// not count as fresh: time.Since would be negative and spuriously satisfy
	// the < CacheTTL gate, suppressing checks until wall-clock catches up. The
	// elapsed >= 0 guard drops future timestamps through to a fresh check.
	if elapsed := time.Since(lastCheckedAt); !lastCheckedAt.IsZero() && elapsed >= 0 && elapsed < CacheTTL {
		return false
	}
	return true
}

// CheckForUpdate checks the GitHub API for the latest release of the given repo
// and persists the result to CLI state. It reads the last-checked timestamp from
// st for freshness gating and, on a successful check, records checked_at +
// versions via st.RecordUpdateCheck (a field merge that never touches the
// changelog cursor). A nil st disables both the gate read and persistence (the
// check just proceeds with a zero "never checked" time).
//
// Returns (nil, nil) if checks are suppressed (see ShouldCheckForUpdate).
// Returns (nil, error) on API/network failures.
// Returns (*CheckResult, nil) otherwise — CheckResult.IsNewer reports whether a
// newer version is available; the fetched version/URL are always populated. A
// best-effort persistence failure is returned (with the result) for the caller
// to log.
//
// The context controls the HTTP request lifetime — cancel it to abort cleanly.
// repo should be "owner/name", e.g. "schmitthub/clawker".
func CheckForUpdate(ctx context.Context, st *state.State, currentVersion, repo string) (*CheckResult, error) {
	var lastCheckedAt time.Time
	if st != nil {
		lastCheckedAt = st.LastCheckedAt()
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	result, err := checkForUpdate(ctx, lastCheckedAt, currentVersion, url)
	if err != nil {
		return nil, fmt.Errorf("checking %s: %w", repo, err)
	}
	if result != nil && st != nil {
		if err := st.RecordUpdateCheck(time.Now(), result.LatestVersion); err != nil {
			return result, fmt.Errorf("recording update check: %w", err)
		}
	}
	return result, nil
}

// checkForUpdate is the URL-parameterized core of CheckForUpdate: the exported
// wrapper builds the GitHub API URL from repo and delegates here. Keeping the
// core unexported lets tests exercise the real gate→fetch→assemble path against
// an httptest URL without a parallel reimplementation and without putting a URL
// seam on the exported signature.
func checkForUpdate(ctx context.Context, lastCheckedAt time.Time, currentVersion, url string) (*CheckResult, error) {
	if !ShouldCheckForUpdate(lastCheckedAt) {
		return nil, nil
	}

	release, err := fetchLatestReleaseFromURL(ctx, url)
	if err != nil {
		return nil, err
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	currentBare := strings.TrimPrefix(currentVersion, "v")

	return &CheckResult{
		CurrentVersion: currentBare,
		LatestVersion:  latestVersion,
		ReleaseURL:     release.HTMLURL,
		IsNewer:        IsNewer(latestVersion, currentBare),
	}, nil
}

// fetchLatestReleaseFromURL fetches and decodes a GitHub release from the given
// URL. The URL is supplied by checkForUpdate (built from the repo), which keeps
// this function URL-agnostic so tests can point it at an httptest server.
func fetchLatestReleaseFromURL(ctx context.Context, url string) (*githubRelease, error) {
	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	if release.TagName == "" {
		return nil, fmt.Errorf("empty tag_name in response")
	}

	return &release, nil
}

// IsNewer reports whether latest is a newer version than current. Both accept an
// optional leading "v" (Masterminds NewVersion tolerates it). Conservative: when
// either side is unparseable, ordering is undefined, so do not claim an upgrade
// is available — this is what keeps non-release builds (whose version may not
// parse) from nagging, with no explicit dev-build branch.
func IsNewer(latest, current string) bool {
	lv, err := semver.NewVersion(latest)
	if err != nil {
		return false
	}
	cv, err := semver.NewVersion(current)
	if err != nil {
		return false
	}
	return lv.Compare(cv) > 0
}
