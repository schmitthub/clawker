package configtest

import (
	"github.com/schmitthub/clawker/internal/config"
)

// ProjectBuilder provides a fluent API for constructing config.Project objects in tests.
// It stores a pointer internally to avoid copying sync.RWMutex, and Build() returns
// a new *config.Project each time (field-by-field copy of exported fields only)
// so that callers get independent, mutex-safe instances.
type ProjectBuilder struct {
	cfg *config.Project
}

// NewProjectBuilder creates a new ProjectBuilder with sensible defaults.
func NewProjectBuilder() *ProjectBuilder {
	return &ProjectBuilder{
		cfg: &config.Project{
			Version: "1",
			Workspace: config.WorkspaceConfig{
				RemotePath:  "/workspace",
				DefaultMode: "bind",
			},
		},
	}
}

// WithVersion sets the config version.
func (b *ProjectBuilder) WithVersion(version string) *ProjectBuilder {
	b.cfg.Version = version
	return b
}

// WithProject sets the project name.
func (b *ProjectBuilder) WithProject(name string) *ProjectBuilder {
	b.cfg.Project = name
	return b
}

// WithDefaultImage sets the default image.
func (b *ProjectBuilder) WithDefaultImage(image string) *ProjectBuilder {
	b.cfg.DefaultImage = image
	return b
}

// WithBuild sets the build configuration.
func (b *ProjectBuilder) WithBuild(build config.BuildConfig) *ProjectBuilder {
	b.cfg.Build = build
	return b
}

// WithAgent sets the agent configuration.
func (b *ProjectBuilder) WithAgent(agent config.AgentConfig) *ProjectBuilder {
	b.cfg.Agent = agent
	return b
}

// WithWorkspace sets the workspace configuration.
func (b *ProjectBuilder) WithWorkspace(workspace config.WorkspaceConfig) *ProjectBuilder {
	b.cfg.Workspace = workspace
	return b
}

// WithSecurity sets the security configuration.
func (b *ProjectBuilder) WithSecurity(security config.SecurityConfig) *ProjectBuilder {
	b.cfg.Security = security
	return b
}

// WithLoop sets the loop configuration.
func (b *ProjectBuilder) WithLoop(loop *config.LoopConfig) *ProjectBuilder {
	b.cfg.Loop = loop
	return b
}

// ForTestBaseImage swaps the image to alpine:latest and clears packages for fast builds.
func (b *ProjectBuilder) ForTestBaseImage() *ProjectBuilder {
	b.cfg.Build.Image = "alpine:latest"
	b.cfg.Build.Packages = nil
	return b
}

// Build returns a new *config.Project with a fresh zero-value mutex.
// Each call returns an independent instance (exported fields only).
func (b *ProjectBuilder) Build() *config.Project {
	return &config.Project{
		Version:      b.cfg.Version,
		Project:      b.cfg.Project,
		DefaultImage: b.cfg.DefaultImage,
		Build:        b.cfg.Build,
		Agent:        b.cfg.Agent,
		Workspace:    b.cfg.Workspace,
		Security:     b.cfg.Security,
		Loop:        b.cfg.Loop,
	}
}
