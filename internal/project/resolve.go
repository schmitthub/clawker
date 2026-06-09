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

// resolveRootPath normalizes a path for project-root identity comparison:
// absolute, then symlink-resolved (e.g. macOS /var → /private/var) — the same
// resolution the registry facade applies when answering "is this root mine?".
// EvalSymlinks fails on paths that do not exist (a registered root may have
// been deleted from disk); fall back to the cleaned absolute path deliberately
// so stale entries still compare by their recorded form.
func resolveRootPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}
	return resolved
}

// ResolveProjectRoot reads the project registry and returns the deepest
// registered project root that is an ancestor of cwd. cwd is cleaned and
// symlink-normalized here, so callers may pass it as-is. Callers pass the
// result to config.NewConfig to bound project-config walk-up.
//
// The registry is read through the storage layer (merge + lock) — the
// canonical path for clawker files, never a raw file read. A missing or empty
// registry yields no match. cwd and each registered root are compared via
// resolveRootPath so a root registered through a symlink matches its real
// path (and vice versa). The returned root is always expressed in cwd's own
// path form — a string-ancestor of the cwd the caller navigates from — so it
// stays a valid walk-up anchor even when os.Getwd reports a logical, symlinked
// path (e.g. macOS /tmp). Returns ErrNotInProject when cwd is not within any
// registered project root, including when a depth-changing symlink leaves the
// logical cwd with no project ancestor in its own path form; a storage failure
// reading the registry is returned wrapped, so it is not mistaken for "not in
// a project".
func ResolveProjectRoot(cwd string) (string, error) {
	cwd = filepath.Clean(cwd)

	store, err := newRegistryStore()
	if err != nil {
		return "", fmt.Errorf("project: loading registry: %w", err)
	}
	registry := store.Read()

	// Find the deepest registered root that is an ancestor of cwd, comparing
	// in symlink-resolved space.
	resolvedCwd := resolveRootPath(cwd)
	var bestMatch, bestRel string
	found := false
	for _, p := range registry.Projects {
		// Skip blank roots: resolveRootPath would anchor them at the process
		// working directory via filepath.Abs and spuriously match everything.
		if strings.TrimSpace(p.Root) == "" {
			continue
		}
		root := resolveRootPath(p.Root)
		rel, relErr := filepath.Rel(root, resolvedCwd)
		if relErr != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			if !found || len(root) > len(bestMatch) {
				bestMatch, bestRel, found = root, rel, true
			}
		}
	}

	if !found {
		return "", ErrNotInProject
	}
	if bestRel == "." {
		return cwd, nil
	}

	// Map the resolved-space match back into cwd's own path form by stripping
	// rel's components, so the returned anchor is an ancestor of the cwd the
	// caller navigates from.
	derived := cwd
	for range strings.Split(bestRel, string(filepath.Separator)) {
		derived = filepath.Dir(derived)
	}
	// An intra-tree directory symlink can change component depth between the
	// logical and resolved forms; verify the derived anchor still resolves to
	// the matched root. When it does not, the contract — a root expressed in
	// cwd's own path form that is a string-ancestor of cwd — cannot be met:
	// returning the resolved-space match would hand callers an anchor that is
	// not an ancestor of their logical cwd and breaks config walk-up. Through
	// a depth-changing symlink the logical cwd's ancestry genuinely does not
	// contain the project tree, so "not in a project" is the honest answer
	// (matching what plain string comparison of the logical paths would say).
	if resolveRootPath(derived) == bestMatch {
		return derived, nil
	}
	return "", ErrNotInProject
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
	return ResolveProjectRoot(cwd)
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
