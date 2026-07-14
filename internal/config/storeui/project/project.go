// Package project provides the domain adapter for editing config.Project via storeui.
package project

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/storeui"
)

// Overrides returns field overrides for config.Project.
// Labels and descriptions come from schema struct tags (desc/label).
// Overrides here are limited to TUI-specific concerns: Kind and Options for constrained fields.
//
// Every field in the schema is editable — no fields are hidden.
// Maps (map[string]string) use the built-in KV editor; struct slices and the
// harnesses struct-map (per-harness init config, keyed by harness name) use
// the YAML textarea editor natively — no per-entry override is possible or
// needed for map keys the schema cannot enumerate.
func Overrides(cfg config.Config) []storeui.Override {
	return []storeui.Override{
		// Default harness — select from every harness the build can resolve
		// (embedded floor, loose convention dirs, installed bundles).
		//nolint:exhaustruct // overrides are sparse by design — an unset field means "no override"
		{
			Path: "build.harness",
			Kind: storeui.Ptr(storeui.KindSelect), Options: bundler.KnownHarnessNames(cfg),
		},
		// Workspace — select widget
		//nolint:exhaustruct // overrides are sparse by design — an unset field means "no override"
		{
			Path: "workspace.default_mode",
			Kind: storeui.Ptr(storeui.KindSelect), Options: []string{"bind", "snapshot"},
		},
	}
}

// LayerTargets builds the per-field save destinations from the store's own
// write targets. Inside a project the walk-up store offers a "Project"
// target; outside a project (no walk-up anchor) it does not. Discovered
// local override files (the clawker.local.yaml filename, any placement) are
// relabeled "Local" — filename naming is domain knowledge storeui does not
// hold.
func LayerTargets(store *storage.Store[config.Project]) ([]storeui.LayerTarget, error) {
	targets, err := storeui.BuildLayerTargets(store)
	if err != nil {
		return nil, fmt.Errorf("building project layer targets: %w", err)
	}
	for i, tgt := range targets {
		if tgt.Filename == consts.ProjectLocalConfigFile && tgt.Label != storeui.LabelProject {
			targets[i].Label = storeui.LabelLocal
		}
	}
	return targets, nil
}

// Edit runs an interactive project config editor. cfg supplies the harness
// enumeration for the build.harness select — pass the Config the store came
// from.
func Edit(ios *iostreams.IOStreams, cfg config.Config, store *storage.Store[config.Project]) (storeui.Result, error) {
	targets, err := LayerTargets(store)
	if err != nil {
		return storeui.Result{}, err
	}
	return storeui.Edit(ios, store,
		storeui.WithTitle("Project Configuration Editor"),
		storeui.WithOverrides(Overrides(cfg)),
		storeui.WithLayerTargets(targets),
	)
}
