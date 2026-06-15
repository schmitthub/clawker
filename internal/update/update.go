// Package update checks GitHub for newer clawker releases.
//
// Foundation-tier package: stdlib + net/http, no internal persistence imports
// (NOT internal/storage, NOT internal/state). It is a pure fetch+compare unit —
// the caller passes the current version and the timestamp of the last check,
// and persists the result itself (via internal/state). This keeps update free
// of any state-file ownership so the 24h update goroutine and the changelog
// cursor can share one storage-backed state without clobbering each other.
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

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/semver"
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
//   - currentVersion is "DEV" — development build
//   - lastCheckedAt is within CacheTTL (a check ran recently)
//
// lastCheckedAt is the timestamp of the last recorded check, supplied by the
// caller from persisted state; a zero value means "never checked".
func ShouldCheckForUpdate(lastCheckedAt time.Time, currentVersion string) bool {
	if os.Getenv(consts.EnvNoUpdateNotifier) != "" {
		return false
	}
	// "CI" is the canonical cross-tool CI-detection env var (not a clawker
	// const) — kept as a literal, matching every other tool's convention.
	if os.Getenv("CI") != "" {
		return false
	}
	if currentVersion == consts.DevVersion {
		return false
	}
	if !lastCheckedAt.IsZero() && time.Since(lastCheckedAt) < CacheTTL {
		return false
	}
	return true
}

// CheckForUpdate checks the GitHub API for the latest release of the given repo.
// It is pure: it performs no persistence. The caller passes lastCheckedAt from
// persisted state for freshness gating and persists the returned result itself.
//
// Returns (nil, nil) if checks are suppressed (see ShouldCheckForUpdate).
// Returns (nil, error) on API/network failures.
// Returns (*CheckResult, nil) otherwise — CheckResult.IsNewer reports whether a
// newer version is available; the fetched version/URL are always populated.
//
// The context controls the HTTP request lifetime — cancel it to abort cleanly.
// repo should be "owner/name", e.g. "schmitthub/clawker".
func CheckForUpdate(ctx context.Context, lastCheckedAt time.Time, currentVersion, repo string) (*CheckResult, error) {
	if !ShouldCheckForUpdate(lastCheckedAt, currentVersion) {
		return nil, nil
	}

	release, err := fetchLatestRelease(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("checking %s: %w", repo, err)
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

// fetchLatestRelease queries the GitHub API for the latest release.
func fetchLatestRelease(ctx context.Context, repo string) (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	return fetchLatestReleaseFromURL(ctx, url)
}

// fetchLatestReleaseFromURL fetches release info from the given URL.
// Separated from fetchLatestRelease to allow test URL injection.
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
// optional leading "v" (e.g. "v1.2.3" == "1.2.3"); an unparseable version is
// treated as not-newer. Delegates to the shared internal/semver comparator.
func IsNewer(latest, current string) bool {
	// Conservative: when either side is unparseable, ordering is undefined, so
	// do not claim an upgrade is available (avoids nagging non-release builds).
	return semver.IsValidLoose(latest) && semver.IsValidLoose(current) &&
		semver.CompareStrings(latest, current) > 0
}
