// Package update checks GitHub for newer clawker releases and caches results.
//
// Foundation-tier package: stdlib + net/http + yaml.v3, no internal imports.
// The caller passes the current version string (no dependency on internal/build).
//
// Designed for background use — the caller launches CheckForUpdate in a goroutine
// with a cancellable context. Context cancellation cleanly aborts the HTTP request
// when the CLI command finishes before the check completes.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// cacheTTL is how long a cached check result is considered fresh.
const cacheTTL = 24 * time.Hour

// httpTimeout is the maximum time for the GitHub API request.
const httpTimeout = 5 * time.Second

// StateEntry is the cached update check result, persisted as YAML.
type StateEntry struct {
	CheckedAt      time.Time `yaml:"checked_at"`
	LatestVersion  string    `yaml:"latest_version"`
	LatestURL      string    `yaml:"latest_url"`
	CurrentVersion string    `yaml:"current_version"`
}

// CheckResult is returned when a newer version is available.
type CheckResult struct {
	CurrentVersion string
	LatestVersion  string
	ReleaseURL     string
}

// githubRelease is a partial response from the GitHub releases API.
type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// ShouldCheckForUpdate returns false if update checks should be suppressed.
// Suppression conditions:
//   - CLAWKER_NO_UPDATE_NOTIFIER env var is set (non-empty)
//   - CI env var is set (non-empty) — standard CI detection
//   - currentVersion is "DEV" — development build
//   - stateFilePath is non-empty and cache is fresh (checked < 24h ago)
func ShouldCheckForUpdate(stateFilePath, currentVersion string) bool {
	if os.Getenv("CLAWKER_NO_UPDATE_NOTIFIER") != "" {
		return false
	}
	if os.Getenv("CI") != "" {
		return false
	}
	if currentVersion == "DEV" {
		return false
	}
	if stateFilePath != "" {
		if entry, err := readState(stateFilePath); err == nil {
			if time.Since(entry.CheckedAt) < cacheTTL {
				return false
			}
		}
	}
	return true
}

// CheckForUpdate checks the GitHub API for a newer release of the given repo.
// Returns (nil, nil) if the current version is latest or checks are suppressed.
// Returns (nil, error) on API/network failures.
// Returns (*CheckResult, nil) when a newer version is available.
//
// The context controls the HTTP request lifetime — cancel it to abort cleanly.
// repo should be "owner/name", e.g. "schmitthub/clawker".
func CheckForUpdate(ctx context.Context, stateFilePath, currentVersion, repo string) (*CheckResult, error) {
	if !ShouldCheckForUpdate(stateFilePath, currentVersion) {
		return nil, nil
	}

	release, err := fetchLatestRelease(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("checking %s: %w", repo, err)
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	currentBare := strings.TrimPrefix(currentVersion, "v")

	// Write cache regardless of comparison result
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

// isNewer returns true if latest is a newer semver than current.
// Both should be bare versions without "v" prefix (e.g. "1.2.3").
func isNewer(latest, current string) bool {
	latestParts := parseSemver(latest)
	currentParts := parseSemver(current)

	if latestParts == nil || currentParts == nil {
		// Can't determine ordering — don't claim newer
		return false
	}

	for i := range 3 {
		if latestParts[i] > currentParts[i] {
			return true
		}
		if latestParts[i] < currentParts[i] {
			return false
		}
	}
	return false
}

// parseSemver parses "MAJOR.MINOR.PATCH" into a 3-element []int.
// Returns nil if parsing fails.
func parseSemver(v string) []int {
	// Strip any pre-release suffix (e.g. "1.2.3-beta.1" → "1.2.3")
	if idx := strings.IndexByte(v, '-'); idx != -1 {
		v = v[:idx]
	}

	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil
	}

	result := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		result[i] = n
	}
	return result
}

// readState reads the cached state file.
func readState(path string) (*StateEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entry StateEntry
	if err := yaml.Unmarshal(data, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

// writeState atomically writes the state file (write to temp, rename).
func writeState(path string, entry StateEntry) error {
	data, err := yaml.Marshal(entry)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
