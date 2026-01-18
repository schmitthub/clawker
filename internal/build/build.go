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
	ForceBuild     bool               // Force rebuild even if image exists
	NoCache        bool               // Build without Docker cache
	Labels         map[string]string  // Labels to apply to the built image
	Target         string             // Multi-stage build target
	Pull           bool               // Always pull base image
	SuppressOutput bool               // Suppress build output
	NetworkMode    string             // Network mode for build
	BuildArgs      map[string]*string // Build-time variables
	Tags           []string           // Additional tags for the image (merged with imageTag)
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
			Tags:           []string{imageTag},
			Dockerfile:     filepath.Base(gen.GetCustomDockerfilePath()),
			NoCache:        opts.NoCache,
			Labels:         opts.Labels,
			Target:         opts.Target,
			Pull:           opts.Pull,
			SuppressOutput: opts.SuppressOutput,
			NetworkMode:    opts.NetworkMode,
			BuildArgs:      opts.BuildArgs,
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
	return b.Build(ctx, imageTag, opts)
}

// Build unconditionally builds the Docker image.
func (b *Builder) Build(ctx context.Context, imageTag string, opts Options) error {
	gen := pkgbuild.NewProjectGenerator(b.config, b.workDir)

	// Merge tags: primary tag + any additional tags from options
	tags := mergeTags(imageTag, opts.Tags)

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
			Tags:           tags,
			Dockerfile:     filepath.Base(gen.GetCustomDockerfilePath()),
			NoCache:        opts.NoCache,
			Labels:         opts.Labels,
			Target:         opts.Target,
			Pull:           opts.Pull,
			SuppressOutput: opts.SuppressOutput,
			NetworkMode:    opts.NetworkMode,
			BuildArgs:      opts.BuildArgs,
		})
	}

	logger.Info().Str("image", imageTag).Msg("building container image")

	buildCtx, err := gen.GenerateBuildContext()
	if err != nil {
		return fmt.Errorf("failed to generate build context: %w", err)
	}

	return b.client.BuildImage(ctx, buildCtx, docker.BuildImageOpts{
		Tags:           tags,
		Dockerfile:     "Dockerfile",
		NoCache:        opts.NoCache,
		Labels:         opts.Labels,
		Target:         opts.Target,
		Pull:           opts.Pull,
		SuppressOutput: opts.SuppressOutput,
		NetworkMode:    opts.NetworkMode,
		BuildArgs:      opts.BuildArgs,
	})
}

// mergeTags combines the primary tag with additional tags, avoiding duplicates.
func mergeTags(primary string, additional []string) []string {
	seen := make(map[string]bool)
	result := []string{primary}
	seen[primary] = true

	for _, tag := range additional {
		if !seen[tag] {
			result = append(result, tag)
			seen[tag] = true
		}
	}
	return result
}
