package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// Builder handles Docker image building for clawker projects.
type Builder struct {
	client  *docker.Client
	config  *config.Config
	workDir string
}

// Options contains options for build operations.
type Options struct {
	ForceBuild      bool               // Force rebuild even if image exists
	NoCache         bool               // Build without Docker cache
	Labels          map[string]string  // Labels to apply to the built image
	Target          string             // Multi-stage build target
	Pull            bool               // Always pull base image
	SuppressOutput  bool               // Suppress build output
	NetworkMode     string             // Network mode for build
	BuildArgs       map[string]*string // Build-time variables
	Tags            []string           // Additional tags for the image (merged with imageTag)
	Dockerfile      []byte             // Pre-rendered Dockerfile bytes (avoids re-generation)
	BuildKitEnabled bool               // Use BuildKit builder for cache mount support
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
// Uses content-addressed tags to detect whether config actually changed.
// If ForceBuild is true, rebuilds even if the image exists.
func (b *Builder) EnsureImage(ctx context.Context, imageTag string, opts Options) error {
	gen := NewProjectGenerator(b.config, b.workDir)
	gen.BuildKitEnabled = opts.BuildKitEnabled

	// Custom Dockerfiles bypass content hashing — always use legacy behavior
	if gen.UseCustomDockerfile() {
		logger.Info().
			Str("dockerfile", b.config.Build.Dockerfile).
			Msg("building from custom Dockerfile")

		buildCtx, err := CreateBuildContextFromDir(
			gen.GetBuildContext(),
			gen.GetCustomDockerfilePath(),
		)
		if err != nil {
			return fmt.Errorf("failed to create build context: %w", err)
		}

		return b.client.BuildImage(ctx, buildCtx, docker.BuildImageOpts{
			Tags:            mergeTags(imageTag, opts.Tags),
			Dockerfile:      filepath.Base(gen.GetCustomDockerfilePath()),
			NoCache:         opts.NoCache,
			Labels:          opts.Labels,
			Target:          opts.Target,
			Pull:            opts.Pull,
			SuppressOutput:  opts.SuppressOutput,
			NetworkMode:     opts.NetworkMode,
			BuildArgs:       opts.BuildArgs,
			BuildKitEnabled: opts.BuildKitEnabled,
			ContextDir:      gen.GetBuildContext(),
		})
	}

	// Render Dockerfile and compute content hash
	dockerfile, err := gen.Generate()
	if err != nil {
		return fmt.Errorf("failed to generate Dockerfile: %w", err)
	}

	hash, err := ContentHash(dockerfile, b.config.Agent.Includes, b.workDir)
	if err != nil {
		return fmt.Errorf("failed to compute content hash: %w", err)
	}

	hashTag := docker.ImageTagWithHash(b.config.Project, hash)

	// Check if content-addressed image already exists
	if !opts.ForceBuild {
		exists, err := b.client.ImageExists(ctx, hashTag)
		if err != nil {
			return err
		}
		if exists {
			logger.Debug().
				Str("image", hashTag).
				Msg("image up-to-date, skipping build")

			// Ensure :latest points to this hash
			if err := b.client.TagImage(ctx, hashTag, imageTag); err != nil {
				return fmt.Errorf("failed to update :latest alias: %w", err)
			}
			return nil
		}
	}

	// Build with both :latest and content-addressed tags
	opts.Tags = append(opts.Tags, hashTag)
	opts.Dockerfile = dockerfile
	return b.Build(ctx, imageTag, opts)
}

// Build unconditionally builds the Docker image.
func (b *Builder) Build(ctx context.Context, imageTag string, opts Options) error {
	gen := NewProjectGenerator(b.config, b.workDir)
	gen.BuildKitEnabled = opts.BuildKitEnabled

	// Merge image labels into build options (applied via Docker API, not in Dockerfile)
	opts.Labels = b.mergeImageLabels(opts.Labels)

	// Merge tags: primary tag + any additional tags from options
	tags := mergeTags(imageTag, opts.Tags)

	// Check if we should use a custom Dockerfile
	if gen.UseCustomDockerfile() {
		logger.Info().
			Str("dockerfile", b.config.Build.Dockerfile).
			Msg("building from custom Dockerfile")

		buildCtx, err := CreateBuildContextFromDir(
			gen.GetBuildContext(),
			gen.GetCustomDockerfilePath(),
		)
		if err != nil {
			return fmt.Errorf("failed to create build context: %w", err)
		}

		return b.client.BuildImage(ctx, buildCtx, docker.BuildImageOpts{
			Tags:            tags,
			Dockerfile:      filepath.Base(gen.GetCustomDockerfilePath()),
			NoCache:         opts.NoCache,
			Labels:          opts.Labels,
			Target:          opts.Target,
			Pull:            opts.Pull,
			SuppressOutput:  opts.SuppressOutput,
			NetworkMode:     opts.NetworkMode,
			BuildArgs:       opts.BuildArgs,
			BuildKitEnabled: opts.BuildKitEnabled,
			ContextDir:      gen.GetBuildContext(),
		})
	}

	logger.Info().Str("image", imageTag).Msg("building container image")

	// Generate Dockerfile bytes if not already provided
	var dockerfile []byte
	if len(opts.Dockerfile) > 0 {
		dockerfile = opts.Dockerfile
	} else {
		var err error
		dockerfile, err = gen.Generate()
		if err != nil {
			return fmt.Errorf("failed to generate Dockerfile: %w", err)
		}
	}

	// BuildKit reads from the filesystem, not a tar stream.
	// Write the generated Dockerfile + scripts to a temp dir for BuildKit to mount.
	if opts.BuildKitEnabled {
		tempDir, err := os.MkdirTemp("", "clawker-buildctx-*")
		if err != nil {
			return fmt.Errorf("failed to create build context temp dir: %w", err)
		}
		defer os.RemoveAll(tempDir)

		if err := gen.WriteBuildContextToDir(tempDir, dockerfile); err != nil {
			return fmt.Errorf("failed to write build context: %w", err)
		}

		return b.client.BuildImage(ctx, nil, docker.BuildImageOpts{
			Tags:            tags,
			Dockerfile:      "Dockerfile",
			NoCache:         opts.NoCache,
			Labels:          opts.Labels,
			Target:          opts.Target,
			Pull:            opts.Pull,
			SuppressOutput:  opts.SuppressOutput,
			NetworkMode:     opts.NetworkMode,
			BuildArgs:       opts.BuildArgs,
			BuildKitEnabled: opts.BuildKitEnabled,
			ContextDir:      tempDir,
		})
	}

	// Legacy path: tar stream build context
	buildCtx, err := gen.GenerateBuildContextFromDockerfile(dockerfile)
	if err != nil {
		return fmt.Errorf("failed to generate build context: %w", err)
	}

	return b.client.BuildImage(ctx, buildCtx, docker.BuildImageOpts{
		Tags:            tags,
		Dockerfile:      "Dockerfile",
		NoCache:         opts.NoCache,
		Labels:          opts.Labels,
		Target:          opts.Target,
		Pull:            opts.Pull,
		SuppressOutput:  opts.SuppressOutput,
		NetworkMode:     opts.NetworkMode,
		BuildArgs:       opts.BuildArgs,
		BuildKitEnabled: opts.BuildKitEnabled,
		ContextDir:      gen.GetBuildContext(),
	})
}

// mergeImageLabels combines clawker internal labels and user-defined labels into opts.Labels.
// Internal labels (managed, project, version, created) are set first, then user labels
// from build.instructions.labels are layered on top, allowing user overrides.
func (b *Builder) mergeImageLabels(existing map[string]string) map[string]string {
	merged := make(map[string]string)

	// Start with any existing labels from opts
	for k, v := range existing {
		merged[k] = v
	}

	// Add clawker internal labels
	for k, v := range docker.ImageLabels(b.config.Project, b.config.Version) {
		merged[k] = v
	}

	// Add user-defined labels from build instructions
	if b.config.Build.Instructions != nil {
		for k, v := range b.config.Build.Instructions.Labels {
			merged[k] = v
		}
	}

	return merged
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
