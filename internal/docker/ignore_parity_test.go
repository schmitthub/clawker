package docker //nolint:testpackage // exercises unexported compileIgnorePatterns/splitIgnorePath directly

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestIgnoreGitignoreParity is the contract test for .clawkerignore matching:
// every verdict must agree with real `git check-ignore` on the same patterns.
// Per-path pattern semantics only — directory-descent behavior (git prunes
// ignored dirs; the walks here SkipDir) is covered by TestFindIgnoredDirs.
func TestIgnoreGitignoreParity(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}

	cases := []struct {
		name     string
		patterns []string
		path     string
		isDir    bool
	}{
		{"unanchored dir at depth", []string{"build/"}, "internal/build", true},
		{"anchored dir at root", []string{"/build/"}, "build", true},
		{"anchored dir not nested", []string{"/build/"}, "internal/build", true},
		{"middle slash anchors", []string{"docs/build/"}, "docs/build", true},
		{"middle slash does not float", []string{"docs/build/"}, "x/docs/build", true},
		{"dir-only vs file", []string{"node_modules/"}, "node_modules", false},
		{"dir-only vs dir", []string{"node_modules/"}, "node_modules", true},
		{"basename glob at depth", []string{"*.go"}, "src/main.go", false},
		{"basename glob no match", []string{"*.txt"}, "src/main.go", false},
		{"unanchored exact file at depth", []string{".env"}, "sub/.env", false},
		{"basename literal at depth", []string{"Makefile"}, "deep/nested/Makefile", false},
		{"doublestar literal suffix", []string{"**/test.log"}, "a/b/c/test.log", false},
		{"doublestar literal suffix at root", []string{"**/test.log"}, "test.log", false},
		{"doublestar wildcard suffix", []string{"**/*.log"}, "a/b/c/test.log", false},
		{"doublestar wildcard suffix no match", []string{"**/*.log"}, "a/b/c/test.txt", false},
		{"anchored glob", []string{"build/*.o"}, "build/output.o", false},
		{"anchored glob star does not cross slash", []string{"build/*.o"}, "build/sub/output.o", false},
		{"doublestar middle", []string{"a/**/b"}, "a/x/y/b", true},
		{"doublestar middle zero dirs", []string{"a/**/b"}, "a/b", true},
		{"char class", []string{"*.p[yc]"}, "x.py", false},
		{"negation re-includes", []string{"*.log", "!important.log"}, "important.log", false},
		{"negation leaves others", []string{"*.log", "!important.log"}, "other.log", false},
		{"later ignore beats earlier negation", []string{"!important.log", "*.log"}, "important.log", false},
		{"negation re-includes dir", []string{"build/", "!internal/build/"}, "internal/build", true},
		{"malformed glob", []string{"[invalid-pattern"}, "src/main.go", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gitIgnored := gitCheckIgnoreVerdict(t, gitPath, tc.patterns, tc.path, tc.isDir)
			got := compileIgnorePatterns(tc.patterns).Match(splitIgnorePath(tc.path), tc.isDir)
			if got != gitIgnored {
				t.Errorf("patterns %v, path %q (isDir=%v): clawker=%v git=%v",
					tc.patterns, tc.path, tc.isDir, got, gitIgnored)
			}
		})
	}
}

// gitCheckIgnoreVerdict builds a throwaway git repo containing .gitignore with
// the given patterns plus the target path, and returns real git's verdict via
// `git check-ignore` (exit 0 = ignored, 1 = not ignored).
func gitCheckIgnoreVerdict(t *testing.T, gitPath string, patterns []string, path string, isDir bool) bool {
	t.Helper()
	repo := t.TempDir()

	if out, code := runGit(t, gitPath, repo, "init", "-q"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	ignoreContent := []byte(strings.Join(patterns, "\n") + "\n")
	if writeErr := os.WriteFile(filepath.Join(repo, ".gitignore"), ignoreContent, 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	makeTarget(t, repo, path, isDir)

	out, code := runGit(t, gitPath, repo, "check-ignore", "-q", "--", path)
	if code > 1 {
		t.Fatalf("git check-ignore failed: %s", out)
	}
	return code == 0
}

// runGit runs git in repo with host/system config disabled, returning combined
// output and exit code; non-exit errors fail the test.
func runGit(t *testing.T, gitPath, repo string, args ...string) ([]byte, int) {
	t.Helper()
	cmd := exec.Command(gitPath, append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			t.Fatalf("git %v: %v (%s)", args, runErr, out)
		}
		return out, exitErr.ExitCode()
	}
	return out, 0
}

// makeTarget creates the probe path inside repo as a directory or empty file.
func makeTarget(t *testing.T, repo, path string, isDir bool) {
	t.Helper()
	target := filepath.Join(repo, filepath.FromSlash(path))
	if isDir {
		if mkErr := os.MkdirAll(target, 0o755); mkErr != nil {
			t.Fatal(mkErr)
		}
		return
	}
	if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
		t.Fatal(mkErr)
	}
	if writeErr := os.WriteFile(target, nil, 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
}
