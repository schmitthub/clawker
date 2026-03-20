package bundler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigFile_ValidJSON(t *testing.T) {
	var content map[string]any
	require.NoError(t, json.Unmarshal([]byte(ConfigFile), &content),
		"ConfigFile must be valid JSON")

	val, ok := content["hasCompletedOnboarding"]
	require.True(t, ok, "ConfigFile must contain hasCompletedOnboarding key")
	require.Equal(t, true, val, "hasCompletedOnboarding must be true")
}

func TestWriteBuildContextToDir(t *testing.T) {
	cfg := testConfig(t, `
version: "1"
build:
  image: "buildpack-deps:bookworm-scm"
security:
  firewall:
    enable: true
`)
	gen := NewProjectGenerator(cfg, t.TempDir())
	gen.BuildKitEnabled = true

	dockerfile := []byte("FROM alpine:latest\nRUN echo hello\n")
	dir := t.TempDir()

	err := gen.WriteBuildContextToDir(dir, dockerfile)
	require.NoError(t, err)

	// Verify Dockerfile was written
	content, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	require.NoError(t, err)
	assert.Equal(t, dockerfile, content)

	// Verify all expected scripts exist
	expectedFiles := []string{
		"entrypoint.sh",
		"statusline.sh",
		"claude-settings.json",
		"claude-config.json",
		"host-open.sh",
		"callback-forwarder.go",
		"git-credential-clawker.sh",
		"clawker-socket-server.go",
		"firewall.sh", // firewall script (always included)
	}
	for _, name := range expectedFiles {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.NoError(t, err, "expected file %s to exist", name)
	}

	// Verify scripts are executable
	for _, name := range []string{"entrypoint.sh", "host-open.sh", "firewall.sh"} {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.NotZero(t, info.Mode()&0111, "%s should be executable", name)
	}
}

func TestWriteBuildContextToDir_NoFirewall(t *testing.T) {
	// Firewall script is always included regardless of config — execution is gated at runtime.
	cfg := testConfig(t, `
version: "1"
build:
  image: "buildpack-deps:bookworm-scm"
security:
  firewall:
    enable: false
`)
	gen := NewProjectGenerator(cfg, t.TempDir())
	dir := t.TempDir()

	err := gen.WriteBuildContextToDir(dir, []byte("FROM alpine\n"))
	require.NoError(t, err)

	// Firewall script should always be written (runtime-gated, not build-gated)
	info, err := os.Stat(filepath.Join(dir, "firewall.sh"))
	require.NoError(t, err, "firewall.sh should always exist in build context")
	assert.NotZero(t, info.Mode()&0111, "firewall.sh should be executable")
}

func TestWriteBuildContextToDir_WithIncludes(t *testing.T) {
	workDir := t.TempDir()

	// Create an include file in workDir
	includeContent := []byte("# my include file\n")
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "CLAUDE.md"), includeContent, 0644))

	cfg := testConfig(t, `
version: "1"
build:
  image: "buildpack-deps:bookworm-scm"
agent:
  includes:
    - "CLAUDE.md"
`)
	gen := NewProjectGenerator(cfg, workDir)
	dir := t.TempDir()

	err := gen.WriteBuildContextToDir(dir, []byte("FROM alpine\n"))
	require.NoError(t, err)

	// Verify include file was copied
	content, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	require.NoError(t, err)
	assert.Equal(t, includeContent, content)
}
