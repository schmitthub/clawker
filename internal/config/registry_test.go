package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"My Project", "my-project"},
		{"hello world", "hello-world"},
		{"UPPER_CASE", "upper-case"},
		{"  spaces  ", "spaces"},
		{"foo--bar", "foo-bar"},
		{"special!@#chars", "special-chars"},
		{"", "project"},
		{"---", "project"},
		{"a", "a"},
		{"my.project.name", "my-project-name"},
		{"CamelCase", "camelcase"},
		{"123-numbers", "123-numbers"},
		{"a-very-long-project-name-that-exceeds-the-sixty-four-character-limit-for-slugs", "a-very-long-project-name-that-exceeds-the-sixty-four-character-l"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Slugify(tt.name)
			if got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestUniqueSlug(t *testing.T) {
	existing := map[string]bool{
		"my-project": true,
	}

	// No collision
	got := UniqueSlug("other-project", existing)
	if got != "other-project" {
		t.Errorf("UniqueSlug no collision = %q, want %q", got, "other-project")
	}

	// Collision
	got = UniqueSlug("My Project", existing)
	if got != "my-project-2" {
		t.Errorf("UniqueSlug collision = %q, want %q", got, "my-project-2")
	}

	// Double collision
	existing["my-project-2"] = true
	got = UniqueSlug("My Project", existing)
	if got != "my-project-3" {
		t.Errorf("UniqueSlug double collision = %q, want %q", got, "my-project-3")
	}
}

func TestProjectEntry_Valid(t *testing.T) {
	tests := []struct {
		name    string
		entry   ProjectEntry
		wantErr bool
	}{
		{"valid entry", ProjectEntry{Name: "test", Root: "/home/user/test"}, false},
		{"empty name", ProjectEntry{Name: "", Root: "/home/user/test"}, true},
		{"empty root", ProjectEntry{Name: "test", Root: ""}, true},
		{"relative root", ProjectEntry{Name: "test", Root: "relative/path"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entry.Valid()
			if (err != nil) != tt.wantErr {
				t.Errorf("Valid() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSlugify_TruncationNoTrailingHyphen(t *testing.T) {
	// Verify that 64-char truncation doesn't leave a trailing hyphen
	slug := Slugify("a-very-long-project-name-that-exceeds-the-sixty-four-character-limit-for-slugs")
	if len(slug) > 64 {
		t.Errorf("Slugify() result length %d exceeds 64", len(slug))
	}
	if slug[len(slug)-1] == '-' {
		t.Errorf("Slugify() result has trailing hyphen: %q", slug)
	}
}

func TestProjectRegistry_Lookup(t *testing.T) {
	registry := &ProjectRegistry{
		Projects: map[string]ProjectEntry{
			"my-project": {Name: "My Project", Root: "/home/user/myapp"},
			"other":      {Name: "Other", Root: "/home/user/other"},
			"nested":     {Name: "Nested", Root: "/home/user/myapp/sub"},
		},
	}

	tests := []struct {
		name    string
		workDir string
		wantKey string
	}{
		{"exact match", "/home/user/myapp", "my-project"},
		{"child dir", "/home/user/myapp/src/main", "my-project"},
		{"nested project wins (longest prefix)", "/home/user/myapp/sub/deep", "nested"},
		{"no match", "/home/other/dir", ""},
		{"other project", "/home/user/other", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, _ := registry.Lookup(tt.workDir)
			if key != tt.wantKey {
				t.Errorf("Lookup(%q) key = %q, want %q", tt.workDir, key, tt.wantKey)
			}
		})
	}
}

func TestProjectRegistry_Lookup_NilRegistry(t *testing.T) {
	var registry *ProjectRegistry
	key, _ := registry.Lookup("/some/dir")
	if key != "" {
		t.Errorf("nil registry Lookup should return empty key, got %q", key)
	}
}

func TestProjectRegistry_Lookup_EmptyProjects(t *testing.T) {
	registry := &ProjectRegistry{Projects: map[string]ProjectEntry{}}
	key, _ := registry.Lookup("/some/dir")
	if key != "" {
		t.Errorf("empty projects Lookup should return empty key, got %q", key)
	}
}

func TestProjectRegistry_LookupByKey(t *testing.T) {
	registry := &ProjectRegistry{
		Projects: map[string]ProjectEntry{
			"test": {Name: "Test", Root: "/test"},
		},
	}

	entry, ok := registry.LookupByKey("test")
	if !ok {
		t.Error("LookupByKey should find existing key")
	}
	if entry.Name != "Test" {
		t.Errorf("LookupByKey name = %q, want %q", entry.Name, "Test")
	}

	_, ok = registry.LookupByKey("nonexistent")
	if ok {
		t.Error("LookupByKey should not find nonexistent key")
	}
}

func TestProjectRegistry_HasKey(t *testing.T) {
	registry := &ProjectRegistry{
		Projects: map[string]ProjectEntry{
			"test": {Name: "Test", Root: "/test"},
		},
	}

	if !registry.HasKey("test") {
		t.Error("HasKey should return true for existing key")
	}
	if registry.HasKey("nope") {
		t.Error("HasKey should return false for missing key")
	}
}

func TestRegistryLoader_CRUD(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-registry-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	loader, err := NewRegistryLoader()
	if err != nil {
		t.Fatalf("NewRegistryLoader() error: %v", err)
	}

	// Should not exist initially
	if loader.Exists() {
		t.Error("registry should not exist initially")
	}

	// Load returns empty registry when file missing
	reg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(reg.Projects) != 0 {
		t.Errorf("empty Load() should have 0 projects, got %d", len(reg.Projects))
	}

	// Register a project
	projectRoot := filepath.Join(tmpDir, "myapp")
	os.MkdirAll(projectRoot, 0755)
	slug, err := loader.Register("My App", projectRoot)
	if err != nil {
		t.Fatalf("Register() error: %v", err)
	}
	if slug != "my-app" {
		t.Errorf("Register() slug = %q, want %q", slug, "my-app")
	}

	// File should exist now
	if !loader.Exists() {
		t.Error("registry should exist after Register()")
	}

	// Load and verify
	reg, err = loader.Load()
	if err != nil {
		t.Fatalf("Load() after Register error: %v", err)
	}
	if len(reg.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(reg.Projects))
	}
	entry, ok := reg.Projects["my-app"]
	if !ok {
		t.Fatal("expected key 'my-app' in registry")
	}
	if entry.Name != "My App" {
		t.Errorf("entry.Name = %q, want %q", entry.Name, "My App")
	}

	// Re-register same root returns same slug
	slug2, err := loader.Register("My App Renamed", projectRoot)
	if err != nil {
		t.Fatalf("re-Register() error: %v", err)
	}
	if slug2 != "my-app" {
		t.Errorf("re-Register() slug = %q, want %q (same slug)", slug2, "my-app")
	}

	// Verify name was updated
	reg, _ = loader.Load()
	if reg.Projects["my-app"].Name != "My App Renamed" {
		t.Errorf("expected name update on re-register, got %q", reg.Projects["my-app"].Name)
	}

	// Unregister
	removed, err := loader.Unregister("my-app")
	if err != nil {
		t.Fatalf("Unregister() error: %v", err)
	}
	if !removed {
		t.Error("Unregister() should return true for existing key")
	}

	// Verify removed
	reg, _ = loader.Load()
	if len(reg.Projects) != 0 {
		t.Errorf("expected 0 projects after Unregister, got %d", len(reg.Projects))
	}

	// Unregister nonexistent
	removed, err = loader.Unregister("nope")
	if err != nil {
		t.Fatalf("Unregister(nope) error: %v", err)
	}
	if removed {
		t.Error("Unregister(nope) should return false")
	}
}

func TestRegistryLoader_Register_Collision(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-registry-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	loader, err := NewRegistryLoader()
	if err != nil {
		t.Fatalf("NewRegistryLoader() error: %v", err)
	}

	root1 := filepath.Join(tmpDir, "project1")
	root2 := filepath.Join(tmpDir, "project2")
	os.MkdirAll(root1, 0755)
	os.MkdirAll(root2, 0755)

	// Register first project
	slug1, err := loader.Register("My Project", root1)
	if err != nil {
		t.Fatalf("Register first error: %v", err)
	}
	if slug1 != "my-project" {
		t.Errorf("first slug = %q, want %q", slug1, "my-project")
	}

	// Register second project with same name but different root
	slug2, err := loader.Register("My Project", root2)
	if err != nil {
		t.Fatalf("Register second error: %v", err)
	}
	if slug2 != "my-project-2" {
		t.Errorf("second slug = %q, want %q", slug2, "my-project-2")
	}
}

func TestRegistryLoader_Path(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-registry-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	loader, err := NewRegistryLoader()
	if err != nil {
		t.Fatalf("NewRegistryLoader() error: %v", err)
	}

	expected := filepath.Join(tmpDir, RegistryFileName)
	if loader.Path() != expected {
		t.Errorf("Path() = %q, want %q", loader.Path(), expected)
	}
}


func TestProjectHandle_Get(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClawkerHomeEnv, tmpDir)

	loader, err := NewRegistryLoader()
	if err != nil {
		t.Fatalf("NewRegistryLoader() error: %v", err)
	}

	// Create a project
	projectRoot := filepath.Join(tmpDir, "myapp")
	os.MkdirAll(projectRoot, 0755)
	_, err = loader.Register("My App", projectRoot)
	if err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	// Get via handle
	handle := loader.Project("my-app")
	if handle.Key() != "my-app" {
		t.Errorf("Key() = %q, want %q", handle.Key(), "my-app")
	}

	entry, err := handle.Get()
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if entry.Name != "My App" {
		t.Errorf("entry.Name = %q, want %q", entry.Name, "My App")
	}
	if entry.Root != projectRoot {
		t.Errorf("entry.Root = %q, want %q", entry.Root, projectRoot)
	}

	// Nonexistent project
	badHandle := loader.Project("nope")
	_, err = badHandle.Get()
	if err == nil {
		t.Error("Get() on nonexistent project should return error")
	}
}

func TestProjectHandle_Exists(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClawkerHomeEnv, tmpDir)

	loader, err := NewRegistryLoader()
	if err != nil {
		t.Fatalf("NewRegistryLoader() error: %v", err)
	}

	// Create a project
	projectRoot := filepath.Join(tmpDir, "myapp")
	os.MkdirAll(projectRoot, 0755)
	_, err = loader.Register("My App", projectRoot)
	if err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	// Check existence
	handle := loader.Project("my-app")
	exists, err := handle.Exists()
	if err != nil {
		t.Fatalf("Exists() error: %v", err)
	}
	if !exists {
		t.Error("Exists() should return true for existing project")
	}

	badHandle := loader.Project("nope")
	exists, err = badHandle.Exists()
	if err != nil {
		t.Fatalf("Exists() error: %v", err)
	}
	if exists {
		t.Error("Exists() should return false for nonexistent project")
	}
}

func TestProjectHandle_ListWorktrees(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClawkerHomeEnv, tmpDir)

	loader := NewRegistryLoaderWithPath(tmpDir)

	// Create registry with worktrees
	registry := &ProjectRegistry{
		Projects: map[string]ProjectEntry{
			"test-project": {
				Name: "Test Project",
				Root: filepath.Join(tmpDir, "project"),
				Worktrees: map[string]string{
					"feature-a": "feature-a",
					"feature/b": "feature-b",
				},
			},
		},
	}
	err := loader.Save(registry)
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	handle := loader.Project("test-project")
	worktrees, err := handle.ListWorktrees()
	if err != nil {
		t.Fatalf("ListWorktrees() error: %v", err)
	}
	if len(worktrees) != 2 {
		t.Errorf("ListWorktrees() count = %d, want 2", len(worktrees))
	}

	// Check that we got handles with correct names
	names := make(map[string]bool)
	for _, wh := range worktrees {
		names[wh.Name()] = true
	}
	if !names["feature-a"] {
		t.Error("expected worktree handle for feature-a")
	}
	if !names["feature/b"] {
		t.Error("expected worktree handle for feature/b")
	}
}

func TestWorktreeHandle_DirExists(t *testing.T) {
	tmpDir := t.TempDir()
	clawkerHome := filepath.Join(tmpDir, "clawker")
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	// Create the worktree directory
	wtDir := filepath.Join(clawkerHome, "projects", "test-project", "worktrees", "feature-a")
	os.MkdirAll(wtDir, 0755)

	loader := NewRegistryLoaderWithPath(clawkerHome)
	registry := &ProjectRegistry{
		Projects: map[string]ProjectEntry{
			"test-project": {
				Name: "Test Project",
				Root: filepath.Join(tmpDir, "project"),
				Worktrees: map[string]string{
					"feature-a": "feature-a",
					"feature-b": "feature-b",
				},
			},
		},
	}
	loader.Save(registry)

	handle := loader.Project("test-project")

	// feature-a has directory
	wha := handle.Worktree("feature-a")
	if !wha.DirExists() {
		t.Error("DirExists() should return true for existing directory")
	}

	// feature-b does not have directory
	whb := handle.Worktree("feature-b")
	if whb.DirExists() {
		t.Error("DirExists() should return false for nonexistent directory")
	}
}

func TestWorktreeHandle_GitExists(t *testing.T) {
	tmpDir := t.TempDir()
	clawkerHome := filepath.Join(tmpDir, "clawker")
	projectRoot := filepath.Join(tmpDir, "project")
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	// Create the worktree directory with .git file
	wtDir := filepath.Join(clawkerHome, "projects", "test-project", "worktrees", "feature-a")
	os.MkdirAll(wtDir, 0755)
	gitContent := "gitdir: " + filepath.Join(projectRoot, ".git", "worktrees", "feature-a")
	os.WriteFile(filepath.Join(wtDir, ".git"), []byte(gitContent), 0644)

	loader := NewRegistryLoaderWithPath(clawkerHome)
	registry := &ProjectRegistry{
		Projects: map[string]ProjectEntry{
			"test-project": {
				Name: "Test Project",
				Root: projectRoot,
				Worktrees: map[string]string{
					"feature-a": "feature-a",
					"feature-b": "feature-b",
				},
			},
		},
	}
	loader.Save(registry)

	handle := loader.Project("test-project")

	// feature-a has valid .git file
	wha := handle.Worktree("feature-a")
	if !wha.GitExists() {
		t.Error("GitExists() should return true for valid .git file")
	}

	// feature-b has no .git file
	whb := handle.Worktree("feature-b")
	if whb.GitExists() {
		t.Error("GitExists() should return false for missing .git file")
	}
}

func TestWorktreeStatus_Methods(t *testing.T) {
	tests := []struct {
		name       string
		dirExists  bool
		gitExists  bool
		wantHealthy bool
		wantPrunable bool
		wantString string
	}{
		{"healthy", true, true, true, false, "healthy"},
		{"dir missing only", false, true, false, false, "dir missing"},
		{"git missing only", true, false, false, false, "git missing"},
		{"both missing (prunable)", false, false, false, true, "dir missing, git missing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &WorktreeStatus{
				Name:      "test",
				Slug:      "test",
				DirExists: tt.dirExists,
				GitExists: tt.gitExists,
			}

			if status.IsHealthy() != tt.wantHealthy {
				t.Errorf("IsHealthy() = %v, want %v", status.IsHealthy(), tt.wantHealthy)
			}
			if status.IsPrunable() != tt.wantPrunable {
				t.Errorf("IsPrunable() = %v, want %v", status.IsPrunable(), tt.wantPrunable)
			}
			if status.String() != tt.wantString {
				t.Errorf("String() = %q, want %q", status.String(), tt.wantString)
			}
		})
	}
}

