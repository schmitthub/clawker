package builders

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigBuilder_NewConfigBuilder(t *testing.T) {
	builder := NewConfigBuilder()
	cfg := builder.Build()

	assert.Equal(t, "1", cfg.Version)
	assert.Empty(t, cfg.Project)
	assert.Equal(t, "/workspace", cfg.Workspace.RemotePath)
	assert.Equal(t, "bind", cfg.Workspace.DefaultMode)
}

func TestConfigBuilder_Fluent(t *testing.T) {
	tests := []struct {
		name   string
		build  func() *config.Project
		verify func(t *testing.T, cfg *config.Project)
	}{
		{
			name: "WithProject",
			build: func() *config.Project {
				return NewConfigBuilder().
					WithProject("my-project").
					Build()
			},
			verify: func(t *testing.T, cfg *config.Project) {
				assert.Equal(t, "my-project", cfg.Project)
			},
		},
		{
			name: "WithVersion",
			build: func() *config.Project {
				return NewConfigBuilder().
					WithVersion("2").
					Build()
			},
			verify: func(t *testing.T, cfg *config.Project) {
				assert.Equal(t, "2", cfg.Version)
			},
		},
		{
			name: "WithDefaultImage",
			build: func() *config.Project {
				return NewConfigBuilder().
					WithDefaultImage("clawker-custom:v1").
					Build()
			},
			verify: func(t *testing.T, cfg *config.Project) {
				assert.Equal(t, "clawker-custom:v1", cfg.DefaultImage)
			},
		},
		{
			name: "WithBuild",
			build: func() *config.Project {
				return NewConfigBuilder().
					WithBuild(config.BuildConfig{
						Image:    "node:20-slim",
						Packages: []string{"git", "curl"},
					}).
					Build()
			},
			verify: func(t *testing.T, cfg *config.Project) {
				assert.Equal(t, "node:20-slim", cfg.Build.Image)
				assert.Equal(t, []string{"git", "curl"}, cfg.Build.Packages)
			},
		},
		{
			name: "WithAgent",
			build: func() *config.Project {
				return NewConfigBuilder().
					WithAgent(config.AgentConfig{
						Env:      map[string]string{"FOO": "bar"},
						Includes: []string{"./README.md"},
					}).
					Build()
			},
			verify: func(t *testing.T, cfg *config.Project) {
				assert.Equal(t, "bar", cfg.Agent.Env["FOO"])
				assert.Equal(t, []string{"./README.md"}, cfg.Agent.Includes)
			},
		},
		{
			name: "WithWorkspace",
			build: func() *config.Project {
				return NewConfigBuilder().
					WithWorkspace(config.WorkspaceConfig{
						RemotePath:  "/app",
						DefaultMode: "snapshot",
					}).
					Build()
			},
			verify: func(t *testing.T, cfg *config.Project) {
				assert.Equal(t, "/app", cfg.Workspace.RemotePath)
				assert.Equal(t, "snapshot", cfg.Workspace.DefaultMode)
			},
		},
		{
			name: "WithSecurity",
			build: func() *config.Project {
				return NewConfigBuilder().
					WithSecurity(config.SecurityConfig{
						DockerSocket: true,
						Firewall: &config.FirewallConfig{
							Enable: true,
						},
					}).
					Build()
			},
			verify: func(t *testing.T, cfg *config.Project) {
				assert.True(t, cfg.Security.DockerSocket)
				require.NotNil(t, cfg.Security.Firewall)
				assert.True(t, cfg.Security.Firewall.Enable)
			},
		},
		{
			name: "Chained fluent calls",
			build: func() *config.Project {
				return NewConfigBuilder().
					WithProject("chained").
					WithDefaultImage("test:latest").
					WithBuild(DefaultBuild()).
					WithSecurity(SecurityFirewallEnabled()).
					Build()
			},
			verify: func(t *testing.T, cfg *config.Project) {
				assert.Equal(t, "chained", cfg.Project)
				assert.Equal(t, "test:latest", cfg.DefaultImage)
				assert.Equal(t, "buildpack-deps:bookworm-scm", cfg.Build.Image)
				require.NotNil(t, cfg.Security.Firewall)
				assert.True(t, cfg.Security.Firewall.Enable)
			},
		},
		{
			name: "ForTestBaseImage",
			build: func() *config.Project {
				return NewConfigBuilder().
					WithProject("test").
					WithBuild(config.BuildConfig{
						Image:    "buildpack-deps:bookworm-scm",
						Packages: []string{"git", "curl", "ripgrep"},
					}).
					ForTestBaseImage().
					Build()
			},
			verify: func(t *testing.T, cfg *config.Project) {
				assert.Equal(t, "alpine:latest", cfg.Build.Image)
				assert.Nil(t, cfg.Build.Packages)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.build()
			tt.verify(t, cfg)
		})
	}
}

