// Package project provides the domain adapter for editing config.Project via storeui.
package project

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/storeui"
)

// Overrides returns field overrides for config.Project.
// Labels and descriptions come from schema struct tags (desc/label).
// Overrides here are limited to TUI-specific concerns: Kind and Options for constrained fields.
//
// Every field in the schema is editable — no fields are hidden.
// Maps (map[string]string) use the built-in KV editor; struct slices use the YAML textarea editor.
func Overrides() []storeui.Override {
	return []storeui.Override{
		// Workspace — select widget
		{Path: "workspace.default_mode",
			Kind: storeui.Ptr(storeui.KindSelect), Options: []string{"bind", "snapshot"}},

		// Agent — Claude Code config strategy select
		{Path: "agent.claude_code.config.strategy",
			Kind: storeui.Ptr(storeui.KindSelect), Options: []string{"copy", "fresh"}},
	}
}

// LayerTargets builds the per-field save destinations from the store's own
// write targets. Inside a project the walk-up store offers a CWD "Local"
// target; outside a project (no walk-up anchor) it does not.
func LayerTargets(store *storage.Store[config.Project]) ([]storeui.LayerTarget, error) {
	targets, err := storeui.BuildLayerTargets(store)
	if err != nil {
		return nil, fmt.Errorf("building project layer targets: %w", err)
	}
	return targets, nil
}

// Edit runs an interactive project config editor.
func Edit(ios *iostreams.IOStreams, store *storage.Store[config.Project]) (storeui.Result, error) {
	targets, err := LayerTargets(store)
	if err != nil {
		return storeui.Result{}, err
	}
	return storeui.Edit(ios, store,
		storeui.WithTitle("Project Configuration Editor"),
		storeui.WithOverrides(Overrides()),
		storeui.WithLayerTargets(targets),
	)
}
