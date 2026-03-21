// Package settings provides the domain adapter for editing config.Settings via storeui.
package settings

import (
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/storeui"
)

// Overrides returns field overrides for config.Settings.
// Labels and descriptions come from schema struct tags (desc/label).
// Overrides here are limited to TUI-specific concerns: ReadOnly.
func Overrides() []storeui.Override {
	return []storeui.Override{
		// Host proxy — internals are read-only
		{Path: "host_proxy.manager.port", ReadOnly: storeui.Ptr(true)},
		{Path: "host_proxy.daemon.port", ReadOnly: storeui.Ptr(true)},
		{Path: "host_proxy.daemon.poll_interval", ReadOnly: storeui.Ptr(true)},
		{Path: "host_proxy.daemon.grace_period", ReadOnly: storeui.Ptr(true)},
		{Path: "host_proxy.daemon.max_consecutive_errs", ReadOnly: storeui.Ptr(true)},
	}
}

// LayerTargets builds the per-field save destinations for settings.
func LayerTargets(store *storage.Store[config.Settings], cfg config.Config) []storeui.LayerTarget {
	filename := cfg.SettingsFileName()

	var targets []storeui.LayerTarget
	seen := make(map[string]bool)

	// Local: CWD dot-file (skipped if CWD is unavailable).
	if cwd, err := os.Getwd(); err == nil {
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
				Label:       "Settings",
				Description: storeui.ShortenHome(l.Path),
				Path:        l.Path,
			})
			seen[l.Path] = true
		}
	}

	return targets
}

// Edit runs an interactive settings editor.
func Edit(ios *iostreams.IOStreams, store *storage.Store[config.Settings], cfg config.Config) (storeui.Result, error) {
	return storeui.Edit(ios, store,
		storeui.WithTitle("Settings Editor"),
		storeui.WithOverrides(Overrides()),
		storeui.WithLayerTargets(LayerTargets(store, cfg)),
	)
}
