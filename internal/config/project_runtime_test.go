package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestProject_RuntimeAccessors_NotInProject(t *testing.T) {
	// ProjectCfg with no runtime context
	p := &Project{Project: ""}

	if p.Found() {
		t.Error("Found() should return false when ProjectCfg field is empty")
	}
	if p.Key() != "" {
		t.Errorf("Key() = %q, want empty", p.Key())
	}
	if p.RootDir() != "" {
		t.Errorf("RootDir() = %q, want empty", p.RootDir())
	}
	if p.DisplayName() != "" {
		t.Errorf("DisplayName() = %q, want empty", p.DisplayName())
	}
}

func TestProject_RuntimeAccessors_InProject(t *testing.T) {
	entry := &ProjectEntry{
		Name: "My Test ProjectCfg",
		Root: "/home/user/myproject",
	}
	p := &Project{Project: "my-test-project"}
	p.setRuntimeContext(entry, nil)

	if !p.Found() {
		t.Error("Found() should return true when ProjectCfg field is set")
	}
	if p.Key() != "my-test-project" {
		t.Errorf("Key() = %q, want %q", p.Key(), "my-test-project")
	}
	if p.RootDir() != "/home/user/myproject" {
		t.Errorf("RootDir() = %q, want %q", p.RootDir(), "/home/user/myproject")
	}
	if p.DisplayName() != "My Test ProjectCfg" {
		t.Errorf("DisplayName() = %q, want %q", p.DisplayName(), "My Test ProjectCfg")
	}
}

func TestProject_DisplayName_FallbackToKey(t *testing.T) {
	entry := &ProjectEntry{
		Name: "", // empty name
		Root: "/home/user/myproject",
	}
	p := &Project{Project: "my-test-project"}
	p.setRuntimeContext(entry, nil)

	// Should fall back to key when name is empty
	if p.DisplayName() != "my-test-project" {
		t.Errorf("DisplayName() = %q, want %q (fallback to key)", p.DisplayName(), "my-test-project")
	}
}

func TestProject_WorktreeMethods_NotInProject(t *testing.T) {
	p := &Project{Project: ""} // not in a project

	// All worktree methods should return ErrNotInProject
	_, err := p.GetOrCreateWorktreeDir("feature/test")
	if !errors.Is(err, ErrNotInProject) {
		t.Errorf("GetOrCreateWorktreeDir() error = %v, want ErrNotInProject", err)
	}

	_, err = p.GetWorktreeDir("feature/test")
	if !errors.Is(err, ErrNotInProject) {
		t.Errorf("GetWorktreeDir() error = %v, want ErrNotInProject", err)
	}

	err = p.DeleteWorktreeDir("feature/test")
	if !errors.Is(err, ErrNotInProject) {
		t.Errorf("DeleteWorktreeDir() error = %v, want ErrNotInProject", err)
	}

	_, err = p.ListWorktreeDirs()
	if !errors.Is(err, ErrNotInProject) {
		t.Errorf("ListWorktreeDirs() error = %v, want ErrNotInProject", err)
	}
}

func TestProject_GetOrCreateWorktreeDir(t *testing.T) {
	tmpDir := t.TempDir()
	clawkerHome := filepath.Join(tmpDir, "clawker-home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	// Create a registry with a project
	registry, err := NewRegistryLoader()
	if err != nil {
		t.Fatalf("NewRegistryLoader() error = %v", err)
	}

	projectRoot := filepath.Join(tmpDir, "myproject")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatalf("failed to create project root: %v", err)
	}

	slug, err := registry.Register("My ProjectCfg", projectRoot)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Set up the ProjectCfg with runtime context
	entry := &ProjectEntry{
		Name:      "My ProjectCfg",
		Root:      projectRoot,
		Worktrees: make(map[string]string),
	}
	p := &Project{Project: slug}
	p.setRuntimeContext(entry, registry)

	// Test GetOrCreateWorktreeDir
	branchName := "feature/my-feature"
	path, err := p.GetOrCreateWorktreeDir(branchName)
	if err != nil {
		t.Fatalf("GetOrCreateWorktreeDir() error = %v", err)
	}

	// Should be a valid path under clawker home
	expectedDir := filepath.Join(clawkerHome, "projects", slug, "worktrees")
	if !hasPrefix(path, expectedDir) {
		t.Errorf("GetOrCreateWorktreeDir() path = %q, should be under %q", path, expectedDir)
	}

	// Directory should exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("GetOrCreateWorktreeDir() did not create directory at %q", path)
	}

	// Calling again should return the same path
	path2, err := p.GetOrCreateWorktreeDir(branchName)
	if err != nil {
		t.Fatalf("GetOrCreateWorktreeDir() second call error = %v", err)
	}
	if path != path2 {
		t.Errorf("GetOrCreateWorktreeDir() second call = %q, want %q", path2, path)
	}
}

