// Package testutil provides shared test utilities for clawker tests.
package builders

import (
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/config/configtest"
)

// ConfigBuilder provides a fluent API for constructing config.Project objects in tests.
// It delegates to configtest.ProjectBuilder which stores a pointer internally,
// avoiding value copies of config.Project (which contains sync.RWMutex).
type ConfigBuilder struct {
	inner *configtest.ProjectBuilder
}

// NewConfigBuilder creates a new ConfigBuilder with sensible defaults.
func NewConfigBuilder() *ConfigBuilder {
	return &ConfigBuilder{
		inner: configtest.NewProjectBuilder(),
	}
}

// WithVersion sets the config version.
func (b *ConfigBuilder) WithVersion(version string) *ConfigBuilder {
	b.inner.WithVersion(version)
	return b
}

// WithProject sets the project name.
func (b *ConfigBuilder) WithProject(name string) *ConfigBuilder {
	b.inner.WithProject(name)
	return b
}

// WithDefaultImage sets the default image.
func (b *ConfigBuilder) WithDefaultImage(image string) *ConfigBuilder {
	b.inner.WithDefaultImage(image)
	return b
}

// WithBuild sets the build configuration.
func (b *ConfigBuilder) WithBuild(build config.BuildConfig) *ConfigBuilder {
	b.inner.WithBuild(build)
	return b
}

// WithAgent sets the agent configuration.
func (b *ConfigBuilder) WithAgent(agent config.AgentConfig) *ConfigBuilder {
	b.inner.WithAgent(agent)
	return b
}

// WithWorkspace sets the workspace configuration.
func (b *ConfigBuilder) WithWorkspace(workspace config.WorkspaceConfig) *ConfigBuilder {
	b.inner.WithWorkspace(workspace)
	return b
}

// WithSecurity sets the security configuration.
func (b *ConfigBuilder) WithSecurity(security config.SecurityConfig) *ConfigBuilder {
	b.inner.WithSecurity(security)
	return b
}

// Build returns the constructed Config.
func (b *ConfigBuilder) Build() *config.Project {
	return b.inner.Build()
}

// ForTestBaseImage modifies the build config to use a fast test base image.
// This swaps the image to alpine:latest and clears packages for fast builds.
func (b *ConfigBuilder) ForTestBaseImage() *ConfigBuilder {
	b.inner.ForTestBaseImage()
	return b
}

// ----------------------------------------------------------------
// Preset Configurations
// ----------------------------------------------------------------

// MinimalValidConfig returns a ConfigBuilder with the bare minimum for a valid config.
func MinimalValidConfig() *ConfigBuilder {
	return NewConfigBuilder().
		WithProject("test-project").
		WithBuild(config.BuildConfig{
			Image: "buildpack-deps:bookworm-scm",
		})
}

// FullFeaturedConfig returns a ConfigBuilder with all features enabled.
func FullFeaturedConfig() *ConfigBuilder {
	firewallEnabled := true
	hostProxyEnabled := true
	gitHTTPS := true
	gitSSH := true
	copyGitConfig := true

	return NewConfigBuilder().
		WithProject("test-project").
		WithDefaultImage("clawker-test:latest").
		WithBuild(config.BuildConfig{
			Image:    "buildpack-deps:bookworm-scm",
			Packages: []string{"git", "curl", "ripgrep"},
			Instructions: &config.DockerInstructions{
				Env: map[string]string{
					"NODE_ENV": "development",
				},
			},
		}).
		WithAgent(config.AgentConfig{
			Env: map[string]string{
				"CLAUDE_TEST": "true",
			},
		}).
		WithWorkspace(config.WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "bind",
		}).
		WithSecurity(config.SecurityConfig{
			Firewall: &config.FirewallConfig{
				Enable: firewallEnabled,
			},
			DockerSocket:    false,
			EnableHostProxy: &hostProxyEnabled,
			GitCredentials: &config.GitCredentialsConfig{
				ForwardHTTPS:  &gitHTTPS,
				ForwardSSH:    &gitSSH,
				CopyGitConfig: &copyGitConfig,
			},
		})
}

// ----------------------------------------------------------------
// Build Config Presets
// ----------------------------------------------------------------

// DefaultBuild returns a standard build configuration.
func DefaultBuild() config.BuildConfig {
	return config.BuildConfig{
		Image:    "buildpack-deps:bookworm-scm",
		Packages: []string{"git", "curl"},
	}
}

// AlpineBuild returns a minimal alpine-based build configuration.
func AlpineBuild() config.BuildConfig {
	return config.BuildConfig{
		Image:    "alpine:latest",
		Packages: nil,
	}
}

// BuildWithPackages returns a build config with the specified packages.
func BuildWithPackages(image string, packages ...string) config.BuildConfig {
	return config.BuildConfig{
		Image:    image,
		Packages: packages,
	}
}

