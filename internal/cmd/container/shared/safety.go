package shared

import (
	"os"
	"path/filepath"
	"strings"
)

// IsOutsideHome reports whether dir is $HOME itself or not within $HOME.
// Returns false if $HOME cannot be determined or paths cannot be resolved.
func IsOutsideHome(dir string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}

	// Resolve symlinks for consistent comparison (macOS /var → /private/var)
	absDir, err = filepath.EvalSymlinks(absDir)
	if err != nil {
		return false
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return false
	}

	// Relative path from home to dir:
	//   "."           → dir IS home
	//   "Code/foo"    → dir is inside home (safe)
	//   ".."          → dir is parent of home
	//   "../../tmp"   → dir is outside home
	rel, err := filepath.Rel(home, absDir)
	if err != nil {
		return false
	}
	return rel == "." || strings.HasPrefix(rel, "..")
}
