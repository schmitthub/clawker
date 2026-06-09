package project

import "github.com/schmitthub/clawker/internal/storage"

// ProjectEntry represents a project in the registry.
type ProjectEntry struct {
	Name      string                   `yaml:"name" label:"Name" desc:"Project slug identifier"`
	Root      string                   `yaml:"root" label:"Root" desc:"Filesystem path to project root"`
	Worktrees map[string]WorktreeEntry `yaml:"worktrees,omitempty" label:"Worktrees" desc:"Active worktrees for this project"`
}

// WorktreeEntry represents a worktree within a project.
type WorktreeEntry struct {
	Path   string `yaml:"path" label:"Path" desc:"Filesystem path to worktree"`
	Branch string `yaml:"branch,omitempty" label:"Branch" desc:"Git branch for this worktree"`
}

// ProjectRegistry is the on-disk structure for the project registry
// (consts.RegistryFile in the data dir). internal/project is its sole owner: the
// registry store, walk-up resolver, and all CRUD live here.
type ProjectRegistry struct {
	Projects []ProjectEntry `yaml:"projects" label:"Projects" desc:"Registered projects"`
}

// Fields implements [storage.Schema] for ProjectRegistry.
func (r ProjectRegistry) Fields() storage.FieldSet {
	return storage.NormalizeFields(r)
}
