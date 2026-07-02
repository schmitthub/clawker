// Package settings provides the domain adapter for editing config.Settings via storeui.
package settings

import (
	"fmt"

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

// LayerTargets builds the per-field save destinations from the store's own
// write targets. The settings store has no walk-up discovery, so no CWD
// "Project" target is offered — a file saved there would never be read back.
func LayerTargets(store *storage.Store[config.Settings]) ([]storeui.LayerTarget, error) {
	targets, err := storeui.BuildLayerTargets(store)
	if err != nil {
		return nil, fmt.Errorf("building settings layer targets: %w", err)
	}
	return targets, nil
}

// Edit runs an interactive settings editor.
func Edit(ios *iostreams.IOStreams, store *storage.Store[config.Settings]) (storeui.Result, error) {
	targets, err := LayerTargets(store)
	if err != nil {
		return storeui.Result{}, err
	}
	return storeui.Edit(ios, store,
		storeui.WithTitle("Settings Editor"),
		storeui.WithOverrides(Overrides()),
		storeui.WithLayerTargets(targets),
	)
}