func TestConfigBuilder_Immutability(t *testing.T) {
	// Build should return a copy, not a reference to internal state
	builder := NewConfigBuilder().WithProject("original")
	cfg1 := builder.Build()
	cfg1.Project = "modified"
	cfg2 := builder.Build()

	assert.Equal(t, "original", cfg2.Project, "modifying returned config should not affect builder")
}

func TestMinimalValidConfig(t *testing.T) {
	cfg := MinimalValidConfig().Build()

	assert.Equal(t, "1", cfg.Version)
	assert.Equal(t, "test-project", cfg.Project)
	assert.Equal(t, "buildpack-deps:bookworm-scm", cfg.Build.Image)
}

func TestFullFeaturedConfig(t *testing.T) {
	cfg := FullFeaturedConfig().Build()

	// Basic fields
	assert.Equal(t, "1", cfg.Version)
	assert.Equal(t, "test-project", cfg.Project)
	assert.Equal(t, "clawker-test:latest", cfg.DefaultImage)

	// Build
	assert.Equal(t, "buildpack-deps:bookworm-scm", cfg.Build.Image)
	assert.Contains(t, cfg.Build.Packages, "git")
	require.NotNil(t, cfg.Build.Instructions)
	assert.Equal(t, "development", cfg.Build.Instructions.Env["NODE_ENV"])

	// Agent
	assert.Equal(t, "true", cfg.Agent.Env["CLAUDE_TEST"])

	// Workspace
	assert.Equal(t, "/workspace", cfg.Workspace.RemotePath)
	assert.Equal(t, "bind", cfg.Workspace.DefaultMode)

	// Security
	require.NotNil(t, cfg.Security.Firewall)
	assert.True(t, cfg.Security.Firewall.Enable)
	assert.False(t, cfg.Security.DockerSocket)
	require.NotNil(t, cfg.Security.EnableHostProxy)
	assert.True(t, *cfg.Security.EnableHostProxy)
	require.NotNil(t, cfg.Security.GitCredentials)
	assert.True(t, *cfg.Security.GitCredentials.ForwardHTTPS)
	assert.True(t, *cfg.Security.GitCredentials.ForwardSSH)
	assert.True(t, *cfg.Security.GitCredentials.CopyGitConfig)
}

