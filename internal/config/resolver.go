package config

// Resolution holds the result of resolving the current working directory
// against the project registry.
type Resolution struct {
	ProjectKey   string
	ProjectEntry ProjectEntry
	WorkDir      string
}

// Found returns true if the working directory was resolved to a registered project.
func (r *Resolution) Found() bool {
	return r != nil && r.ProjectKey != ""
}

// ProjectRoot returns the project root directory, or empty string if not found.
func (r *Resolution) ProjectRoot() string {
	if r == nil || r.ProjectKey == "" {
		return ""
	}
	return r.ProjectEntry.Root
}

// Resolver resolves the current working directory against the project registry.
type Resolver struct {
	registry *ProjectRegistry
}

// NewResolver creates a new Resolver with the given registry.
// If registry is nil, all resolutions will return not-found.
func NewResolver(registry *ProjectRegistry) *Resolver {
	return &Resolver{registry: registry}
}

// Resolve looks up workDir in the registry and returns a Resolution.
func (r *Resolver) Resolve(workDir string) *Resolution {
	if r.registry == nil {
		return &Resolution{WorkDir: workDir}
	}

	key, entry := r.registry.Lookup(workDir)
	return &Resolution{
		ProjectKey:   key,
		ProjectEntry: entry,
		WorkDir:      workDir,
	}
}
