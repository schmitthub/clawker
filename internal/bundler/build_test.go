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
	}
	for _, name := range expectedFiles {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.NoError(t, err, "expected file %s to exist", name)
	}

	// Verify scripts are executable
	for _, name := range []string{"entrypoint.sh", "host-open.sh"} {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.NotZero(t, info.Mode()&0111, "%s should be executable", name)
	}
}

// TestWriteBuildContextToDir_NoFirewall deleted — firewall.sh replaced by eBPF.
