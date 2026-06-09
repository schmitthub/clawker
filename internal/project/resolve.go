package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/consts"
)

// ErrNotInProject is returned when the current working directory is not within
// any registered project root. Callers branch on errors.Is(err, ErrNotInProject)
// to degrade gracefully (e.g. default to empty ignore patterns, disable walk-up).
var ErrNotInProject = errors.New("current directory is not within a configured project root")

// ResolveProjectRoot reads the project registry and returns the deepest
// registered project root that is an ancestor of cwd. cwd is expected to be
// cleaned by the caller. Callers pass the result to config.NewConfig to bound
// project-config walk-up.
//
// The registry is read through the storage layer (merge + migrate + lock) — the
// canonical path for clawker files, never a raw file read. A missing or empty
// registry yields no match. Returns ErrNotInProject when cwd is not within any
// registered project root; a storage failure reading the registry is returned
// wrapped, so it is not mistaken for "not in a project".
func ResolveProjectRoot(cwd string) (string, error) {
	store, err := newRegistryStore()
	if err != nil {
		return "", fmt.Errorf("project: loading registry: %w", err)
	}
	registry := store.Read()

	// Find the deepest registered root that is an ancestor of cwd.
	var bestMatch string
	for _, p := range registry.Projects {
		root := filepath.Clean(p.Root)
		if root == "" {
			continue
		}
		rel, relErr := filepath.Rel(root, cwd)
		if relErr != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			if len(root) > len(bestMatch) {
				bestMatch = root
			}
		}
	}

	if bestMatch == "" {
		return "", ErrNotInProject
	}
	return bestMatch, nil
}

// CurrentProjectRoot resolves the project root for the current working
// directory. Returns ErrNotInProject (via ResolveProjectRoot) when CWD is not
// within any registered project root; a storage failure reading the registry is
// returned wrapped, so callers can branch on errors.Is(err, ErrNotInProject)
// without masking real errors.
func CurrentProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("project: getting current working directory: %w", err)
	}
	return ResolveProjectRoot(filepath.Clean(cwd))
}

// CurrentProjectIgnoreFile returns the path to the .clawkerignore file at the
// current project root. It surfaces ErrNotInProject (via CurrentProjectRoot)
// when CWD is not within a registered project.
func CurrentProjectIgnoreFile() (string, error) {
	root, err := CurrentProjectRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, consts.IgnoreFile), nil
}