func TestBuildConfigPresets(t *testing.T) {
	tests := []struct {
		name   string
		preset config.BuildConfig
		verify func(t *testing.T, cfg config.BuildConfig)
	}{
		{
			name:   "DefaultBuild",
			preset: DefaultBuild(),
			verify: func(t *testing.T, cfg config.BuildConfig) {
				assert.Equal(t, "buildpack-deps:bookworm-scm", cfg.Image)
				assert.Contains(t, cfg.Packages, "git")
				assert.Contains(t, cfg.Packages, "curl")
			},
		},
		{
			name:   "AlpineBuild",
			preset: AlpineBuild(),
			verify: func(t *testing.T, cfg config.BuildConfig) {
				assert.Equal(t, "alpine:latest", cfg.Image)
				assert.Nil(t, cfg.Packages)
			},
		},
		{
			name:   "BuildWithPackages",
			preset: BuildWithPackages("ubuntu:22.04", "vim", "htop"),
			verify: func(t *testing.T, cfg config.BuildConfig) {
				assert.Equal(t, "ubuntu:22.04", cfg.Image)
				assert.Equal(t, []string{"vim", "htop"}, cfg.Packages)
			},
		},
		{
			name: "BuildWithInstructions",
			preset: BuildWithInstructions("node:20", &config.DockerInstructions{
				Env: map[string]string{"NODE_ENV": "test"},
			}),
			verify: func(t *testing.T, cfg config.BuildConfig) {
				assert.Equal(t, "node:20", cfg.Image)
				require.NotNil(t, cfg.Instructions)
				assert.Equal(t, "test", cfg.Instructions.Env["NODE_ENV"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.verify(t, tt.preset)
		})
	}
}

func TestSecurityConfigPresets(t *testing.T) {
	tests := []struct {
		name   string
		preset config.SecurityConfig
		verify func(t *testing.T, cfg config.SecurityConfig)
	}{
		{
			name:   "SecurityFirewallEnabled",
			preset: SecurityFirewallEnabled(),
			verify: func(t *testing.T, cfg config.SecurityConfig) {
				require.NotNil(t, cfg.Firewall)
				assert.True(t, cfg.Firewall.Enable)
				assert.False(t, cfg.DockerSocket)
			},
		},
		{
			name:   "SecurityFirewallDisabled",
			preset: SecurityFirewallDisabled(),
			verify: func(t *testing.T, cfg config.SecurityConfig) {
				require.NotNil(t, cfg.Firewall)
				assert.False(t, cfg.Firewall.Enable)
				assert.False(t, cfg.DockerSocket)
				require.NotNil(t, cfg.EnableHostProxy)
				assert.False(t, *cfg.EnableHostProxy)
			},
		},
		{
			name:   "SecurityWithDockerSocket",
			preset: SecurityWithDockerSocket(),
			verify: func(t *testing.T, cfg config.SecurityConfig) {
				assert.True(t, cfg.DockerSocket)
			},
		},
		{
			name:   "SecurityWithFirewallDomains",
			preset: SecurityWithFirewallDomains([]string{"api.openai.com"}),
			verify: func(t *testing.T, cfg config.SecurityConfig) {
				require.NotNil(t, cfg.Firewall)
				assert.True(t, cfg.Firewall.Enable)
				assert.Equal(t, []string{"api.openai.com"}, cfg.Firewall.AddDomains)
			},
		},
		{
			name:   "SecurityWithGitCredentials",
			preset: SecurityWithGitCredentials(true, false, true),
			verify: func(t *testing.T, cfg config.SecurityConfig) {
				require.NotNil(t, cfg.GitCredentials)
				assert.True(t, *cfg.GitCredentials.ForwardHTTPS)
				assert.False(t, *cfg.GitCredentials.ForwardSSH)
				assert.True(t, *cfg.GitCredentials.CopyGitConfig)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.verify(t, tt.preset)
		})
	}
}

func TestAgentConfigPresets(t *testing.T) {
	tests := []struct {
		name   string
		preset config.AgentConfig
		verify func(t *testing.T, cfg config.AgentConfig)
	}{
		{
			name:   "DefaultAgent",
			preset: DefaultAgent(),
			verify: func(t *testing.T, cfg config.AgentConfig) {
				assert.Empty(t, cfg.Env)
				assert.Empty(t, cfg.Includes)
			},
		},
		{
			name:   "AgentWithEnv",
			preset: AgentWithEnv(map[string]string{"KEY": "value"}),
			verify: func(t *testing.T, cfg config.AgentConfig) {
				assert.Equal(t, "value", cfg.Env["KEY"])
			},
		},
		{
			name:   "AgentWithIncludes",
			preset: AgentWithIncludes("./README.md", "./docs/ARCH.md"),
			verify: func(t *testing.T, cfg config.AgentConfig) {
				assert.Equal(t, []string{"./README.md", "./docs/ARCH.md"}, cfg.Includes)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.verify(t, tt.preset)
		})
	}
}

func TestWorkspaceConfigPresets(t *testing.T) {
	tests := []struct {
		name   string
		preset config.WorkspaceConfig
		verify func(t *testing.T, cfg config.WorkspaceConfig)
	}{
		{
			name:   "DefaultWorkspace",
			preset: DefaultWorkspace(),
			verify: func(t *testing.T, cfg config.WorkspaceConfig) {
				assert.Equal(t, "/workspace", cfg.RemotePath)
				assert.Equal(t, "bind", cfg.DefaultMode)
			},
		},
		{
			name:   "WorkspaceSnapshot",
			preset: WorkspaceSnapshot(),
			verify: func(t *testing.T, cfg config.WorkspaceConfig) {
				assert.Equal(t, "/workspace", cfg.RemotePath)
				assert.Equal(t, "snapshot", cfg.DefaultMode)
			},
		},
		{
			name:   "WorkspaceWithPath",
			preset: WorkspaceWithPath("/app"),
			verify: func(t *testing.T, cfg config.WorkspaceConfig) {
				assert.Equal(t, "/app", cfg.RemotePath)
				assert.Equal(t, "bind", cfg.DefaultMode)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.verify(t, tt.preset)
		})
	}
}

func TestInstructionPresets(t *testing.T) {
	t.Run("InstructionsWithEnv", func(t *testing.T) {
		inst := InstructionsWithEnv(map[string]string{"FOO": "bar"})
		assert.Equal(t, "bar", inst.Env["FOO"])
	})

	t.Run("InstructionsWithRootRun", func(t *testing.T) {
		inst := InstructionsWithRootRun("mkdir -p /data", "chmod 777 /data")
		require.Len(t, inst.RootRun, 2)
		assert.Equal(t, "mkdir -p /data", inst.RootRun[0].Cmd)
		assert.Equal(t, "chmod 777 /data", inst.RootRun[1].Cmd)
	})

	t.Run("InstructionsWithUserRun", func(t *testing.T) {
		inst := InstructionsWithUserRun("npm install")
		require.Len(t, inst.UserRun, 1)
		assert.Equal(t, "npm install", inst.UserRun[0].Cmd)
	})

	t.Run("InstructionsWithCopy", func(t *testing.T) {
		inst := InstructionsWithCopy(
			config.CopyInstruction{Src: "./config.json", Dest: "/app/"},
			config.CopyInstruction{Src: "./scripts/", Dest: "/scripts/"},
		)
		require.Len(t, inst.Copy, 2)
		assert.Equal(t, "./config.json", inst.Copy[0].Src)
		assert.Equal(t, "/app/", inst.Copy[0].Dest)
	})
}
