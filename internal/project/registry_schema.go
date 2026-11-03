package project

import "github.com/schmitthub/clawker/internal/storage"

// keyProjects is the storage key segment addressing ProjectRegistry.Projects —
// the one field of the registry schema, and therefore the only key the
// registry facade reads or writes.
const keyProjects = "projects"

// ProjectEntry represents a project in the registry.
type ProjectEntry struct {
	Name      string                   `yaml:"name"                label:"Name"      desc:"Project slug identifier"`
	Root      string                   `yaml:"root"                label:"Root"      desc:"Filesystem path to project root"`
	Worktrees map[string]WorktreeEntry `yaml:"worktrees,omitempty" label:"Worktrees" desc:"Active worktrees for this project"`
	// ConfigTrust records, per covered key group, the digest of the
	// project-file security settings the user last accepted. The host CLI
	// compares it before it acts on the project config (see
	// config.ProjectTrustDigests). Nil means never recorded.
	ConfigTrust map[string]string `yaml:"config_trust,omitempty" label:"Config Trust" desc:"Digest per covered key group of the project config security settings the user last accepted"`
}

// WorktreeEntry represents a worktree within a project.
type WorktreeEntry struct {
	Path   string `yaml:"path"             label:"Path"   desc:"Filesystem path to worktree"`
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