func TestProject_GetWorktreeDir(t *testing.T) {
	tmpDir := t.TempDir()
	clawkerHome := filepath.Join(tmpDir, "clawker-home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	// Create a registry with a project
	registry, err := NewRegistryLoader()
	if err != nil {
		t.Fatalf("NewRegistryLoader() error = %v", err)
	}

	projectRoot := filepath.Join(tmpDir, "myproject")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatalf("failed to create project root: %v", err)
	}

	slug, err := registry.Register("My ProjectCfg", projectRoot)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Set up the ProjectCfg with runtime context
	entry := &ProjectEntry{
		Name:      "My ProjectCfg",
		Root:      projectRoot,
		Worktrees: make(map[string]string),
	}
	p := &Project{Project: slug}
	p.setRuntimeContext(entry, registry)

	// GetWorktreeDir for non-existent worktree should fail
	_, err = p.GetWorktreeDir("feature/nonexistent")
	if err == nil {
		t.Error("GetWorktreeDir() should fail for non-existent worktree")
	}

	// Create a worktree first
	branchName := "feature/test"
	createdPath, err := p.GetOrCreateWorktreeDir(branchName)
	if err != nil {
		t.Fatalf("GetOrCreateWorktreeDir() error = %v", err)
	}

	// Now GetWorktreeDir should work
	gotPath, err := p.GetWorktreeDir(branchName)
	if err != nil {
		t.Fatalf("GetWorktreeDir() error = %v", err)
	}
	if gotPath != createdPath {
		t.Errorf("GetWorktreeDir() = %q, want %q", gotPath, createdPath)
	}
}

func TestProject_DeleteWorktreeDir(t *testing.T) {
	tmpDir := t.TempDir()
	clawkerHome := filepath.Join(tmpDir, "clawker-home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	// Create a registry with a project
	registry, err := NewRegistryLoader()
	if err != nil {
		t.Fatalf("NewRegistryLoader() error = %v", err)
	}

	projectRoot := filepath.Join(tmpDir, "myproject")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatalf("failed to create project root: %v", err)
	}

	slug, err := registry.Register("My ProjectCfg", projectRoot)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Set up the ProjectCfg with runtime context
	entry := &ProjectEntry{
		Name:      "My ProjectCfg",
		Root:      projectRoot,
		Worktrees: make(map[string]string),
	}
	p := &Project{Project: slug}
	p.setRuntimeContext(entry, registry)

	// DeleteWorktreeDir for non-existent worktree should fail
	err = p.DeleteWorktreeDir("feature/nonexistent")
	if err == nil {
		t.Error("DeleteWorktreeDir() should fail for non-existent worktree")
	}

	// Create a worktree
	branchName := "feature/to-delete"
	path, err := p.GetOrCreateWorktreeDir(branchName)
	if err != nil {
		t.Fatalf("GetOrCreateWorktreeDir() error = %v", err)
	}

	// Verify directory exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("worktree directory should exist at %q", path)
	}

	// Delete it
	err = p.DeleteWorktreeDir(branchName)
	if err != nil {
		t.Fatalf("DeleteWorktreeDir() error = %v", err)
	}

	// Directory should be gone
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("DeleteWorktreeDir() should have removed directory at %q", path)
	}

	// GetWorktreeDir should now fail
	_, err = p.GetWorktreeDir(branchName)
	if err == nil {
		t.Error("GetWorktreeDir() should fail after delete")
	}
}

