package docker

import (
	"os"
	"path/filepath"
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
)

var _blankCfg = configmocks.NewBlankConfig()

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

func TestFindIgnoredDirs(t *testing.T) {
	// Helper to create a directory tree
	mkdirs := func(t *testing.T, root string, dirs ...string) {
		t.Helper()
		for _, d := range dirs {
			if err := os.MkdirAll(filepath.Join(root, d), 0755); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("matches directory patterns", func(t *testing.T) {
		root := t.TempDir()
		mkdirs(t, root, "node_modules/foo", "src", "dist", ".venv/lib")

		dirs, err := FindIgnoredDirs(root, []string{"node_modules/", "dist/", ".venv/"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := map[string]bool{"node_modules": true, "dist": true, ".venv": true}
		got := make(map[string]bool)
		for _, d := range dirs {
			got[d] = true
		}
		for w := range want {
			if !got[w] {
				t.Errorf("expected %q in results, got %v", w, dirs)
			}
		}
		if got["src"] {
			t.Error("src should not be matched")
		}
	})

	t.Run("skips .git directory", func(t *testing.T) {
		root := t.TempDir()
		mkdirs(t, root, ".git/objects", "node_modules")

		// Even with a pattern that would match .git, it should be skipped
		dirs, err := FindIgnoredDirs(root, []string{".git/", "node_modules/"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, d := range dirs {
			if d == ".git" {
				t.Error(".git should never be in results (bind mode needs git)")
			}
		}
		found := false
		for _, d := range dirs {
			if d == "node_modules" {
				found = true
			}
		}
		if !found {
			t.Error("expected node_modules in results")
		}
	})

	t.Run("skips recursion into matched directories", func(t *testing.T) {
		root := t.TempDir()
		mkdirs(t, root, "node_modules/deep/nested")

		dirs, err := FindIgnoredDirs(root, []string{"node_modules/"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(dirs) != 1 {
			t.Fatalf("expected 1 result, got %d: %v", len(dirs), dirs)
		}
		if dirs[0] != "node_modules" {
			t.Errorf("expected node_modules, got %q", dirs[0])
		}
	})

	t.Run("returns empty for no patterns", func(t *testing.T) {
		root := t.TempDir()
		mkdirs(t, root, "node_modules", "dist")

		dirs, err := FindIgnoredDirs(root, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(dirs) != 0 {
			t.Errorf("expected empty, got %v", dirs)
		}
	})

	t.Run("returns empty when no directories match", func(t *testing.T) {
		root := t.TempDir()
		mkdirs(t, root, "src", "lib")

		dirs, err := FindIgnoredDirs(root, []string{"node_modules/", "dist/"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(dirs) != 0 {
			t.Errorf("expected empty, got %v", dirs)
		}
	})

	t.Run("matches patterns without trailing slash", func(t *testing.T) {
		root := t.TempDir()
		mkdirs(t, root, "vendor/pkg", "build/output")

		dirs, err := FindIgnoredDirs(root, []string{"vendor", "build"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := map[string]bool{"vendor": true, "build": true}
		got := make(map[string]bool)
		for _, d := range dirs {
			got[d] = true
		}
		for w := range want {
			if !got[w] {
				t.Errorf("expected %q in results, got %v", w, dirs)
			}
		}
	})

	t.Run("returns error for nonexistent path", func(t *testing.T) {
		dirs, err := FindIgnoredDirs("/nonexistent/path/that/does/not/exist", []string{"node_modules/"})
		if err == nil {
			t.Fatal("expected error for nonexistent path, got nil")
		}
		if dirs != nil {
			t.Errorf("expected nil dirs on error, got %v", dirs)
		}
	})

	t.Run("file-glob patterns do not match similarly named directories", func(t *testing.T) {
		root := t.TempDir()
		mkdirs(t, root, "src", "logs")

		dirs, err := FindIgnoredDirs(root, []string{"*.log", "*.env"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(dirs) != 0 {
			t.Errorf("file-glob patterns should not match directories, got %v", dirs)
		}
	})
}

func TestBindOverlayDirsFromPatterns(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		want     map[string]bool
	}{
		{
			name:     "extracts literal directory patterns",
			patterns: []string{"node_modules/", "build", "vendor/pkg"},
			want:     map[string]bool{"node_modules": true, "build": true, "vendor/pkg": true},
		},
		{
			name:     "skips file globs and likely file paths",
			patterns: []string{"*.log", "**/*.env", ".env", "credentials.json", "dist/"},
			want:     map[string]bool{"dist": true},
		},
		{
			name:     "skips comments blanks negation and git",
			patterns: []string{"", "  ", "# comment", "!keep.txt", ".git/", "./.git/hooks", ".venv/"},
			want:     map[string]bool{".venv": true},
		},
		{
			name:     "dedupes repeated patterns",
			patterns: []string{"node_modules/", "./node_modules", "node_modules/"},
			want:     map[string]bool{"node_modules": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BindOverlayDirsFromPatterns(tt.patterns)

			if len(got) != len(tt.want) {
				t.Fatalf("BindOverlayDirsFromPatterns(%v) returned %d items (%v), want %d", tt.patterns, len(got), got, len(tt.want))
			}

			gotSet := make(map[string]bool)
			for _, d := range got {
				gotSet[d] = true
			}

			for wantDir := range tt.want {
				if !gotSet[wantDir] {
					t.Errorf("expected %q in result, got %v", wantDir, got)
				}
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
		path := filepath.Join(dir, _blankCfg.ClawkerIgnoreName())
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
		path := filepath.Join(dir, _blankCfg.ClawkerIgnoreName())
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
		path := filepath.Join(dir, _blankCfg.ClawkerIgnoreName())
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

	t.Run("returns error for malformed pattern", func(t *testing.T) {
		tmpDir := t.TempDir()
		ignoreFile := filepath.Join(tmpDir, _blankCfg.ClawkerIgnoreName())
		if err := os.WriteFile(ignoreFile, []byte("[invalid-pattern\n"), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadIgnorePatterns(ignoreFile)
		if err == nil {
			t.Fatal("expected error for malformed glob pattern, got nil")
		}
	})
}
