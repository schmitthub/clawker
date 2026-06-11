package storage

import (
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/consts"
)

// Directory resolution delegates to internal/consts — the single XDG
// resolver (CLAWKER_*_DIR > XDG_*_HOME > platform default). consts is
// foundational (stdlib-only imports), so depending on it does not
// compromise storage's leaf position in the dependency graph.

// configDir returns the clawker config directory.
func configDir() string { return consts.ConfigDir() }

// dataDir returns the clawker data directory.
func dataDir() string { return consts.DataDir() }

// stateDir returns the clawker state directory.
func stateDir() string { return consts.StateDir() }

// cacheDir returns the clawker cache directory.
func cacheDir() string { return consts.CacheDir() }

// dirEntry pairs a resolved path with its human-readable category name.
type dirEntry struct {
	name string
	path string
}

// ValidateDirectories resolves all four XDG-style directories and returns an
// error if any two resolve to the same path. This catches misconfiguration
// (e.g. CLAWKER_DATA_DIR accidentally pointing at the config directory).
func ValidateDirectories() error {
	dirs := []dirEntry{
		{"config", configDir()},
		{"data", dataDir()},
		{"state", stateDir()},
		{"cache", cacheDir()},
	}

	seen := make(map[string]string, len(dirs)) // path → name
	var collisions []string

	for _, d := range dirs {
		if prev, ok := seen[d.path]; ok {
			collisions = append(collisions, fmt.Sprintf(
				"%s and %s both resolve to %s", prev, d.name, d.path,
			))
		}
		seen[d.path] = d.name
	}

	if len(collisions) > 0 {
		return fmt.Errorf("directory collision detected: %s — check CLAWKER_*_DIR and XDG_*_HOME env vars",
			strings.Join(collisions, "; "))
	}
	return nil
}