func TestProject_ListWorktreeDirs(t *testing.T) {
	tmpDir := t.TempDir()
	clawkerHome := filepath.Join(tmpDir, "clawker-home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	// Create a registry with a project
	registry, err := NewRegistryLoader()
	if err != nil {
		t.Fatalf("NewRegistryLoader() error = %v", err)
	}

	projectRoot := filepath.Join(tmpDir, "myproject")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatalf("failed to create project root: %v", err)
	}

	slug, err := registry.Register("My ProjectCfg", projectRoot)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Set up the ProjectCfg with runtime context
	entry := &ProjectEntry{
		Name:      "My ProjectCfg",
		Root:      projectRoot,
		Worktrees: make(map[string]string),
	}
	p := &Project{Project: slug}
	p.setRuntimeContext(entry, registry)

	// Empty list initially
	dirs, err := p.ListWorktreeDirs()
	if err != nil {
		t.Fatalf("ListWorktreeDirs() error = %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("ListWorktreeDirs() = %d items, want 0", len(dirs))
	}

	// Create two worktrees
	_, err = p.GetOrCreateWorktreeDir("feature/one")
	if err != nil {
		t.Fatalf("GetOrCreateWorktreeDir(one) error = %v", err)
	}
	_, err = p.GetOrCreateWorktreeDir("feature/two")
	if err != nil {
		t.Fatalf("GetOrCreateWorktreeDir(two) error = %v", err)
	}

	// Should have two worktrees
	dirs, err = p.ListWorktreeDirs()
	if err != nil {
		t.Fatalf("ListWorktreeDirs() error = %v", err)
	}
	if len(dirs) != 2 {
		t.Errorf("ListWorktreeDirs() = %d items, want 2", len(dirs))
	}

	// Verify each has Name, Slug, and Path
	for _, dir := range dirs {
		if dir.Name == "" {
			t.Error("WorktreeDirInfo.Name should not be empty")
		}
		if dir.Slug == "" {
			t.Error("WorktreeDirInfo.Slug should not be empty")
		}
		if dir.Path == "" {
			t.Error("WorktreeDirInfo.Path should not be empty")
		}
	}
}

func TestProject_WorktreeMethods_NoRegistry(t *testing.T) {
	// ProjectCfg with runtime context but no registry loader
	entry := &ProjectEntry{
		Name: "My ProjectCfg",
		Root: "/home/user/myproject",
	}
	p := &Project{Project: "my-project"}
	p.setRuntimeContext(entry, nil) // nil registry

	// setWorktreeSlug requires a registry
	_, err := p.GetOrCreateWorktreeDir("feature/test")
	if err == nil {
		t.Error("GetOrCreateWorktreeDir() should fail when registry is nil")
	}
	if err.Error() == "" || err.Error() == string(ErrNotInProject.Error()) {
		t.Errorf("GetOrCreateWorktreeDir() error should mention registry, got: %v", err)
	}
}

// hasPrefix checks if path starts with prefix (handling path separators)
func hasPrefix(path, prefix string) bool {
	return len(path) >= len(prefix) && path[:len(prefix)] == prefix
}

func TestProject_GetOrCreateWorktreeDir_Concurrent(t *testing.T) {
	tmpDir := t.TempDir()
	clawkerHome := filepath.Join(tmpDir, "clawker-home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	// Create a registry with a project
	registry, err := NewRegistryLoader()
	if err != nil {
		t.Fatalf("NewRegistryLoader() error = %v", err)
	}

	projectRoot := filepath.Join(tmpDir, "myproject")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatalf("failed to create project root: %v", err)
	}

	slug, err := registry.Register("My ProjectCfg", projectRoot)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Set up the ProjectCfg with runtime context
	entry := &ProjectEntry{
		Name:      "My ProjectCfg",
		Root:      projectRoot,
		Worktrees: make(map[string]string),
	}
	p := &Project{Project: slug}
	p.setRuntimeContext(entry, registry)

	// Test concurrent access with multiple goroutines
	const numGoroutines = 10
	const numIterations = 5
	errChan := make(chan error, numGoroutines*numIterations)
	pathChan := make(chan string, numGoroutines*numIterations)

	branchName := "feature/concurrent-test"

	// Start multiple goroutines calling GetOrCreateWorktreeDir simultaneously
	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				path, err := p.GetOrCreateWorktreeDir(branchName)
				if err != nil {
					errChan <- err
				} else {
					pathChan <- path
				}
			}
		}()
	}

	wg.Wait()
	close(errChan)
	close(pathChan)

	// Check for errors
	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		t.Errorf("concurrent GetOrCreateWorktreeDir() had %d errors: first error = %v", len(errs), errs[0])
	}

	// All paths should be the same
	var paths []string
	for path := range pathChan {
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		t.Fatal("no successful calls to GetOrCreateWorktreeDir()")
	}

	expectedPath := paths[0]
	for i, path := range paths {
		if path != expectedPath {
			t.Errorf("concurrent call %d returned different path: got %q, want %q", i, path, expectedPath)
		}
	}
}
