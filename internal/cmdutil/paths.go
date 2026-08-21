package cmdutil

import (
	"errors"
	"fmt"
	"path/filepath"
)

// ResolveHostPath returns the real filesystem target of an expanded absolute
// host path. The path must exist.
func ResolveHostPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("resolve host path: path is required")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("resolve host path %q: path must be absolute", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve host path %q: evaluate path: %w", path, err)
	}
	return resolved, nil
}