func TestWorktreeStatus_IsPrunable_WithError(t *testing.T) {
	// When Path() fails (e.g., ClawkerHome() error), both DirExists() and GitExists()
	// return false because they can't check the filesystem. However, IsPrunable()
	// should NOT return true because we don't know the actual state - the worktree
	// might exist and we just can't resolve the path.

	status := &WorktreeStatus{
		Name:      "error-branch",
		Slug:      "error-branch",
		Path:      "",
		DirExists: false, // Can't check - path resolution failed
		GitExists: false, // Can't check - path resolution failed
		Error:     errors.New("failed to resolve clawker home"),
	}

	// THE KEY ASSERTION: worktree with error should NOT be prunable
	// When we have an error, we don't know if the directory exists, so we can't safely prune
	if status.IsPrunable() {
		t.Error("worktree with path error should NOT be prunable - we don't know if it exists")
	}

	// Also verify IsHealthy returns false when there's an error
	if status.IsHealthy() {
		t.Error("worktree with error should not be healthy")
	}

	// Verify String() shows the error
	if !strings.Contains(status.String(), "error:") {
		t.Errorf("String() should indicate error, got: %s", status.String())
	}
}

func TestWorktreeHandle_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClawkerHomeEnv, tmpDir)

	loader := NewRegistryLoaderWithPath(tmpDir)
	registry := &ProjectRegistry{
		Projects: map[string]ProjectEntry{
			"test-project": {
				Name: "Test Project",
				Root: filepath.Join(tmpDir, "project"),
				Worktrees: map[string]string{
					"feature-a": "feature-a",
					"feature-b": "feature-b",
				},
			},
		},
	}
	err := loader.Save(registry)
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	handle := loader.Project("test-project")
	wh := handle.Worktree("feature-a")

	// Delete the worktree entry
	err = wh.Delete()
	if err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	// Verify it's gone
	reg, _ := loader.Load()
	entry := reg.Projects["test-project"]
	if _, ok := entry.Worktrees["feature-a"]; ok {
		t.Error("feature-a should be deleted from worktrees map")
	}
	if _, ok := entry.Worktrees["feature-b"]; !ok {
		t.Error("feature-b should still exist")
	}
}
