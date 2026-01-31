package docker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		pattern string
		want    bool
	}{
		{
			name:    "exact match",
			path:    "foo.txt",
			pattern: "foo.txt",
			want:    true,
		},
		{
			name:    "no match",
			path:    "foo.txt",
			pattern: "bar.txt",
			want:    false,
		},
		{
			name:    "wildcard extension match",
			path:    "src/main.go",
			pattern: "*.go",
			want:    true,
		},
		{
			name:    "wildcard extension no match",
			path:    "src/main.go",
			pattern: "*.txt",
			want:    false,
		},
		{
			name:    "double star with literal suffix",
			path:    "a/b/c/test.log",
			pattern: "**/test.log",
			want:    true,
		},
		{
			name:    "double star with literal suffix no match",
			path:    "a/b/c/test.txt",
			pattern: "**/test.log",
			want:    false,
		},
		{
			name:    "double star with wildcard suffix",
			path:    "a/b/c/test.log",
			pattern: "**/*.log",
			want:    true,
		},
		{
			name:    "double star with wildcard suffix no match",
			path:    "a/b/c/test.txt",
			pattern: "**/*.log",
			want:    false,
		},
		{
			name:    "directory prefix match",
			path:    "vendor/pkg/file.go",
			pattern: "vendor",
			want:    true,
		},
		{
			name:    "basename match",
			path:    "deep/nested/Makefile",
			pattern: "Makefile",
			want:    true,
		},
		{
			name:    "basename no match",
			path:    "deep/nested/Makefile",
			pattern: "Dockerfile",
			want:    false,
		},
		{
			name:    "full path with wildcard",
			path:    "build/output.o",
			pattern: "build/*.o",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchPattern(tt.path, tt.pattern)
			if got != tt.want {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestShouldIgnore(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		isDir    bool
		patterns []string
		want     bool
	}{
		{
			name:     "empty patterns still ignores .git",
			path:     ".git",
			isDir:    true,
			patterns: []string{},
			want:     true,
		},
		{
			name:     ".git subdirectory ignored",
			path:     ".git/objects/pack",
			isDir:    false,
			patterns: []string{},
			want:     true,
		},
		{
			name:     "no match returns false",
			path:     "src/main.go",
			isDir:    false,
			patterns: []string{"*.txt"},
			want:     false,
		},
		{
			name:     "glob match",
			path:     "build/output.o",
			isDir:    false,
			patterns: []string{"*.o"},
			want:     true,
		},
		{
			name:     "exact file match",
			path:     ".env",
			isDir:    false,
			patterns: []string{".env"},
			want:     true,
		},
		{
			name:     "directory-only pattern matches directory",
			path:     "node_modules",
			isDir:    true,
			patterns: []string{"node_modules/"},
			want:     true,
		},
		{
			name:     "directory-only pattern skips file",
			path:     "node_modules",
			isDir:    false,
			patterns: []string{"node_modules/"},
			want:     false,
		},
		{
			name:     "comment lines are skipped",
			path:     "important.txt",
			isDir:    false,
			patterns: []string{"# this is a comment", "*.log"},
			want:     false,
		},
		{
			name:     "empty pattern lines are skipped",
			path:     "important.txt",
			isDir:    false,
			patterns: []string{"", "  ", "*.log"},
			want:     false,
		},
		{
			name:     "negation patterns are skipped (not implemented)",
			path:     "important.log",
			isDir:    false,
			patterns: []string{"*.log", "!important.log"},
			want:     true,
		},
		{
			name:     "double star with literal suffix",
			path:     "a/b/c/test.log",
			isDir:    false,
			patterns: []string{"**/test.log"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldIgnore(tt.path, tt.isDir, tt.patterns)
			if got != tt.want {
				t.Errorf("shouldIgnore(%q, %v, %v) = %v, want %v", tt.path, tt.isDir, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestLoadIgnorePatterns(t *testing.T) {
	t.Run("file not found returns empty slice", func(t *testing.T) {
		patterns, err := LoadIgnorePatterns(filepath.Join(t.TempDir(), "nonexistent"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(patterns) != 0 {
			t.Errorf("expected empty slice, got %v", patterns)
		}
	})

	t.Run("valid file with patterns", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".clawkerignore")
		content := "node_modules\n*.log\nbuild/\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		patterns, err := LoadIgnorePatterns(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := []string{"node_modules", "*.log", "build/"}
		if len(patterns) != len(want) {
			t.Fatalf("got %d patterns, want %d: %v", len(patterns), len(want), patterns)
		}
		for i := range want {
			if patterns[i] != want[i] {
				t.Errorf("patterns[%d] = %q, want %q", i, patterns[i], want[i])
			}
		}
	})

	t.Run("comments and blank lines stripped", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".clawkerignore")
		content := "# This is a comment\n\nnode_modules\n\n# Another comment\n*.log\n  \n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		patterns, err := LoadIgnorePatterns(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := []string{"node_modules", "*.log"}
		if len(patterns) != len(want) {
			t.Fatalf("got %d patterns, want %d: %v", len(patterns), len(want), patterns)
		}
		for i := range want {
			if patterns[i] != want[i] {
				t.Errorf("patterns[%d] = %q, want %q", i, patterns[i], want[i])
			}
		}
	})

	t.Run("permission error is returned", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".clawkerignore")
		if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
		// Remove read permission
		if err := os.Chmod(path, 0000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			os.Chmod(path, 0644) // restore for cleanup
		})

		_, err := LoadIgnorePatterns(path)
		if err == nil {
			t.Error("expected permission error, got nil")
		}
	})
}
