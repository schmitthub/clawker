package build

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	pkgbuild "github.com/schmitthub/clawker/pkg/build"
	"github.com/schmitthub/clawker/pkg/logger"
)

// Builder handles Docker image building for clawker projects.
type Builder struct {
	client  *docker.Client
	config  *config.Config
	workDir string
}

// Options contains options for build operations.
type Options struct {
	ForceBuild bool              // Force rebuild even if image exists
	NoCache    bool              // Build without Docker cache
	Labels     map[string]string // Labels to apply to the built image
}

// NewBuilder creates a new Builder instance.
func NewBuilder(cli *docker.Client, cfg *config.Config, workDir string) *Builder {
	return &Builder{
		client:  cli,
		config:  cfg,
		workDir: workDir,
	}
}

// EnsureImage ensures an image is available, building if necessary.
// If ForceBuild is true, rebuilds even if the image exists.
func (b *Builder) EnsureImage(ctx context.Context, imageTag string, opts Options) error {
	gen := pkgbuild.NewProjectGenerator(b.config, b.workDir)

	// Check if we should use a custom Dockerfile
	if gen.UseCustomDockerfile() {
		logger.Info().
			Str("dockerfile", b.config.Build.Dockerfile).
			Msg("building from custom Dockerfile")

		// Create build context from directory
		buildCtx, err := pkgbuild.CreateBuildContextFromDir(
			gen.GetBuildContext(),
			gen.GetCustomDockerfilePath(),
		)
		if err != nil {
			return fmt.Errorf("failed to create build context: %w", err)
		}

		return b.client.BuildImage(ctx, buildCtx, docker.BuildImageOpts{
			Tag:        imageTag,
			Dockerfile: filepath.Base(gen.GetCustomDockerfilePath()),
			NoCache:    opts.NoCache,
			Labels:     opts.Labels,
		})
	}

	// Check if image exists and we don't need to rebuild
	if !opts.ForceBuild {
		exists, err := b.client.ImageExists(ctx, imageTag)
		if err != nil {
			return err
		}
		if exists {
			logger.Debug().Str("image", imageTag).Msg("image exists, skipping build")
			return nil
		}
	}

	// Generate and build Dockerfile
	return b.Build(ctx, imageTag, opts.NoCache, opts.Labels)
}

// Build unconditionally builds the Docker image.
func (b *Builder) Build(ctx context.Context, imageTag string, noCache bool, labels map[string]string) error {
	gen := pkgbuild.NewProjectGenerator(b.config, b.workDir)

	// Check if we should use a custom Dockerfile
	if gen.UseCustomDockerfile() {
		logger.Info().
			Str("dockerfile", b.config.Build.Dockerfile).
			Msg("building from custom Dockerfile")

		buildCtx, err := pkgbuild.CreateBuildContextFromDir(
			gen.GetBuildContext(),
			gen.GetCustomDockerfilePath(),
		)
		if err != nil {
			return fmt.Errorf("failed to create build context: %w", err)
		}

		return b.client.BuildImage(ctx, buildCtx, docker.BuildImageOpts{
			Tag:        imageTag,
			Dockerfile: filepath.Base(gen.GetCustomDockerfilePath()),
			NoCache:    noCache,
			Labels:     labels,
		})
	}

	logger.Info().Str("image", imageTag).Msg("building container image")

	buildCtx, err := gen.GenerateBuildContext()
	if err != nil {
		return fmt.Errorf("failed to generate build context: %w", err)
	}

	return b.client.BuildImage(ctx, buildCtx, docker.BuildImageOpts{
		Tag:        imageTag,
		Dockerfile: "Dockerfile",
		NoCache:    noCache,
		Labels:     labels,
	})
}
