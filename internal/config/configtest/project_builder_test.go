package configtest

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectBuilder_Defaults(t *testing.T) {
	cfg := NewProjectBuilder().Build()

	assert.Equal(t, "1", cfg.Version)
	assert.Empty(t, cfg.Project)
	assert.Equal(t, "/workspace", cfg.Workspace.RemotePath)
	assert.Equal(t, "bind", cfg.Workspace.DefaultMode)
}

func TestProjectBuilder_Fluent(t *testing.T) {
	loopCfg := &config.LoopConfig{}

	cfg := NewProjectBuilder().
		WithVersion("2").
		WithProject("my-project").
		WithDefaultImage("custom:v1").
		WithBuild(config.BuildConfig{
			Image:    "node:20-slim",
			Packages: []string{"git"},
		}).
		WithAgent(config.AgentConfig{
			Env: map[string]string{"KEY": "val"},
		}).
		WithWorkspace(config.WorkspaceConfig{
			RemotePath:  "/app",
			DefaultMode: "snapshot",
		}).
		WithSecurity(config.SecurityConfig{
			DockerSocket: true,
		}).
		WithLoop(loopCfg).
		Build()

	assert.Equal(t, "2", cfg.Version)
	assert.Equal(t, "my-project", cfg.Project)
	assert.Equal(t, "custom:v1", cfg.DefaultImage)
	assert.Equal(t, "node:20-slim", cfg.Build.Image)
	assert.Equal(t, []string{"git"}, cfg.Build.Packages)
	assert.Equal(t, "val", cfg.Agent.Env["KEY"])
	assert.Equal(t, "/app", cfg.Workspace.RemotePath)
	assert.Equal(t, "snapshot", cfg.Workspace.DefaultMode)
	assert.True(t, cfg.Security.DockerSocket)
	require.NotNil(t, cfg.Loop)
}

func TestProjectBuilder_Immutability(t *testing.T) {
	builder := NewProjectBuilder().WithProject("original")
	cfg1 := builder.Build()
	cfg1.Project = "modified"
	cfg2 := builder.Build()

	assert.Equal(t, "original", cfg2.Project, "modifying returned config should not affect builder")
}

func TestProjectBuilder_ForTestBaseImage(t *testing.T) {
	cfg := NewProjectBuilder().
		WithBuild(config.BuildConfig{
			Image:    "buildpack-deps:bookworm-scm",
			Packages: []string{"git", "curl"},
		}).
		ForTestBaseImage().
		Build()

	assert.Equal(t, "alpine:latest", cfg.Build.Image)
	assert.Nil(t, cfg.Build.Packages)
}
