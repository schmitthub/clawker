// Package project provides the domain adapter for editing config.Project via storeui.
package project

import (
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
// Maps and struct slices use the default YAML textarea editor.
// Domain adapters can wire custom Editor factories for polished UX in the future.
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

// LayerTargets builds the per-field save destinations for project config.
func LayerTargets(store *storage.Store[config.Project], cfg config.Config) []storeui.LayerTarget {
	return storeui.BuildLayerTargets(cfg.ProjectConfigFileName(), config.ConfigDir(), store.Layers())
}

// Edit runs an interactive project config editor.
func Edit(ios *iostreams.IOStreams, store *storage.Store[config.Project], cfg config.Config) (storeui.Result, error) {
	return storeui.Edit(ios, store,
		storeui.WithTitle("Project Configuration Editor"),
		storeui.WithOverrides(Overrides()),
		storeui.WithLayerTargets(LayerTargets(store, cfg)),
	)
}
