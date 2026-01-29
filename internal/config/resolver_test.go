package config

import (
	"testing"
)

func TestResolver_Resolve_Found(t *testing.T) {
	registry := &ProjectRegistry{
		Projects: map[string]ProjectEntry{
			"my-app": {Name: "My App", Root: "/home/user/myapp"},
		},
	}

	resolver := NewResolver(registry)
	res := resolver.Resolve("/home/user/myapp")

	if !res.Found() {
		t.Error("expected Found() to be true for exact match")
	}
	if res.ProjectKey != "my-app" {
		t.Errorf("ProjectKey = %q, want %q", res.ProjectKey, "my-app")
	}
	if res.ProjectRoot() != "/home/user/myapp" {
		t.Errorf("ProjectRoot() = %q, want %q", res.ProjectRoot(), "/home/user/myapp")
	}
}

func TestResolver_Resolve_ChildDir(t *testing.T) {
	registry := &ProjectRegistry{
		Projects: map[string]ProjectEntry{
			"my-app": {Name: "My App", Root: "/home/user/myapp"},
		},
	}

	resolver := NewResolver(registry)
	res := resolver.Resolve("/home/user/myapp/src/pkg")

	if !res.Found() {
		t.Error("expected Found() to be true for child directory")
	}
	if res.ProjectKey != "my-app" {
		t.Errorf("ProjectKey = %q, want %q", res.ProjectKey, "my-app")
	}
}

func TestResolver_Resolve_NotFound(t *testing.T) {
	registry := &ProjectRegistry{
		Projects: map[string]ProjectEntry{
			"my-app": {Name: "My App", Root: "/home/user/myapp"},
		},
	}

	resolver := NewResolver(registry)
	res := resolver.Resolve("/home/user/other")

	if res.Found() {
		t.Error("expected Found() to be false for non-matching directory")
	}
	if res.ProjectRoot() != "" {
		t.Errorf("ProjectRoot() should be empty, got %q", res.ProjectRoot())
	}
}

func TestResolver_Resolve_NilRegistry(t *testing.T) {
	resolver := NewResolver(nil)
	res := resolver.Resolve("/any/dir")

	if res.Found() {
		t.Error("expected Found() to be false for nil registry")
	}
	if res.WorkDir != "/any/dir" {
		t.Errorf("WorkDir = %q, want %q", res.WorkDir, "/any/dir")
	}
}

func TestResolution_NilReceiver(t *testing.T) {
	var res *Resolution
	if res.Found() {
		t.Error("nil Resolution.Found() should return false")
	}
	if res.ProjectRoot() != "" {
		t.Error("nil Resolution.ProjectRoot() should return empty string")
	}
}
