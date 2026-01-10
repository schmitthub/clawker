package build

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/internal/dockerfile"
	"github.com/schmitthub/claucker/internal/engine"
	"github.com/schmitthub/claucker/pkg/logger"
)

// Builder handles Docker image building for claucker projects.
type Builder struct {
	engine  *engine.Engine
	config  *config.Config
	workDir string
}

// Options contains options for build operations.
type Options struct {
	ForceBuild bool // Force rebuild even if image exists
	NoCache    bool // Build without Docker cache
}

// NewBuilder creates a new Builder instance.
func NewBuilder(eng *engine.Engine, cfg *config.Config, workDir string) *Builder {
	return &Builder{
		engine:  eng,
		config:  cfg,
		workDir: workDir,
	}
}

// EnsureImage ensures an image is available, building if necessary.
// If ForceBuild is true, rebuilds even if the image exists.
func (b *Builder) EnsureImage(ctx context.Context, imageTag string, opts Options) error {
	imgMgr := engine.NewImageManager(b.engine)
	gen := dockerfile.NewGenerator(b.config, b.workDir)

	// Check if we should use a custom Dockerfile
	if gen.UseCustomDockerfile() {
		logger.Info().
			Str("dockerfile", b.config.Build.Dockerfile).
			Msg("building from custom Dockerfile")

		// Create build context from directory
		buildCtx, err := dockerfile.CreateBuildContextFromDir(
			gen.GetBuildContext(),
			gen.GetCustomDockerfilePath(),
		)
		if err != nil {
			return fmt.Errorf("failed to create build context: %w", err)
		}

		return imgMgr.BuildImage(buildCtx, imageTag, filepath.Base(gen.GetCustomDockerfilePath()), nil, opts.NoCache)
	}

	// Check if image exists and we don't need to rebuild
	if !opts.ForceBuild {
		exists, err := b.engine.ImageExists(imageTag)
		if err != nil {
			return err
		}
		if exists {
			logger.Debug().Str("image", imageTag).Msg("image exists, skipping build")
			return nil
		}
	}

	// Generate and build Dockerfile
	return b.Build(ctx, imageTag, opts.NoCache)
}

// Build unconditionally builds the Docker image.
func (b *Builder) Build(ctx context.Context, imageTag string, noCache bool) error {
	imgMgr := engine.NewImageManager(b.engine)
	gen := dockerfile.NewGenerator(b.config, b.workDir)

	// Check if we should use a custom Dockerfile
	if gen.UseCustomDockerfile() {
		logger.Info().
			Str("dockerfile", b.config.Build.Dockerfile).
			Msg("building from custom Dockerfile")

		buildCtx, err := dockerfile.CreateBuildContextFromDir(
			gen.GetBuildContext(),
			gen.GetCustomDockerfilePath(),
		)
		if err != nil {
			return fmt.Errorf("failed to create build context: %w", err)
		}

		return imgMgr.BuildImage(buildCtx, imageTag, filepath.Base(gen.GetCustomDockerfilePath()), nil, noCache)
	}

	logger.Info().Str("image", imageTag).Msg("building container image")

	buildCtx, err := gen.GenerateBuildContext()
	if err != nil {
		return fmt.Errorf("failed to generate build context: %w", err)
	}

	return imgMgr.BuildImage(buildCtx, imageTag, "Dockerfile", nil, noCache)
}
