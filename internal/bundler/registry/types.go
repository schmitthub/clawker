// Package registry provides clients for fetching package version information
// from npm and other registries.
package registry

import (
	"sort"

	"github.com/Masterminds/semver/v3"
)

// DistTags maps dist-tag names to version strings.
// Common tags: latest, stable, next, beta, alpha
type DistTags map[string]string

// VersionInfo contains metadata for a specific version.
type VersionInfo struct {
	FullVersion   string              `json:"fullVersion"`
	Major         int                 `json:"major"`
	Minor         int                 `json:"minor"`
	Patch         int                 `json:"patch"`
	Prerelease    string              `json:"prerelease,omitempty"`
	DebianDefault string              `json:"debianDefault"`
	AlpineDefault string              `json:"alpineDefault"`
	Variants      map[string][]string `json:"variants"`
}

// NewVersionInfo creates a VersionInfo from a parsed semver.Version.
// A Masterminds *Version is always complete — NewVersion coerces a partial like
// "2.1" to "2.1.0", so Minor/Patch are unconditionally set (no HasMinor/HasPatch
// guards). Major/Minor/Patch are uint64; the JSON contract uses int.
func NewVersionInfo(v *semver.Version, debianDefault, alpineDefault string, variants map[string][]string) *VersionInfo {
	return &VersionInfo{
		FullVersion:   v.Original(),
		Major:         int(v.Major()),
		Minor:         int(v.Minor()),
		Patch:         int(v.Patch()),
		Prerelease:    v.Prerelease(),
		DebianDefault: debianDefault,
		AlpineDefault: alpineDefault,
		Variants:      variants,
	}
}

// VersionsFile represents the structure of versions.json.
// Keys are full version strings (e.g., "2.1.2").
type VersionsFile map[string]*VersionInfo

// Keys returns all version keys in the file.
func (v VersionsFile) Keys() []string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	return keys
}

// SortedKeys returns version keys sorted by semver (descending). Unparseable
// keys are dropped (keys are resolved full versions, so this is defensive).
func (v VersionsFile) SortedKeys() []string {
	parsed := make(semver.Collection, 0, len(v))
	for k := range v {
		if ver, err := semver.NewVersion(k); err == nil {
			parsed = append(parsed, ver)
		}
	}
	sort.Sort(sort.Reverse(parsed))

	keys := make([]string, len(parsed))
	for i, ver := range parsed {
		keys[i] = ver.Original()
	}
	return keys
}

// MarshalJSON implements json.Marshaler to output versions in sorted order.

// NPMPackageInfo represents the npm registry response for a package.
type NPMPackageInfo struct {
	Name     string              `json:"name"`
	DistTags DistTags            `json:"dist-tags"`
	Versions map[string]struct{} `json:"versions"`
}
