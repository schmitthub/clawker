package docker

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
)

var _blankCfg = configmocks.NewBlankConfig()

func TestIgnorePatternMatch(t *testing.T) {
	// Gitignore matching conformance is proven by TestIgnoreGitignoreParity
	// against a real `git check-ignore` oracle. This table covers only the
	// clawker-owned behavior in compileIgnorePatterns: the comment/blank
	// trim-and-skip loop and the empty-matcher base case.
	tests := []struct {
		name     string
		path     string
		isDir    bool
		patterns []string
		want     bool
	}{
		{
			// A file literally named like the comment line: if the skip were
			// removed, the line would parse as a pattern and match it.
			name:     "comment line is skipped, not parsed as a pattern",
			path:     "# this is a comment",
			isDir:    false,
			patterns: []string{"# this is a comment"},
			want:     false,
		},
		{
			name:     "whitespace-padded pattern is trimmed before parsing",
			path:     "build",
			isDir:    true,
			patterns: []string{"  build/  "},
			want:     true,
		},
		{
			name:     "no patterns match nothing",
			path:     "anything",
			isDir:    false,
			patterns: []string{},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compileIgnorePatterns(tt.patterns).Match(splitIgnorePath(tt.path), tt.isDir)
			if got != tt.want {
				t.Errorf(
					"Match(%q, isDir=%v) with patterns %v = %v, want %v",
					tt.path,
					tt.isDir,
					tt.patterns,
					got,
					tt.want,
				)
			}
		})
	}
}

// TestCreateTarArchiveIgnores exercises ignore integration on the snapshot
// copy path end-to-end: .clawkerignore patterns are honored verbatim, .git is
// copied by default (no hardcoded skip — snapshot isolation comes from the
// copy direction, not from withholding git history), and negation re-includes
// files through the tar walk.
func TestCreateTarArchiveIgnores(t *testing.T) {
	tests := []struct {
		name        string
		patterns    []string
		wantPresent []string
		wantAbsent  []string
	}{
		{
			name:        "no patterns copies everything including .git",
			patterns:    nil,
			wantPresent: []string{".git/config", "src/main.go", "node_modules/pkg/index.js"},
		},
		{
			name:        ".git excluded only when a pattern matches it",
			patterns:    []string{".git/"},
			wantPresent: []string{"src/main.go"},
			wantAbsent:  []string{".git", ".git/config"},
		},
		{
			name:       "directory pattern prunes the whole subtree",
			patterns:   []string{"node_modules/"},
			wantAbsent: []string{"node_modules", "node_modules/pkg/index.js"},
		},
		{
			name:        "negation re-includes a file through the walk",
			patterns:    []string{"*.log", "!keep.log"},
			wantPresent: []string{"logs/keep.log"},
			wantAbsent:  []string{"logs/drop.log"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			names := tarArchiveNames(t, tt.patterns)
			assertArchiveNames(t, names, tt.wantPresent, tt.wantAbsent)
		})
	}
}

// assertArchiveNames checks the archive entry set for required and forbidden
// entries.
func assertArchiveNames(t *testing.T, names map[string]bool, wantPresent, wantAbsent []string) {
	t.Helper()
	for _, p := range wantPresent {
		if !names[p] {
			t.Errorf("expected %q in archive, got %v", p, names)
		}
	}
	for _, p := range wantAbsent {
		if names[p] {
			t.Errorf("expected %q excluded from archive, got %v", p, names)
		}
	}
}

// tarArchiveNames runs createTarArchive over a fixed source tree with the
// given ignore patterns and returns the set of entry names in the archive.
func tarArchiveNames(t *testing.T, patterns []string) map[string]bool {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{
		".git/config",
		"src/main.go",
		"node_modules/pkg/index.js",
		"logs/keep.log",
		"logs/drop.log",
	} {
		path := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	if err := createTarArchive(root, &buf, patterns, 1001, 1001); err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	tr := tar.NewReader(&buf)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names[filepath.ToSlash(hdr.Name)] = true
	}
	return names
}

func TestFindIgnoredDirs(t *testing.T) {
	// Helper to create a directory tree
	mkdirs := func(t *testing.T, root string, dirs ...string) {
		t.Helper()
		for _, d := range dirs {
			if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
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

	t.Run("anchored pattern matches root only", func(t *testing.T) {
		root := t.TempDir()
		mkdirs(t, root, "build", "internal/build")

		dirs, err := FindIgnoredDirs(root, []string{"/build/"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(dirs) != 1 || dirs[0] != "build" {
			t.Errorf("expected only root build, got %v", dirs)
		}
	})

	t.Run("unanchored pattern matches nested dirs (gitignore parity)", func(t *testing.T) {
		root := t.TempDir()
		mkdirs(t, root, "build", "internal/build")

		dirs, err := FindIgnoredDirs(root, []string{"build/"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := make(map[string]bool)
		for _, d := range dirs {
			got[d] = true
		}
		if !got["build"] || !got["internal/build"] {
			t.Errorf("expected build and internal/build, got %v", dirs)
		}
	})

	t.Run("negation excludes dir from results", func(t *testing.T) {
		root := t.TempDir()
		mkdirs(t, root, "build", "internal/build")

		dirs, err := FindIgnoredDirs(root, []string{"build/", "!internal/build/"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(dirs) != 1 || dirs[0] != "build" {
			t.Errorf("expected only root build after negation, got %v", dirs)
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
		{
			name:     "negation removes a static overlay candidate",
			patterns: []string{"node_modules/", "dist/", "!node_modules/"},
			want:     map[string]bool{"dist": true},
		},
		{
			// A dir masked by a **/-prefixed pattern must get a static
			// overlay even before it exists on the host, or a
			// container-created node_modules writes through to the host.
			name:     "doublestar prefix derives root candidate",
			patterns: []string{"**/node_modules/"},
			want:     map[string]bool{"node_modules": true},
		},
		{
			name:     "doublestar prefix with nested literal",
			patterns: []string{"**/vendor/bundle/"},
			want:     map[string]bool{"vendor/bundle": true},
		},
		{
			name:     "doublestar prefix with remaining glob still skipped",
			patterns: []string{"**/*.log", "**/build-*/"},
			want:     map[string]bool{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BindOverlayDirsFromPatterns(tt.patterns)

			if len(got) != len(tt.want) {
				t.Fatalf(
					"BindOverlayDirsFromPatterns(%v) returned %d items (%v), want %d",
					tt.patterns,
					len(got),
					got,
					len(tt.want),
				)
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
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
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
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
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
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Remove read permission
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			os.Chmod(path, 0o644) // restore for cleanup
		})

		_, err := LoadIgnorePatterns(path)
		if err == nil {
			t.Error("expected permission error, got nil")
		}
	})

	t.Run("malformed glob and negation lines are kept verbatim (gitignore semantics)", func(t *testing.T) {
		tmpDir := t.TempDir()
		ignoreFile := filepath.Join(tmpDir, _blankCfg.ClawkerIgnoreName())
		if err := os.WriteFile(ignoreFile, []byte("[invalid-pattern\n!keep/\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		patterns, err := LoadIgnorePatterns(ignoreFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"[invalid-pattern", "!keep/"}
		if len(patterns) != len(want) {
			t.Fatalf("got %d patterns, want %d: %v", len(patterns), len(want), patterns)
		}
		for i := range want {
			if patterns[i] != want[i] {
				t.Errorf("patterns[%d] = %q, want %q", i, patterns[i], want[i])
			}
		}
	})
}
