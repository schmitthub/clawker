package project

import (
	"fmt"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/storage"
)

// projectRegistry is an internal facade for project registration and registry operations.
// It is backed by a storage.Store[config.ProjectRegistry] for typed access.
type projectRegistry struct {
	store *storage.Store[config.ProjectRegistry]
}

// newRegistryStore creates a new storage.Store for the project registry.
// Called once during ProjectManager construction.
func newRegistryStore() (*storage.Store[config.ProjectRegistry], error) {
	return storage.NewStore[config.ProjectRegistry](
		storage.WithFilenames("registry.yaml"),
		storage.WithDefaults(config.DefaultRegistryYAML),
		storage.WithDataDir(),
		storage.WithLock(),
	)
}

// newRegistry creates a project registry facade backed by the provided store.
func newRegistry(store *storage.Store[config.ProjectRegistry]) *projectRegistry {
	return &projectRegistry{store: store}
}

// Projects returns all project entries.
func (r *projectRegistry) Projects() []config.ProjectEntry {
	if r == nil || r.store == nil {
		return []config.ProjectEntry{}
	}
	reg := r.store.Get()
	if reg == nil {
		return []config.ProjectEntry{}
	}
	return reg.Projects
}

// List returns all project entries in undefined order.
func (r *projectRegistry) List() []config.ProjectEntry {
	entries := r.Projects()
	result := make([]config.ProjectEntry, len(entries))
	copy(result, entries)
	return result
}

func (r *projectRegistry) findByResolvedRoot(root string) (int, config.ProjectEntry, bool, error) {
	if r == nil || r.store == nil {
		return -1, config.ProjectEntry{}, false, fmt.Errorf("registry not initialized")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return -1, config.ProjectEntry{}, false, fmt.Errorf("failed to get absolute path: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		resolvedRoot = absRoot
	}

	entries := r.Projects()
	for i, entry := range entries {
		entryResolvedRoot, evalErr := filepath.EvalSymlinks(entry.Root)
		if evalErr != nil {
			entryResolvedRoot = entry.Root
		}
		if entryResolvedRoot == resolvedRoot {
			return i, entry, true, nil
		}
	}

	return -1, config.ProjectEntry{}, false, nil
}

func (r *projectRegistry) ProjectByRoot(root string) (config.ProjectEntry, bool, error) {
	_, entry, ok, err := r.findByResolvedRoot(root)
	if err != nil {
		return config.ProjectEntry{}, false, err
	}
	return entry, ok, nil
}

func (r *projectRegistry) setProjects(entries []config.ProjectEntry) {
	r.store.Set(func(reg *config.ProjectRegistry) {
		reg.Projects = entries
	})
}

func (r *projectRegistry) RemoveByRoot(root string) error {
	index, _, ok, err := r.findByResolvedRoot(root)
	if err != nil {
		return err
	}
	if !ok {
		return ErrProjectNotFound
	}

	entries := r.Projects()
	entries = append(entries[:index], entries[index+1:]...)
	r.setProjects(entries)
	return nil
}

func (r *projectRegistry) registerWorktree(projectRoot, branch, path string) error {
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
	if entry.Worktrees == nil {
		entry.Worktrees = map[string]config.WorktreeEntry{}
	}
	entry.Worktrees[branch] = config.WorktreeEntry{Path: path, Branch: branch}

	entries := r.Projects()
	entries[index] = entry
	r.setProjects(entries)
	return nil
}

func (r *projectRegistry) unregisterWorktree(projectRoot, branch string) error {
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

	delete(entry.Worktrees, branch)
	entries := r.Projects()
	entries[index] = entry
	r.setProjects(entries)
	return nil
}

// Save persists staged project registry changes to disk.
func (r *projectRegistry) Save() error {
	if r == nil || r.store == nil {
		return fmt.Errorf("registry not initialized")
	}
	return r.store.Write()
}

// Register adds a project by root path.
func (r *projectRegistry) Register(displayName, rootDir string) (config.ProjectEntry, error) {
	if r == nil || r.store == nil {
		return config.ProjectEntry{}, fmt.Errorf("registry not initialized")
	}

	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return config.ProjectEntry{}, fmt.Errorf("failed to get absolute path: %w", err)
	}

	if _, _, ok, err := r.findByResolvedRoot(absRoot); err != nil {
		return config.ProjectEntry{}, err
	} else if ok {
		return config.ProjectEntry{}, ErrProjectExists
	}

	entry := config.ProjectEntry{Name: displayName, Root: absRoot}
	entries := r.Projects()
	entries = append(entries, entry)
	r.setProjects(entries)
	if err := r.Save(); err != nil {
		return config.ProjectEntry{}, err
	}

	return entry, nil
}

func (r *projectRegistry) Update(entry config.ProjectEntry) (config.ProjectEntry, error) {
	if r == nil || r.store == nil {
		return config.ProjectEntry{}, fmt.Errorf("registry not initialized")
	}
	if entry.Root == "" {
		return config.ProjectEntry{}, fmt.Errorf("project root cannot be empty")
	}

	index, existing, ok, err := r.findByResolvedRoot(entry.Root)
	if err != nil {
		return config.ProjectEntry{}, err
	}
	if !ok {
		return config.ProjectEntry{}, ErrProjectNotFound
	}

	if entry.Worktrees == nil {
		entry.Worktrees = existing.Worktrees
	}

	entries := r.Projects()
	entries[index] = entry
	r.setProjects(entries)
	if err := r.Save(); err != nil {
		return config.ProjectEntry{}, err
	}

	return entry, nil
}
