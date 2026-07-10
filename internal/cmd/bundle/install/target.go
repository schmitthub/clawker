package install

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// resolveTarget determines the clawker.yaml layer the declaration is written to
// and whether that layer is the user config-dir layer (which has no project
// root and so requires an absolute local path). The layer is selected by the
// mutually exclusive --user/--project/--local flags, defaulting to --user.
func resolveTarget(cfg config.Config, opts *InstallOptions) (string, bool, error) {
	set := 0
	for _, b := range []bool{opts.User, opts.Project, opts.Local} {
		if b {
			set++
		}
	}
	if set > 1 {
		return "", false, errors.New("--user, --project, and --local are mutually exclusive")
	}
	switch {
	case opts.Project:
		path, projErr := projectTarget(cfg, consts.ProjectConfigFile)
		return path, false, projErr
	case opts.Local:
		return localTarget(cfg)
	default:
		path, pathErr := consts.UserProjectConfigFilePath()
		if pathErr != nil {
			return "", false, fmt.Errorf("resolving user config path: %w", pathErr)
		}
		return path, true, nil
	}
}

// projectTarget resolves the project-layer write target for the given filename
// (the rediscoverable walk-up target under the project root), erroring when the
// command is not run inside a project.
func projectTarget(cfg config.Config, filename string) (string, error) {
	root := cfg.ProjectRoot()
	if root == "" {
		return "", errors.New("not inside a project — use --user (the default) or run inside a project")
	}
	targets, err := cfg.ProjectStore().WriteTargets()
	if err != nil {
		return "", fmt.Errorf("resolving project write target: %w", err)
	}
	for _, t := range targets {
		if t.Filename == filename && underRoot(t.Path, root) {
			return t.Path, nil
		}
	}
	return "", fmt.Errorf("could not resolve a %s write target under %s", filename, root)
}

// localTarget resolves the uncommitted project override layer
// (clawker.local.yaml): a discovered layer of that filename, else the path
// beside the project clawker.yaml target using the same placement convention.
func localTarget(cfg config.Config) (string, bool, error) {
	root := cfg.ProjectRoot()
	if root == "" {
		return "", false, errors.New("not inside a project — use --user (the default) or run inside a project")
	}
	targets, err := cfg.ProjectStore().WriteTargets()
	if err != nil {
		return "", false, fmt.Errorf("resolving local write target: %w", err)
	}
	for _, t := range targets {
		if t.Filename == consts.ProjectLocalConfigFile && underRoot(t.Path, root) {
			return t.Path, false, nil
		}
	}
	projPath, projErr := projectTarget(cfg, consts.ProjectConfigFile)
	if projErr != nil {
		return "", false, projErr
	}
	// Place the override beside the resolved project clawker.yaml in its own
	// placement form. storage owns the dual-placement dot-form convention;
	// consuming its helper keeps this write target from drifting out of sync
	// with discovery.
	return storage.SiblingTarget(projPath, consts.ProjectLocalConfigFile), false, nil
}

// underRoot reports whether path lies within root.
func underRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// planSource computes the bundles sequence for the target layer after adding
// src. It reads only the target layer's own declarations (never the
// union-merged view), so a project-layer install never rewrites the user
// layer's entries. When src is already declared in that layer the write is an
// idempotent no-op (alreadyDeclared=true) and updated is the unchanged
// sequence. The actual store mutation stays in the caller so the persistence
// path is a direct store write rather than an intermediary indirection.
func planSource(
	cfg config.Config,
	targetPath string,
	src config.BundleSource,
) ([]config.BundleSource, bool) {
	existing := layerBundles(cfg, targetPath)
	if slices.Contains(existing, src) {
		return existing, true
	}
	updated := make([]config.BundleSource, 0, len(existing)+1)
	updated = append(updated, existing...)
	updated = append(updated, src)
	return updated, false
}

// layerBundles returns the bundle sources declared in exactly the layer whose
// file is targetPath (its own entries, not the union across layers). An
// undiscovered target file contributes nothing — the write creates it.
func layerBundles(cfg config.Config, targetPath string) []config.BundleSource {
	var out []config.BundleSource
	for _, d := range cfg.BundleDeclarations() {
		if d.File == targetPath {
			out = append(out, d.Source)
		}
	}
	return out
}
