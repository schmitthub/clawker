package project

import (
	"fmt"
	"maps"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// Registry is the project registry facade — the single owner of registry
// persistence (the registry file in the data dir) and project-root
// resolution. Construct one per process via NewRegistry and inject it; the
// CLI factory exposes it as f.ProjectRegistry. Nothing else constructs
// registry storage.
//
// Registration/mutation methods are unexported: callers outside this package
// mutate registry state through ProjectManager only.
type Registry struct {
	store *storage.Store[ProjectRegistry]
}

// RegistryOption configures NewRegistry.
type RegistryOption func(*registryOptions)

type registryOptions struct {
	dir string
}

// WithRegistryDir places the registry file in dir instead of the resolved
// data directory. Injection seam for tests; production callers use the
// default.
//
// TODO: This variadic option exists only as a test seam, which violates
// testing.md gotcha #8 (no production params solely for mockability). Replace
// with a data-layer seed (cf. state.NewFromString, per store-backed-package.md)
// and drop this option.
func WithRegistryDir(dir string) RegistryOption {
	return func(o *registryOptions) { o.dir = dir }
}

// NewRegistry creates the project registry facade, reading the registry
// through the storage layer (merge + lock) — the canonical path for clawker
// files, never a raw file read.
func NewRegistry(opts ...RegistryOption) (*Registry, error) {
	var o registryOptions
	for _, opt := range opts {
		opt(&o)
	}
	storageOpts := []storage.Option{
		storage.WithFilenames(consts.RegistryFile),
		storage.WithLock(),
	}
	if o.dir != "" {
		storageOpts = append(storageOpts, storage.WithPaths(o.dir))
	} else {
		storageOpts = append(storageOpts, storage.WithDataDir())
	}
	store, err := storage.New[ProjectRegistry]("", storageOpts...)
	if err != nil {
		return nil, fmt.Errorf("project: loading registry: %w", err)
	}
	return &Registry{store: store}, nil
}

// projects returns all project entries.
func (r *Registry) projects() []ProjectEntry {
	if r == nil || r.store == nil {
		return []ProjectEntry{}
	}
	reg := r.store.Read()
	if reg == nil {
		return []ProjectEntry{}
	}
	return reg.Projects
}

// list returns all project entries in undefined order. Each entry's Worktrees
// map is cloned so callers never alias live store state.
func (r *Registry) list() []ProjectEntry {
	entries := r.projects()
	result := make([]ProjectEntry, len(entries))
	for i, entry := range entries {
		entry.Worktrees = maps.Clone(entry.Worktrees)
		result[i] = entry
	}
	return result
}

func (r *Registry) findByResolvedRoot(root string) (int, ProjectEntry, bool, error) {
	if r == nil || r.store == nil {
		return -1, ProjectEntry{}, false, fmt.Errorf("registry not initialized")
	}
	resolvedRoot := resolveRootPath(root)

	entries := r.projects()
	for i, entry := range entries {
		if resolveRootPath(entry.Root) == resolvedRoot {
			return i, entry, true, nil
		}
	}

	return -1, ProjectEntry{}, false, nil
}

func (r *Registry) projectByRoot(root string) (ProjectEntry, bool, error) {
	_, entry, ok, err := r.findByResolvedRoot(root)
	if err != nil {
		return ProjectEntry{}, false, err
	}
	return entry, ok, nil
}

func (r *Registry) setProjects(entries []ProjectEntry) error {
	return r.store.Set(func(reg *ProjectRegistry) {
		reg.Projects = entries
	})
}

func (r *Registry) removeByRoot(root string) error {
	index, _, ok, err := r.findByResolvedRoot(root)
	if err != nil {
		return err
	}
	if !ok {
		return ErrProjectNotFound
	}

	// Splice a copy, not the live slice, so store state only changes via Set.
	entries := r.list()
	entries = append(entries[:index], entries[index+1:]...)
	return r.setProjects(entries)
}

func (r *Registry) registerWorktree(projectRoot, branch, path string) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("registry not initialized")
	}
	if projectRoot == "" {
		return fmt.Errorf("project root cannot be empty")
	}
	if branch == "" {
		return fmt.Errorf("worktree branch cannot be empty")
	}

	index, _, ok, err := r.findByResolvedRoot(projectRoot)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("project %q not found in registry", projectRoot)
	}

	// list() clones each entry's Worktrees map, so mutating the indexed entry
	// never touches live store state.
	entries := r.list()
	if entries[index].Worktrees == nil {
		entries[index].Worktrees = map[string]WorktreeEntry{}
	}
	entries[index].Worktrees[branch] = WorktreeEntry{Path: path, Branch: branch}
	return r.setProjects(entries)
}

func (r *Registry) unregisterWorktree(projectRoot, branch string) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("registry not initialized")
	}
	if projectRoot == "" {
		return fmt.Errorf("project root cannot be empty")
	}
	if branch == "" {
		return fmt.Errorf("worktree branch cannot be empty")
	}

	index, entry, ok, err := r.findByResolvedRoot(projectRoot)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("project %q not found in registry", projectRoot)
	}
	if len(entry.Worktrees) == 0 {
		return nil
	}

	// list() clones each entry's Worktrees map, so deleting from the indexed
	// entry never touches live store state.
	entries := r.list()
	delete(entries[index].Worktrees, branch)
	return r.setProjects(entries)
}

// save persists staged project registry changes to disk.
func (r *Registry) save() error {
	if r == nil || r.store == nil {
		return fmt.Errorf("registry not initialized")
	}
	return r.store.Write()
}

// register adds a project by root path.
func (r *Registry) register(displayName, rootDir string) (ProjectEntry, error) {
	if r == nil || r.store == nil {
		return ProjectEntry{}, fmt.Errorf("registry not initialized")
	}

	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return ProjectEntry{}, fmt.Errorf("failed to get absolute path: %w", err)
	}

	if _, _, ok, err := r.findByResolvedRoot(absRoot); err != nil {
		return ProjectEntry{}, err
	} else if ok {
		return ProjectEntry{}, ErrProjectExists
	}

	entry := ProjectEntry{Name: displayName, Root: absRoot}
	entries := r.list()
	entries = append(entries, entry)
	if err := r.setProjects(entries); err != nil {
		return ProjectEntry{}, err
	}
	if err := r.save(); err != nil {
		return ProjectEntry{}, err
	}

	return entry, nil
}

func (r *Registry) update(entry ProjectEntry) (ProjectEntry, error) {
	if r == nil || r.store == nil {
		return ProjectEntry{}, fmt.Errorf("registry not initialized")
	}
	if entry.Root == "" {
		return ProjectEntry{}, fmt.Errorf("project root cannot be empty")
	}

	index, existing, ok, err := r.findByResolvedRoot(entry.Root)
	if err != nil {
		return ProjectEntry{}, err
	}
	if !ok {
		return ProjectEntry{}, ErrProjectNotFound
	}

	if entry.Worktrees == nil {
		entry.Worktrees = maps.Clone(existing.Worktrees)
	}

	entries := r.list()
	entries[index] = entry
	if err := r.setProjects(entries); err != nil {
		return ProjectEntry{}, err
	}
	if err := r.save(); err != nil {
		return ProjectEntry{}, err
	}

	return entry, nil
}
