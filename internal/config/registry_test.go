package config

import (
	"os"
	"path/filepath"
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
