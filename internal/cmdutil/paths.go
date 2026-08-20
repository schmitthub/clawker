package cmdutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveHostPath expands host process environment variables and a leading
// tilde, and then returns the absolute real path. The path must exist.
func ResolveHostPath(expression string) (string, error) {
	if expression == "" {
		return "", errors.New("resolve host path: expression is required")
	}
	expanded := os.ExpandEnv(expression)
	if expanded == "~" || strings.HasPrefix(expanded, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve host path %q: get user home: %w", expression, err)
		}
		expanded = filepath.Join(home, strings.TrimPrefix(expanded, "~/"))
	}
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve host path %q: make %q absolute: %w", expression, expanded, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve host path %q: evaluate %q: %w", expression, absolute, err)
	}
	return resolved, nil
}
