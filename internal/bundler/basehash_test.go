package bundler //nolint:testpackage // shares in-package test helpers (testConfig, newTestProjectGenerator)

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func copyInstructionsYAML() string {
	return `
version: "1"
build:
  instructions:
    copy:
      - src: "scripts"
        dst: "/opt/scripts"
      - src: "config-*.yaml"
        dst: "/opt/config/"
`
}

func writeCopyFixtures(t *testing.T, workDir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "scripts", "run.sh"), []byte("echo run"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "config-a.yaml"), []byte("a: 1"), 0o644))
}

func TestBaseContentHash_Deterministic(t *testing.T) {
	workDir := t.TempDir()
	writeCopyFixtures(t, workDir)
	gen := newTestProjectGenerator(testConfig(t, copyInstructionsYAML()), workDir)

	df := []byte("FROM x\n")
	h1, err := gen.BaseContentHash(df)
	require.NoError(t, err)
	h2, err := gen.BaseContentHash(df)
	require.NoError(t, err)
	assert.Equal(t, h1, h2)
	assert.Len(t, h1, 64, "hex-encoded sha256")
}

func TestBaseContentHash_ChangesOnDockerfileChange(t *testing.T) {
	workDir := t.TempDir()
	gen := newTestProjectGenerator(testConfig(t, minimalProjectYAML()), workDir)

	h1, err := gen.BaseContentHash([]byte("FROM x\n"))
	require.NoError(t, err)
	h2, err := gen.BaseContentHash([]byte("FROM y\n"))
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2)
}

func TestBaseContentHash_ChangesOnCopySourceChange(t *testing.T) {
	workDir := t.TempDir()
	writeCopyFixtures(t, workDir)
	gen := newTestProjectGenerator(testConfig(t, copyInstructionsYAML()), workDir)

	df := []byte("FROM x\n")
	h1, err := gen.BaseContentHash(df)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(workDir, "scripts", "run.sh"), []byte("echo changed"), 0o755))
	h2, err := gen.BaseContentHash(df)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "copy-src content change must flip the hash")

	// Glob-matched file too.
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "config-b.yaml"), []byte("b: 2"), 0o644))
	h3, err := gen.BaseContentHash(df)
	require.NoError(t, err)
	assert.NotEqual(t, h2, h3, "new glob match must flip the hash")
}

func TestBaseContentHash_IgnoresUnreferencedFiles(t *testing.T) {
	workDir := t.TempDir()
	writeCopyFixtures(t, workDir)
	gen := newTestProjectGenerator(testConfig(t, copyInstructionsYAML()), workDir)

	df := []byte("FROM x\n")
	h1, err := gen.BaseContentHash(df)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main"), 0o644))
	h2, err := gen.BaseContentHash(df)
	require.NoError(t, err)
	assert.Equal(t, h1, h2,
		"source edits outside copy srcs must not rebuild the base")
}

func TestBaseContentHash_MissingSrcStableMarker(t *testing.T) {
	workDir := t.TempDir() // fixtures NOT written — srcs missing
	gen := newTestProjectGenerator(testConfig(t, copyInstructionsYAML()), workDir)

	df := []byte("FROM x\n")
	h1, err := gen.BaseContentHash(df)
	require.NoError(t, err)
	h2, err := gen.BaseContentHash(df)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "missing srcs hash a stable marker, no error")

	// The src appearing later must flip the hash.
	writeCopyFixtures(t, workDir)
	h3, err := gen.BaseContentHash(df)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h3)
}
