package bundler

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
)

func TestConfigFile_ValidJSON(t *testing.T) {
	src, err := bundle.FloorFS(bundle.ComponentHarness, "claude")
	require.NoError(t, err)
	raw, err := fs.ReadFile(src, "assets/claude-config.json")
	require.NoError(t, err)

	var content map[string]any
	require.NoError(t, json.Unmarshal(raw, &content),
		"claude-config.json must be valid JSON")

	val, ok := content["hasCompletedOnboarding"]
	require.True(t, ok, "ConfigFile must contain hasCompletedOnboarding key")
	require.Equal(t, true, val, "hasCompletedOnboarding must be true")
}

func TestWriteHarnessBuildContextToDir(t *testing.T) {
	cfg := testConfig(t, `
version: "1"
build:
security:
  firewall:
    enable: true
`)
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "project-source.go"), []byte("package main"), 0o644))
	gen := NewProjectGenerator(cfg, workDir)
	gen.BuildKitEnabled = true

	dockerfile := []byte("FROM alpine:latest\nRUN echo hello\n")
	dir := t.TempDir()

	err := gen.WriteHarnessBuildContextToDir(dir, dockerfile)
	require.NoError(t, err)

	// Verify Dockerfile was written
	content, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	require.NoError(t, err)
	assert.Equal(t, dockerfile, content)

	// Verify all expected scripts exist.
	expectedFiles := []string{
		"clawkerd",
		"assets/statusline.sh",
		"assets/claude-settings.json",
		"assets/claude-config.json",
		"clawker-agent-prompt.md",
		"host-open.sh",
		"callback-forwarder.go",
		"git-credential-clawker.sh",
		"clawker-socket-server.go",
	}
	for _, name := range expectedFiles {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.NoError(t, err, "expected file %s to exist", name)
	}

	// Verify scripts are executable
	for _, name := range []string{"clawkerd", "host-open.sh"} {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.NotZero(t, info.Mode()&0o111, "%s should be executable", name)
	}

	// Project files stay out of the harness context — user copy sources
	// belong to the base image's context (the project build-context dir).
	_, err = os.Stat(filepath.Join(dir, "project-source.go"))
	assert.True(t, os.IsNotExist(err),
		"harness build context must not absorb project files")
}

func TestGenerateBaseBuildContext(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "app.go"), []byte("package main"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "Dockerfile"), []byte("FROM user-owned"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".git", "HEAD"), []byte("ref"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(workDir, "app.go"), filepath.Join(workDir, "link.go")))

	gen := NewProjectGenerator(cfg, workDir)
	rendered := []byte("FROM buildpack-deps:bookworm-scm\n")

	reader, err := gen.GenerateBaseBuildContext(rendered)
	require.NoError(t, err)

	entries := map[string][]byte{}
	tr := tar.NewReader(reader)
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		require.NoError(t, nextErr)
		content, readErr := io.ReadAll(tr)
		require.NoError(t, readErr)
		entries[hdr.Name] = content
	}

	assert.Equal(t, rendered, entries[BaseDockerfileName],
		"rendered base Dockerfile must land under the reserved name")
	assert.Equal(t, []byte("FROM user-owned"), entries["Dockerfile"],
		"a user's own Dockerfile in the context must ride along untouched")
	assert.Contains(t, entries, "app.go", "project files must be staged for user copy instructions")
	assert.NotContains(t, entries, ".git/HEAD", ".git must be skipped")
	assert.NotContains(t, entries, "link.go", "symlinks must be skipped")
}

// TestWriteBuildContextToDir_NoFirewall deleted — firewall.sh replaced by eBPF.
