package project

import (
	"fmt"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// projectRegistry is an internal facade for project registration and registry operations.
// It is backed by a storage.Store[ProjectRegistry] for typed access.
type projectRegistry struct {
	store *storage.Store[ProjectRegistry]
}

// newRegistryStore creates a new storage.Store for the project registry.
func newRegistryStore() (*storage.Store[ProjectRegistry], error) {
	return storage.NewStore[ProjectRegistry](
		storage.WithFilenames(consts.RegistryFile),
		storage.WithDataDir(),
		storage.WithLock(),
	)
}

// newRegistry creates a project registry facade backed by the provided store.
func newRegistry(store *storage.Store[ProjectRegistry]) *projectRegistry {
	return &projectRegistry{store: store}
}

// Projects returns all project entries.
func (r *projectRegistry) Projects() []ProjectEntry {
	if r == nil || r.store == nil {
		return []ProjectEntry{}
	}
	reg := r.store.Get()
	if reg == nil {
		return []ProjectEntry{}
	}
	return reg.Projects
}

// List returns all project entries in undefined order.
func (r *projectRegistry) List() []ProjectEntry {
	entries := r.Projects()
	result := make([]ProjectEntry, len(entries))
	copy(result, entries)
	return result
}

func (r *projectRegistry) findByResolvedRoot(root string) (int, ProjectEntry, bool, error) {
	if r == nil || r.store == nil {
		return -1, ProjectEntry{}, false, fmt.Errorf("registry not initialized")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return -1, ProjectEntry{}, false, fmt.Errorf("failed to get absolute path: %w", err)
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

	return -1, ProjectEntry{}, false, nil
}

func (r *projectRegistry) ProjectByRoot(root string) (ProjectEntry, bool, error) {
	_, entry, ok, err := r.findByResolvedRoot(root)
	if err != nil {
		return ProjectEntry{}, false, err
	}
	return entry, ok, nil
}

func (r *projectRegistry) setProjects(entries []ProjectEntry) error {
	return r.store.Set(func(reg *ProjectRegistry) {
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
	return r.setProjects(entries)
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
		entry.Worktrees = map[string]WorktreeEntry{}
	}
	entry.Worktrees[branch] = WorktreeEntry{Path: path, Branch: branch}

	entries := r.Projects()
	entries[index] = entry
	return r.setProjects(entries)
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
	return r.setProjects(entries)
}

// Save persists staged project registry changes to disk.
func (r *projectRegistry) Save() error {
	if r == nil || r.store == nil {
		return fmt.Errorf("registry not initialized")
	}
	return r.store.Write()
}

// Register adds a project by root path.
func (r *projectRegistry) Register(displayName, rootDir string) (ProjectEntry, error) {
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
	entries := r.Projects()
	entries = append(entries, entry)
	if err := r.setProjects(entries); err != nil {
		return ProjectEntry{}, err
	}
	if err := r.Save(); err != nil {
		return ProjectEntry{}, err
	}

	return entry, nil
}

func (r *projectRegistry) Update(entry ProjectEntry) (ProjectEntry, error) {
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
		entry.Worktrees = existing.Worktrees
	}

	entries := r.Projects()
	entries[index] = entry
	if err := r.setProjects(entries); err != nil {
		return ProjectEntry{}, err
	}
	if err := r.Save(); err != nil {
		return ProjectEntry{}, err
	}

	return entry, nil
}
