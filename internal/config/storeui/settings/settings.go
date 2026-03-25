// Package settings provides the domain adapter for editing config.Settings via storeui.
package settings

import (
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
	return storeui.BuildLayerTargets(cfg.SettingsFileName(), config.ConfigDir(), store.Layers())
}

// Edit runs an interactive settings editor.
func Edit(ios *iostreams.IOStreams, store *storage.Store[config.Settings], cfg config.Config) (storeui.Result, error) {
	return storeui.Edit(ios, store,
		storeui.WithTitle("Settings Editor"),
		storeui.WithOverrides(Overrides()),
		storeui.WithLayerTargets(LayerTargets(store, cfg)),
	)
}