// BuildWithInstructions returns a build config with custom instructions.
func BuildWithInstructions(image string, instructions *config.DockerInstructions) config.BuildConfig {
	return config.BuildConfig{
		Image:        image,
		Instructions: instructions,
	}
}

// ----------------------------------------------------------------
// Security Config Presets
// ----------------------------------------------------------------

// SecurityFirewallEnabled returns a security config with firewall enabled.
func SecurityFirewallEnabled() config.SecurityConfig {
	return config.SecurityConfig{
		Firewall: &config.FirewallConfig{
			Enable: true,
		},
		DockerSocket: false,
	}
}

// SecurityFirewallDisabled returns a security config with firewall disabled.
func SecurityFirewallDisabled() config.SecurityConfig {
	hostProxyDisabled := false
	return config.SecurityConfig{
		Firewall: &config.FirewallConfig{
			Enable: false,
		},
		DockerSocket:    false,
		EnableHostProxy: &hostProxyDisabled,
	}
}

// SecurityWithDockerSocket returns a security config with Docker socket access enabled.
func SecurityWithDockerSocket() config.SecurityConfig {
	return config.SecurityConfig{
		DockerSocket: true,
	}
}

// SecurityWithFirewallDomains returns a security config with custom firewall domains.
func SecurityWithFirewallDomains(addDomains []string) config.SecurityConfig {
	return config.SecurityConfig{
		Firewall: &config.FirewallConfig{
			Enable:     true,
			AddDomains: addDomains,
		},
	}
}

// SecurityWithGitCredentials returns a security config with git credential settings.
func SecurityWithGitCredentials(forwardHTTPS, forwardSSH, copyConfig bool) config.SecurityConfig {
	return config.SecurityConfig{
		GitCredentials: &config.GitCredentialsConfig{
			ForwardHTTPS:  &forwardHTTPS,
			ForwardSSH:    &forwardSSH,
			CopyGitConfig: &copyConfig,
		},
	}
}

// ----------------------------------------------------------------
// Agent Config Presets
// ----------------------------------------------------------------

// DefaultAgent returns a minimal agent configuration.
func DefaultAgent() config.AgentConfig {
	return config.AgentConfig{}
}

// AgentWithEnv returns an agent config with environment variables.
func AgentWithEnv(env map[string]string) config.AgentConfig {
	return config.AgentConfig{
		Env: env,
	}
}

// AgentWithIncludes returns an agent config with include files.
func AgentWithIncludes(includes ...string) config.AgentConfig {
	return config.AgentConfig{
		Includes: includes,
	}
}

// ----------------------------------------------------------------
// Workspace Config Presets
// ----------------------------------------------------------------

// DefaultWorkspace returns a workspace config with bind mode.
func DefaultWorkspace() config.WorkspaceConfig {
	return config.WorkspaceConfig{
		RemotePath:  "/workspace",
		DefaultMode: "bind",
	}
}

// WorkspaceSnapshot returns a workspace config with snapshot mode.
func WorkspaceSnapshot() config.WorkspaceConfig {
	return config.WorkspaceConfig{
		RemotePath:  "/workspace",
		DefaultMode: "snapshot",
	}
}

// WorkspaceWithPath returns a workspace config with a custom remote path.
func WorkspaceWithPath(remotePath string) config.WorkspaceConfig {
	return config.WorkspaceConfig{
		RemotePath:  remotePath,
		DefaultMode: "bind",
	}
}

// ----------------------------------------------------------------
// Docker Instructions Presets
// ----------------------------------------------------------------

// InstructionsWithEnv returns Docker instructions with environment variables.
func InstructionsWithEnv(env map[string]string) *config.DockerInstructions {
	return &config.DockerInstructions{
		Env: env,
	}
}

// InstructionsWithRootRun returns Docker instructions with root commands.
func InstructionsWithRootRun(commands ...string) *config.DockerInstructions {
	runs := make([]config.RunInstruction, len(commands))
	for i, cmd := range commands {
		runs[i] = config.RunInstruction{Cmd: cmd}
	}
	return &config.DockerInstructions{
		RootRun: runs,
	}
}

// InstructionsWithUserRun returns Docker instructions with user commands.
func InstructionsWithUserRun(commands ...string) *config.DockerInstructions {
	runs := make([]config.RunInstruction, len(commands))
	for i, cmd := range commands {
		runs[i] = config.RunInstruction{Cmd: cmd}
	}
	return &config.DockerInstructions{
		UserRun: runs,
	}
}

// InstructionsWithCopy returns Docker instructions with copy commands.
func InstructionsWithCopy(copies ...config.CopyInstruction) *config.DockerInstructions {
	return &config.DockerInstructions{
		Copy: copies,
	}
}
