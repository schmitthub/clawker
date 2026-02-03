package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeTemplateHash(t *testing.T) {
	hash, err := ComputeTemplateHash()
	require.NoError(t, err)

	// Hash should be 64 hex characters (SHA256)
	assert.Len(t, hash, 64, "hash should be 64 hex characters")

	// Hash should be stable (same result on repeated calls)
	hash2, err := ComputeTemplateHash()
	require.NoError(t, err)
	assert.Equal(t, hash, hash2, "hash should be deterministic")
}

func TestComputeTemplateHashFromDir(t *testing.T) {
	// Find project root for testing
	rootDir, err := FindProjectRoot()
	require.NoError(t, err)

	hash, err := ComputeTemplateHashFromDir(rootDir)
	require.NoError(t, err)

	// Hash should be 64 hex characters (SHA256)
	assert.Len(t, hash, 64, "hash should be 64 hex characters")

	// Should match the auto-detected version
	autoHash, err := ComputeTemplateHash()
	require.NoError(t, err)
	assert.Equal(t, autoHash, hash, "explicit root should match auto-detected")
}

func TestComputeTemplateHashFromDir_InvalidDir(t *testing.T) {
	_, err := ComputeTemplateHashFromDir("/nonexistent/path")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to hash templates directory")
}

func TestTemplateHashShort(t *testing.T) {
	short, err := TemplateHashShort()
	require.NoError(t, err)

	// Short hash should be 12 characters
	assert.Len(t, short, 12, "short hash should be 12 characters")

	// Should be prefix of full hash
	full, err := ComputeTemplateHash()
	require.NoError(t, err)
	assert.True(t, len(full) >= 12, "full hash should be at least 12 characters")
	assert.Equal(t, full[:12], short, "short hash should be prefix of full hash")
}

func TestHashChangesWhenFileChanges(t *testing.T) {
	// Create a temp directory with mock templates
	tmpDir := t.TempDir()
	templatesDir := filepath.Join(tmpDir, "internal", "bundler", "assets")
	internalsDir := filepath.Join(tmpDir, "internal", "hostproxy", "internals")
	require.NoError(t, os.MkdirAll(templatesDir, 0755))
	require.NoError(t, os.MkdirAll(internalsDir, 0755))

	// Create mock files
	require.NoError(t, os.WriteFile(filepath.Join(templatesDir, "test.tmpl"), []byte("original"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(internalsDir, "host-open.sh"), []byte("#!/bin/bash"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "internal", "bundler", "dockerfile.go"), []byte("type Foo struct{}"), 0644))

	// Get initial hash
	hash1, err := ComputeTemplateHashFromDir(tmpDir)
	require.NoError(t, err)

	// Modify the template file
	require.NoError(t, os.WriteFile(filepath.Join(templatesDir, "test.tmpl"), []byte("modified"), 0644))

	// Hash should change
	hash2, err := ComputeTemplateHashFromDir(tmpDir)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hash2, "hash should change when template content changes")
}

func TestHashChangesWhenDockerfileGoChanges(t *testing.T) {
	// Create a temp directory with mock templates
	tmpDir := t.TempDir()
	templatesDir := filepath.Join(tmpDir, "internal", "bundler", "assets")
	internalsDir := filepath.Join(tmpDir, "internal", "hostproxy", "internals")
	require.NoError(t, os.MkdirAll(templatesDir, 0755))
	require.NoError(t, os.MkdirAll(internalsDir, 0755))

	// Create mock files
	require.NoError(t, os.WriteFile(filepath.Join(templatesDir, "test.tmpl"), []byte("template"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(internalsDir, "host-open.sh"), []byte("#!/bin/bash"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "internal", "bundler", "dockerfile.go"), []byte("type Foo struct{}"), 0644))

	// Get initial hash
	hash1, err := ComputeTemplateHashFromDir(tmpDir)
	require.NoError(t, err)

	// Modify the dockerfile.go file
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "internal", "bundler", "dockerfile.go"), []byte("type Foo struct { NewField string }"), 0644))

	// Hash should change
	hash2, err := ComputeTemplateHashFromDir(tmpDir)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hash2, "hash should change when dockerfile.go changes")
}

func TestHashStableAcrossFileOrder(t *testing.T) {
	// Create a temp directory with mock templates
	tmpDir := t.TempDir()
	templatesDir := filepath.Join(tmpDir, "internal", "bundler", "assets")
	internalsDir := filepath.Join(tmpDir, "internal", "hostproxy", "internals")
	require.NoError(t, os.MkdirAll(templatesDir, 0755))
	require.NoError(t, os.MkdirAll(internalsDir, 0755))

	// Create multiple mock files
	require.NoError(t, os.WriteFile(filepath.Join(templatesDir, "a.tmpl"), []byte("content a"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(templatesDir, "b.tmpl"), []byte("content b"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(templatesDir, "c.tmpl"), []byte("content c"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(internalsDir, "host-open.sh"), []byte("#!/bin/bash"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "internal", "bundler", "dockerfile.go"), []byte("struct def"), 0644))

	// Hash should be deterministic regardless of internal ordering
	hash1, err := ComputeTemplateHashFromDir(tmpDir)
	require.NoError(t, err)

	hash2, err := ComputeTemplateHashFromDir(tmpDir)
	require.NoError(t, err)

	assert.Equal(t, hash1, hash2, "hash should be deterministic")
}

func TestFindProjectRoot(t *testing.T) {
	root, err := FindProjectRoot()
	require.NoError(t, err)

	// Should find go.mod in the root
	goMod := filepath.Join(root, "go.mod")
	_, err = os.Stat(goMod)
	require.NoError(t, err, "go.mod should exist at project root")

	// Should also have internal/build/templates
	templatesDir := filepath.Join(root, "internal", "bundler", "assets")
	stat, err := os.Stat(templatesDir)
	require.NoError(t, err, "templates directory should exist")
	require.True(t, stat.IsDir(), "templates should be a directory")
}
