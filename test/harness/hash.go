package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// ComputeTemplateHash generates a content-addressed hash of Dockerfile templates
// and their related type definitions. Used for CI cache invalidation - rebuild
// test images only when templates change.
//
// The hash includes:
//   - All files in internal/bundler/assets/ (Dockerfile.tmpl, entrypoint.sh, etc.)
//   - All files in internal/hostproxy/internals/ (host-open.sh, git-credential-clawker.sh, Go sources)
//   - The dockerfile.go file containing DockerfileContext struct definition
//
// Returns a stable SHA256 hex digest (64 characters).
//
// Usage in CI:
//
//	hash, err := testutil.ComputeTemplateHash()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	// Use hash as cache key: "clawker-test-image-" + hash[:12]
func ComputeTemplateHash() (string, error) {
	rootDir, err := FindProjectRoot()
	if err != nil {
		return "", fmt.Errorf("failed to find project root: %w", err)
	}

	hasher := sha256.New()

	// Hash bundler assets (lifecycle scripts, Dockerfile template)
	templatesDir := filepath.Join(rootDir, "internal", "bundler", "assets")
	if err := hashDirectory(hasher, templatesDir); err != nil {
		return "", fmt.Errorf("failed to hash templates directory: %w", err)
	}

	// Hash hostproxy internals (client scripts and Go sources)
	internalsDir := filepath.Join(rootDir, "internal", "hostproxy", "internals")
	if err := hashDirectory(hasher, internalsDir); err != nil {
		return "", fmt.Errorf("failed to hash internals directory: %w", err)
	}

	// Hash the dockerfile.go file containing struct definitions
	// Changes to DockerfileContext, DockerfileInstructions, etc. should invalidate cache
	dockerfileGo := filepath.Join(rootDir, "internal", "bundler", "dockerfile.go")
	if err := hashFile(hasher, dockerfileGo); err != nil {
		return "", fmt.Errorf("failed to hash dockerfile.go: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// ComputeTemplateHashFromDir computes the template hash using an explicit project root.
// This is useful for testing or when the project root cannot be auto-detected.
func ComputeTemplateHashFromDir(rootDir string) (string, error) {
	hasher := sha256.New()

	// Hash bundler assets (lifecycle scripts, Dockerfile template)
	templatesDir := filepath.Join(rootDir, "internal", "bundler", "assets")
	if err := hashDirectory(hasher, templatesDir); err != nil {
		return "", fmt.Errorf("failed to hash templates directory: %w", err)
	}

	// Hash hostproxy internals (client scripts and Go sources)
	internalsDir := filepath.Join(rootDir, "internal", "hostproxy", "internals")
	if err := hashDirectory(hasher, internalsDir); err != nil {
		return "", fmt.Errorf("failed to hash internals directory: %w", err)
	}

	// Hash the dockerfile.go file containing struct definitions
	dockerfileGo := filepath.Join(rootDir, "internal", "bundler", "dockerfile.go")
	if err := hashFile(hasher, dockerfileGo); err != nil {
		return "", fmt.Errorf("failed to hash dockerfile.go: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// hashDirectory hashes all files in a directory recursively, sorted by name for stability.
func hashDirectory(hasher io.Writer, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	// Sort entries by name for deterministic ordering
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			if err := hashDirectory(hasher, path); err != nil {
				return err
			}
			continue
		}

		if err := hashFile(hasher, path); err != nil {
			return err
		}
	}

	return nil
}

// hashFile hashes a single file's contents, including its name for uniqueness.
func hashFile(hasher io.Writer, path string) error {
	// Include filename in hash for uniqueness even if content is same
	filename := filepath.Base(path)
	if _, err := hasher.Write([]byte(filename)); err != nil {
		return fmt.Errorf("failed to write filename: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	if _, err := io.Copy(hasher, f); err != nil {
		return fmt.Errorf("failed to hash %s: %w", path, err)
	}

	return nil
}

// FindProjectRoot locates the project root by looking for go.mod file.
// Starts from the current file's location and walks up.
func FindProjectRoot() (string, error) {
	// Get the directory containing this source file
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("failed to get caller information")
	}

	dir := filepath.Dir(thisFile)

	// Walk up looking for go.mod
	for {
		goMod := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goMod); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found in any parent directory")
		}
		dir = parent
	}
}

// TemplateHashShort returns a shortened hash suitable for use as a cache key suffix.
// Returns the first 12 characters of the full hash.
func TemplateHashShort() (string, error) {
	hash, err := ComputeTemplateHash()
	if err != nil {
		return "", err
	}
	return hash[:12], nil
}
