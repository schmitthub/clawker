// Package semver provides semantic versioning utilities with support for
// partial version matching (e.g., "2.1" matches "2.1.x").
package semver

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Version represents a parsed semantic version.
// Supports partial versions where Minor or Patch may be unspecified (-1).
type Version struct {
	Major      int    // Major version number (required)
	Minor      int    // Minor version number (-1 if unspecified)
	Patch      int    // Patch version number (-1 if unspecified)
	Prerelease string // Prerelease identifier (e.g., "alpha", "beta.1")
	Build      string // Build metadata (e.g., "build.123")
	Original   string // Original string representation
}

// semverRegex matches semantic versions with optional minor, patch, prerelease, and build.
// Supports: "1", "1.2", "1.2.3", "1.2.3-alpha", "1.2.3+build", "1.2.3-alpha+build"
var semverRegex = regexp.MustCompile(
	`^(?P<major>0|[1-9][0-9]*)` +
		`(?:\.(?P<minor>0|[1-9][0-9]*)` +
		`(?:\.(?P<patch>0|[1-9][0-9]*)` +
		`(?:-(?P<prerelease>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?` +
		`(?:\+(?P<build>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?)?)?$`,
)

// Parse parses a semver string into a Version struct.
// Supports partial versions: "2", "2.1", "2.1.3", "2.1.3-beta+build"
func Parse(s string) (*Version, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty version string")
	}

	match := semverRegex.FindStringSubmatch(s)
	if match == nil {
		return nil, fmt.Errorf("invalid semver: %q", s)
	}

	v := &Version{
		Original: s,
		Minor:    -1,
		Patch:    -1,
	}

	// Extract named groups
	for i, name := range semverRegex.SubexpNames() {
		if i == 0 || name == "" {
			continue
		}
		value := match[i]
		if value == "" {
			continue
		}

		switch name {
		case "major":
			n, _ := strconv.Atoi(value)
			v.Major = n
		case "minor":
			n, _ := strconv.Atoi(value)
			v.Minor = n
		case "patch":
			n, _ := strconv.Atoi(value)
			v.Patch = n
		case "prerelease":
			v.Prerelease = value
		case "build":
			v.Build = value
		}
	}

	return v, nil
}

// MustParse parses a semver string and panics on error.
// Use only for known-good version strings.
func MustParse(s string) *Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// IsValid checks if a string is a valid semantic version.
func IsValid(s string) bool {
	_, err := Parse(s)
	return err == nil
}

// HasMinor returns true if the minor version is specified.
func (v *Version) HasMinor() bool {
	return v.Minor >= 0
}

// HasPatch returns true if the patch version is specified.
func (v *Version) HasPatch() bool {
	return v.Patch >= 0
}

// HasPrerelease returns true if a prerelease identifier is present.
func (v *Version) HasPrerelease() bool {
	return v.Prerelease != ""
}

// String returns the canonical string representation of the version.
func (v *Version) String() string {
	if v.Original != "" {
		return v.Original
	}

	var sb strings.Builder
	sb.WriteString(strconv.Itoa(v.Major))

	if v.HasMinor() {
		sb.WriteByte('.')
		sb.WriteString(strconv.Itoa(v.Minor))

		if v.HasPatch() {
			sb.WriteByte('.')
			sb.WriteString(strconv.Itoa(v.Patch))
		}
	}

	if v.Prerelease != "" {
		sb.WriteByte('-')
		sb.WriteString(v.Prerelease)
	}

	if v.Build != "" {
		sb.WriteByte('+')
		sb.WriteString(v.Build)
	}

	return sb.String()
}

// Compare compares two versions.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
// Prereleases are considered lower than releases (1.0.0-alpha < 1.0.0).
func Compare(a, b *Version) int {
	// Compare major
	if a.Major < b.Major {
		return -1
	}
	if a.Major > b.Major {
		return 1
	}

	// Compare minor (treat -1 as 0 for comparison)
	aMinor, bMinor := a.Minor, b.Minor
	if aMinor < 0 {
		aMinor = 0
	}
	if bMinor < 0 {
		bMinor = 0
	}
	if aMinor < bMinor {
		return -1
	}
	if aMinor > bMinor {
		return 1
	}

	// Compare patch (treat -1 as 0 for comparison)
	aPatch, bPatch := a.Patch, b.Patch
	if aPatch < 0 {
		aPatch = 0
	}
	if bPatch < 0 {
		bPatch = 0
	}
	if aPatch < bPatch {
		return -1
	}
	if aPatch > bPatch {
		return 1
	}

	// Compare prerelease
	// No prerelease > prerelease (1.0.0 > 1.0.0-alpha)
	if a.Prerelease == "" && b.Prerelease != "" {
		return 1
	}
	if a.Prerelease != "" && b.Prerelease == "" {
		return -1
	}
	if a.Prerelease < b.Prerelease {
		return -1
	}
	if a.Prerelease > b.Prerelease {
		return 1
	}

	return 0
}

// Sort sorts a slice of versions in ascending order.
func Sort(versions []*Version) {
	sort.Slice(versions, func(i, j int) bool {
		return Compare(versions[i], versions[j]) < 0
	})
}

// SortDesc sorts a slice of versions in descending order.
func SortDesc(versions []*Version) {
	sort.Slice(versions, func(i, j int) bool {
		return Compare(versions[i], versions[j]) > 0
	})
}

// SortStrings sorts version strings in ascending order.
// Invalid versions are filtered out.
func SortStrings(versions []string) []string {
	var parsed []*Version
	for _, s := range versions {
		if v, err := Parse(s); err == nil {
			parsed = append(parsed, v)
		}
	}

	Sort(parsed)

	result := make([]string, len(parsed))
	for i, v := range parsed {
		result[i] = v.Original
	}
	return result
}

// SortStringsDesc sorts version strings in descending order.
// Invalid versions are filtered out.
func SortStringsDesc(versions []string) []string {
	var parsed []*Version
	for _, s := range versions {
		if v, err := Parse(s); err == nil {
			parsed = append(parsed, v)
		}
	}

	SortDesc(parsed)

	result := make([]string, len(parsed))
	for i, v := range parsed {
		result[i] = v.Original
	}
	return result
}

// Match finds the best matching version for a target pattern.
// The target can be a full version ("2.1.3") or partial ("2.1" or "2").
// Returns the highest version that matches the pattern.
// Prereleases are excluded unless the target is an exact match.
func Match(versions []string, target string) (string, bool) {
	// Parse target pattern
	t, err := Parse(target)
	if err != nil {
		return "", false
	}

	// Check for exact match first
	for _, v := range versions {
		if v == target {
			return v, true
		}
	}

	// Find all matching versions
	var matches []*Version
	for _, s := range versions {
		v, err := Parse(s)
		if err != nil {
			continue
		}

		// Must match major
		if v.Major != t.Major {
			continue
		}

		// If target specifies minor, must match
		if t.HasMinor() && v.Minor != t.Minor {
			continue
		}

		// If target specifies patch, must match
		if t.HasPatch() && v.Patch != t.Patch {
			continue
		}

		// Exclude prereleases (unless exact match handled above)
		if v.HasPrerelease() {
			continue
		}

		matches = append(matches, v)
	}

	if len(matches) == 0 {
		return "", false
	}

	// Sort and return highest
	SortDesc(matches)
	return matches[0].Original, true
}

// FilterValid returns only valid semver strings from the input slice.
func FilterValid(versions []string) []string {
	var result []string
	for _, s := range versions {
		if IsValid(s) {
			result = append(result, s)
		}
	}
	return result
}
