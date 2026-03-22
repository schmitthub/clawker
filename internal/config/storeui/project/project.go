// Package project provides the domain adapter for editing config.Project via storeui.
package project

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/storeui"
)

// Overrides returns field overrides for config.Project.
// Labels and descriptions come from schema struct tags (desc/label).
// Overrides here are limited to TUI-specific concerns: Hidden, Kind, Options, ReadOnly.
func Overrides() []storeui.Override {
	return []storeui.Override{
		// Build — complex types hidden (prefix-based: hides all children)
		{Path: "build.build_args", Hidden: true},
		{Path: "build.instructions", Hidden: true},
		{Path: "build.inject", Hidden: true},

		// Agent — complex types hidden
		{Path: "agent.env", Hidden: true},
		{Path: "agent.claude_code", Hidden: true},

		// Workspace — select widget
		{Path: "workspace.default_mode",
			Kind: storeui.Ptr(storeui.KindSelect), Options: []string{"bind", "snapshot"}},

		// Security — complex types hidden
		{Path: "security.firewall.rules", Hidden: true},
		{Path: "security.firewall.ip_range_sources", Hidden: true},
		{Path: "security.cap_add", Hidden: true},
	}
}

// LayerTargets builds the per-field save destinations for project config.
// Targets: Local (CWD dot-file), User (config dir), plus Original if provenance exists.
// Paths and filenames come from config accessors, never hardcoded.
func LayerTargets(store *storage.Store[config.Project], cfg config.Config) []storeui.LayerTarget {
	filename := cfg.ProjectConfigFileName()

	var targets []storeui.LayerTarget
	seen := make(map[string]bool)

	// Local: CWD dot-file (skipped if CWD is unavailable).
	cwd, cwdErr := os.Getwd()
	if cwdErr == nil {
		localPath := storeui.ResolveLocalPath(cwd, filename)
		targets = append(targets, storeui.LayerTarget{
			Label:       "Local",
			Description: storeui.ShortenHome(localPath),
			Path:        localPath,
		})
		seen[localPath] = true
	}

	// User: config dir file.
	userPath := filepath.Join(config.ConfigDir(), filename)
	if !seen[userPath] {
		targets = append(targets, storeui.LayerTarget{
			Label:       "User",
			Description: storeui.ShortenHome(userPath),
			Path:        userPath,
		})
		seen[userPath] = true
	}

	// Original: add any discovered layers not already in the list.
	for _, l := range store.Layers() {
		if !seen[l.Path] {
			targets = append(targets, storeui.LayerTarget{
				Label:       layerLabel(l, config.ConfigDir(), cwd),
				Description: storeui.ShortenHome(l.Path),
				Path:        l.Path,
			})
			seen[l.Path] = true
		}
	}

	return targets
}

// layerLabel produces a human-readable label for a layer based on its path.
func layerLabel(l storage.LayerInfo, configDir, cwd string) string {
	dir := filepath.Dir(l.Path)

	switch {
	case dir == configDir || strings.HasPrefix(dir, configDir+string(os.PathSeparator)):
		return "User"
	case cwd != "" && dir == cwd:
		return "Local"
	case cwd != "" && strings.HasPrefix(dir, cwd+string(os.PathSeparator)):
		rel, _ := filepath.Rel(cwd, l.Path)
		return "Local (" + rel + ")"
	default:
		return "Project"
	}
}

// Edit runs an interactive project config editor.
func Edit(ios *iostreams.IOStreams, store *storage.Store[config.Project], cfg config.Config) (storeui.Result, error) {
	return storeui.Edit(ios, store,
		storeui.WithTitle("Project Configuration Editor"),
		storeui.WithOverrides(Overrides()),
		storeui.WithLayerTargets(LayerTargets(store, cfg)),
	)
}
