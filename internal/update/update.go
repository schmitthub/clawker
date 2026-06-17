// Package update checks GitHub for newer clawker releases.
//
// CheckForUpdate reads the last-checked timestamp from the CLI state facade
// (internal/state) for freshness gating and persists the check there on every
// successful fetch (RecordUpdateCheck — a field merge that never touches the
// changelog cursor, so it cannot clobber a concurrent cursor write). A non-nil
// *ReleaseInfo result means a strictly newer release exists; nil means
// up-to-date, TTL-fresh, or not newer. Env/CI opt-out suppression is the
// caller's responsibility — only the TTL freshness gate lives here.
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
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/schmitthub/clawker/internal/state"
)

// cacheTTL is how long a recorded check result is considered fresh. It is
// compared against the persisted last-checked timestamp inside shouldCheckForUpdate.
const cacheTTL = 24 * time.Hour

// ReleaseInfo describes a strictly newer release than the running version. A
// non-nil *ReleaseInfo MEANS "a newer release exists" — CheckForUpdate only
// returns one when LatestVersion is a newer semver than CurrentVersion, so the
// caller never has to re-check a flag.
type ReleaseInfo struct {
	CurrentVersion string
	LatestVersion  string
	ReleaseURL     string
}

// githubRelease is a partial response from the GitHub releases API.
type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// shouldCheckForUpdate is the TTL freshness gate: it returns false only when a
// check ran recently (within cacheTTL). Env/CI opt-out suppression is NOT handled
// here — that is the caller's responsibility (see CheckForUpdate's doc).
//
// A non-release build whose version does not parse as semver is rejected up front
// by CheckForUpdate (semver.NewVersion(currentVersion) fails before any fetch), so
// no explicit dev-build gate is needed here.
//
// lastCheckedAt is the timestamp of the last recorded check, supplied by the
// caller from persisted state; a zero value means "never checked".
func shouldCheckForUpdate(lastCheckedAt time.Time) bool {
	// A future lastCheckedAt (clock skew at write time, later corrected) must
	// not count as fresh: time.Since would be negative and spuriously satisfy
	// the < cacheTTL gate, suppressing checks until wall-clock catches up. The
	// elapsed >= 0 guard drops future timestamps through to a fresh check.
	if elapsed := time.Since(lastCheckedAt); !lastCheckedAt.IsZero() && elapsed >= 0 && elapsed < cacheTTL {
		return false
	}
	return true
}

// CheckForUpdate checks the GitHub API for the latest release of the given repo,
// persists the check to CLI state, and reports a strictly newer release.
//
// Return contract:
//   - (nil, nil)         — up-to-date, TTL-fresh, or the latest release is not
//     newer than currentVersion. A non-nil result MEANS a newer release exists.
//   - (*ReleaseInfo, nil) — a strictly newer release is available.
//   - (nil, error)       — the fetch failed (API/network/decode).
//
// Persistence is keyed on FETCH SUCCESS, not on whether a newer release was
// found: on every successful fetch (newer or not) it records checked_at +
// latest_version via st.RecordUpdateCheck BEFORE the newer/not-newer decision.
// This is what lets the TTL gate throttle — if persistence only happened on a
// newer release, checked_at would never advance on the common not-newer path and
// the GitHub API would be hit on every run. A nil st is a programming error (the
// caller wires state via the factory) and returns an error before any fetch.
//
// client supplies the transport and timeout (the caller's Factory HttpClient in
// production; an internal/httpmock stub in tests).
//
// Opt-out suppression (e.g. an env-var kill switch or CI detection) is the
// CALLER's responsibility — shouldCheckForUpdate only applies the TTL freshness
// gate. Defense-in-depth note: a future second caller that bypasses the cmd.go
// opt-out gate would still reach the GitHub API here; the opt-out is not enforced
// inside this package.
//
// The context controls the HTTP request lifetime — cancel it to abort cleanly.
// repo should be "owner/name", e.g. "schmitthub/clawker".
func CheckForUpdate(ctx context.Context, client *http.Client, st state.StateStore, currentVersion, repo string) (*ReleaseInfo, error) {

	if st == nil {
		// A nil state store is a programming error (the caller wires it via the
		// factory); without it there is no TTL gate and no persistence.
		return nil, fmt.Errorf("state: CheckForUpdate: nil StateStore")
	}

	cv, err := semver.NewVersion(currentVersion)
	if err != nil {
		return nil, fmt.Errorf("parsing current version %q: %w", currentVersion, err)
	}

	if !shouldCheckForUpdate(st.State().CheckedAt) {
		return nil, nil
	}

	release, err := getLatestReleaseInfo(ctx, client, repo)
	if err != nil {
		return nil, fmt.Errorf("checking %s: %w", repo, err)
	}

	lv, err := semver.NewVersion(release.TagName)
	if err != nil {
		return nil, fmt.Errorf("parsing release tag %q from %s: %w", release.TagName, repo, err)
	}

	// Persist on fetch success, BEFORE the newer/not-newer decision, so the TTL
	// gate throttles regardless of outcome.
	if err := st.RecordUpdateCheck(time.Now(), lv.String()); err != nil {
		return nil, fmt.Errorf("recording update check: %w", err)
	}

	if !cv.LessThan(lv) {
		return nil, nil
	}

	return &ReleaseInfo{
		CurrentVersion: cv.String(),
		LatestVersion:  lv.String(),
		ReleaseURL:     release.HTMLURL,
	}, nil
}

// getLatestReleaseInfo GETs and decodes the latest GitHub release for repo using
// the supplied client. The GitHub API URL is built from repo here; there is no
// URL seam in the signature — tests inject a client whose transport is an
// internal/httpmock stub, so this reaches no live network.
func getLatestReleaseInfo(ctx context.Context, client *http.Client, repo string) (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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

	return &release, nil
}
