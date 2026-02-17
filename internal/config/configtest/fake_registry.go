// Package configtest provides test fakes for config types.
package configtest

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
)

// FakeRegistryBuilder builds a fake registry for testing.
// Uses a temp directory for the registry file, allowing full load/save cycles.
type FakeRegistryBuilder struct {
	dir      string
	projects map[string]*FakeProjectBuilder
}

// NewFakeRegistryBuilder creates a new fake registry builder.
// Call Build() to get the RegistryLoader.
func NewFakeRegistryBuilder(dir string) *FakeRegistryBuilder {
	return &FakeRegistryBuilder{
		dir:      dir,
		projects: make(map[string]*FakeProjectBuilder),
	}
}

// WithProject adds a project to the registry.
func (b *FakeRegistryBuilder) WithProject(key, name, root string) *FakeProjectBuilder {
	pb := &FakeProjectBuilder{
		parent:    b,
		key:       key,
		name:      name,
		root:      root,
		worktrees: make(map[string]string),
	}
	b.projects[key] = pb
	return pb
}

// Build creates the registry file and returns the loader.
// Returns config.Registry interface for compatibility with test setups.
func (b *FakeRegistryBuilder) Build() (config.Registry, error) {
	registry := &config.ProjectRegistry{
		Projects: make(map[string]config.ProjectEntry),
	}

	for key, pb := range b.projects {
		entry := config.ProjectEntry{
			Name:      pb.name,
			Root:      pb.root,
			Worktrees: pb.worktrees,
		}
		registry.Projects[key] = entry
	}

	// Write registry to file
	loader := NewRegistryLoaderForTest(b.dir)
	if err := loader.Save(registry); err != nil {
		return nil, fmt.Errorf("saving fake registry: %w", err)
	}

	return loader, nil
}

// FakeProjectBuilder builds a fake project entry.
type FakeProjectBuilder struct {
	parent    *FakeRegistryBuilder
	key       string
	name      string
	root      string
	worktrees map[string]string
}

// WithWorktree adds a worktree entry to the project.
// name is the branch name, slug is the filesystem-safe slug.
func (pb *FakeProjectBuilder) WithWorktree(name, slug string) *FakeProjectBuilder {
	pb.worktrees[name] = slug
	return pb
}

// Registry returns to the parent builder.
func (pb *FakeProjectBuilder) Registry() *FakeRegistryBuilder {
	return pb.parent
}

// NewRegistryLoaderForTest creates a RegistryLoader that uses a temp directory.
// This is exported for tests that need direct loader access.
func NewRegistryLoaderForTest(dir string) *config.RegistryLoader {
	return config.NewRegistryLoaderWithPath(dir)
}

// FakeWorktreeFS sets up filesystem artifacts for a worktree.
// Call this to make DirExists() and GitExists() return specific values.
type FakeWorktreeFS struct {
	homeDir      string
	projectKey   string
	worktreeSlug string
}

// NewFakeWorktreeFS creates helpers for setting up worktree filesystem state.
func NewFakeWorktreeFS(homeDir, projectKey, worktreeSlug string) *FakeWorktreeFS {
	return &FakeWorktreeFS{
		homeDir:      homeDir,
		projectKey:   projectKey,
		worktreeSlug: worktreeSlug,
	}
}

// WorktreePath returns the worktree directory path.
func (f *FakeWorktreeFS) WorktreePath() string {
	return filepath.Join(f.homeDir, "projects", f.projectKey, "worktrees", f.worktreeSlug)
}

// CreateDir creates the worktree directory.
func (f *FakeWorktreeFS) CreateDir() error {
	return os.MkdirAll(f.WorktreePath(), 0755)
}

// CreateGitFile creates a .git file pointing to the expected location.
// projectRoot is where the main git repo is (for constructing the gitdir path).
func (f *FakeWorktreeFS) CreateGitFile(projectRoot string) error {
	wtPath := f.WorktreePath()
	if err := os.MkdirAll(wtPath, 0755); err != nil {
		return err
	}

	gitFile := filepath.Join(wtPath, ".git")
	gitDir := filepath.Join(projectRoot, ".git", "worktrees", f.worktreeSlug)
	content := fmt.Sprintf("gitdir: %s\n", gitDir)
	return os.WriteFile(gitFile, []byte(content), 0644)
}

// CreateBoth creates both the directory and .git file.
func (f *FakeWorktreeFS) CreateBoth(projectRoot string) error {
	if err := f.CreateDir(); err != nil {
		return err
	}
	return f.CreateGitFile(projectRoot)
}
