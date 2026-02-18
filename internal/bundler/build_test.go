package bundler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteBuildContextToDir(t *testing.T) {
	cfg := &config.Config{Project: &config.Project{
		Build: config.BuildConfig{
			Image: "buildpack-deps:bookworm-scm",
		},
		Workspace: config.WorkspaceConfig{
			RemotePath: "/workspace",
		},
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{Enable: true},
		},
	}}

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
		"host-open.sh",
		"callback-forwarder.go",
		"git-credential-clawker.sh",
		"clawker-socket-server.go",
		"init-firewall.sh", // firewall enabled
	}
	for _, name := range expectedFiles {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.NoError(t, err, "expected file %s to exist", name)
	}

	// Verify scripts are executable
	for _, name := range []string{"entrypoint.sh", "host-open.sh", "init-firewall.sh"} {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.NotZero(t, info.Mode()&0111, "%s should be executable", name)
	}
}

func TestWriteBuildContextToDir_NoFirewall(t *testing.T) {
	// Firewall script is always included regardless of config — execution is gated at runtime.
	cfg := &config.Config{Project: &config.Project{
		Build: config.BuildConfig{
			Image: "buildpack-deps:bookworm-scm",
		},
		Workspace: config.WorkspaceConfig{
			RemotePath: "/workspace",
		},
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{Enable: false},
		},
	}}

	gen := NewProjectGenerator(cfg, t.TempDir())
	dir := t.TempDir()

	err := gen.WriteBuildContextToDir(dir, []byte("FROM alpine\n"))
	require.NoError(t, err)

	// Firewall script should always be written (runtime-gated, not build-gated)
	info, err := os.Stat(filepath.Join(dir, "init-firewall.sh"))
	require.NoError(t, err, "init-firewall.sh should always exist in build context")
	assert.NotZero(t, info.Mode()&0111, "init-firewall.sh should be executable")
}

func TestWriteBuildContextToDir_WithIncludes(t *testing.T) {
	workDir := t.TempDir()

	// Create an include file in workDir
	includeContent := []byte("# my include file\n")
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "CLAUDE.md"), includeContent, 0644))

	cfg := &config.Config{Project: &config.Project{
		Build: config.BuildConfig{
			Image: "buildpack-deps:bookworm-scm",
		},
		Workspace: config.WorkspaceConfig{
			RemotePath: "/workspace",
		},
		Agent: config.AgentConfig{
			Includes: []string{"CLAUDE.md"},
		},
	}}

	gen := NewProjectGenerator(cfg, workDir)
	dir := t.TempDir()

	err := gen.WriteBuildContextToDir(dir, []byte("FROM alpine\n"))
	require.NoError(t, err)

	// Verify include file was copied
	content, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	require.NoError(t, err)
	assert.Equal(t, includeContent, content)
}
