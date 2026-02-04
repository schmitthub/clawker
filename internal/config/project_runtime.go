package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotInProject is returned when an operation requires a registered project
// but the current working directory is not within a registered project.
var ErrNotInProject = errors.New("not in a registered project directory")

// ErrWorktreeNotFound is returned when a worktree is not found in the registry.
var ErrWorktreeNotFound = errors.New("worktree not found")

// WorktreeDirInfo contains information about a worktree directory.
type WorktreeDirInfo struct {
	Name string // original branch name
	Slug string // slugified directory name
	Path string // full filesystem path
}

// setRuntimeContext injects runtime context from resolution into the project.
// This is called after loading the project config.
func (p *Project) setRuntimeContext(entry *ProjectEntry, registry *RegistryLoader) {
	p.projectEntry = entry
	p.registry = registry
}

// Key returns the project key (slug).
// This is the same as the Project field.
func (p *Project) Key() string {
	if p == nil {
		return ""
	}
	return p.Project
}

// DisplayName returns the project display name from the registry.
// Falls back to the project key if not available.
func (p *Project) DisplayName() string {
	if p == nil {
		return ""
	}
	if p.projectEntry != nil && p.projectEntry.Name != "" {
		return p.projectEntry.Name
	}
	return p.Project
}

// Found returns true if we are in a registered project.
func (p *Project) Found() bool {
	return p != nil && p.Project != ""
}

// RootDir returns the project root directory.
// Returns empty string if not in a registered project.
func (p *Project) RootDir() string {
	if p == nil || p.projectEntry == nil {
		return ""
	}
	return p.projectEntry.Root
}

// ---- WorktreeDirProvider interface implementation ----

// worktreesDir returns the path to the worktrees directory for this project.
// Returns error if not in a registered project.
func (p *Project) worktreesDir() (string, error) {
	if !p.Found() {
		return "", ErrNotInProject
	}
	home, err := ClawkerHome()
	if err != nil {
		return "", fmt.Errorf("determining clawker home: %w", err)
	}
	return filepath.Join(home, "projects", p.Key(), "worktrees"), nil
}

// GetOrCreateWorktreeDir returns the path to a worktree directory,
// creating it if it doesn't exist. The name is typically a branch name
// which will be slugified for filesystem safety.
//
// This method implements git.WorktreeDirProvider.
func (p *Project) GetOrCreateWorktreeDir(name string) (string, error) {
	if !p.Found() {
		return "", ErrNotInProject
	}

	// Check if we already have a slug for this branch name
	slug, exists := p.getWorktreeSlug(name)
	if !exists {
		// Generate new slug and persist mapping
		slug = Slugify(name)
		if err := p.setWorktreeSlug(name, slug); err != nil {
			return "", fmt.Errorf("persisting worktree mapping: %w", err)
		}
	}

	wtDir, err := p.worktreesDir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(wtDir, slug)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", fmt.Errorf("creating worktree directory: %w", err)
	}

	return path, nil
}

// GetWorktreeDir returns the path to an existing worktree directory.
// Returns an error if the worktree directory doesn't exist.
// Returns ErrWorktreeNotFound (wrapped) if the worktree is not in the registry.
//
// This method implements git.WorktreeDirProvider.
func (p *Project) GetWorktreeDir(name string) (string, error) {
	if !p.Found() {
		return "", ErrNotInProject
	}

	slug, exists := p.getWorktreeSlug(name)
	if !exists {
		return "", fmt.Errorf("worktree %q: %w", name, ErrWorktreeNotFound)
	}

	wtDir, err := p.worktreesDir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(wtDir, slug)

	// Verify directory exists
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("worktree directory does not exist: %s: %w", path, ErrWorktreeNotFound)
		}
		return "", fmt.Errorf("checking worktree directory: %w", err)
	}

	return path, nil
}

// DeleteWorktreeDir removes a worktree directory and its registry entry.
// Returns an error if the directory doesn't exist.
// Returns ErrWorktreeNotFound (wrapped) if the worktree is not in the registry.
//
// This method implements git.WorktreeDirProvider.
func (p *Project) DeleteWorktreeDir(name string) error {
	if !p.Found() {
		return ErrNotInProject
	}

	slug, exists := p.getWorktreeSlug(name)
	if !exists {
		return fmt.Errorf("worktree %q: %w", name, ErrWorktreeNotFound)
	}

	wtDir, err := p.worktreesDir()
	if err != nil {
		return err
	}

	path := filepath.Join(wtDir, slug)

	// Remove directory
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("removing worktree directory: %w", err)
	}

	// Remove from registry
	if err := p.deleteWorktreeSlug(name); err != nil {
		return fmt.Errorf("removing worktree from registry: %w", err)
	}

	return nil
}

// ListWorktreeDirs returns information about all worktree directories for this project.
func (p *Project) ListWorktreeDirs() ([]WorktreeDirInfo, error) {
	if !p.Found() {
		return nil, ErrNotInProject
	}

	worktrees := p.getWorktrees()
	if len(worktrees) == 0 {
		return nil, nil
	}

	wtDir, err := p.worktreesDir()
	if err != nil {
		return nil, err
	}

	result := make([]WorktreeDirInfo, 0, len(worktrees))
	for name, slug := range worktrees {
		result = append(result, WorktreeDirInfo{
			Name: name,
			Slug: slug,
			Path: filepath.Join(wtDir, slug),
		})
	}

	return result, nil
}

// ---- Registry helpers for worktree mapping ----

// getWorktrees returns a copy of the worktrees map from the registry entry.
// Returns nil if no worktrees are registered.
func (p *Project) getWorktrees() map[string]string {
	p.worktreeMu.RLock()
	defer p.worktreeMu.RUnlock()

	if p.projectEntry == nil || p.projectEntry.Worktrees == nil {
		return nil
	}
	// Return a defensive copy to avoid data races on the map
	result := make(map[string]string, len(p.projectEntry.Worktrees))
	for k, v := range p.projectEntry.Worktrees {
		result[k] = v
	}
	return result
}

// getWorktreeSlug looks up the slug for a branch name.
func (p *Project) getWorktreeSlug(name string) (string, bool) {
	p.worktreeMu.RLock()
	defer p.worktreeMu.RUnlock()

	if p.projectEntry == nil || p.projectEntry.Worktrees == nil {
		return "", false
	}
	slug, ok := p.projectEntry.Worktrees[name]
	return slug, ok
}

// setWorktreeSlug persists a branch→slug mapping to the registry.
func (p *Project) setWorktreeSlug(name, slug string) error {
	if p.registry == nil {
		return errors.New("no registry loader available")
	}

	p.worktreeMu.Lock()
	defer p.worktreeMu.Unlock()

	err := p.registry.UpdateProject(p.Key(), func(entry *ProjectEntry) error {
		if entry.Worktrees == nil {
			entry.Worktrees = make(map[string]string)
		}
		entry.Worktrees[name] = slug
		// Update our local copy
		p.projectEntry.Worktrees = entry.Worktrees
		return nil
	})

	return err
}

// deleteWorktreeSlug removes a branch→slug mapping from the registry.
func (p *Project) deleteWorktreeSlug(name string) error {
	if p.registry == nil {
		return errors.New("no registry loader available")
	}

	p.worktreeMu.Lock()
	defer p.worktreeMu.Unlock()

	return p.registry.UpdateProject(p.Key(), func(entry *ProjectEntry) error {
		if entry.Worktrees != nil {
			delete(entry.Worktrees, name)
			// Update our local copy
			p.projectEntry.Worktrees = entry.Worktrees
		}
		return nil
	})
}
