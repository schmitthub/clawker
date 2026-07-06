package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/pkg/whail"
)

// Builder handles Docker image building for clawker projects.
type Builder struct {
	client      *Client
	config      *config.Project
	log         *logger.Logger
	workDir     string
	projectName string
}

// BuilderOptions contains options for build operations.
type BuilderOptions struct {
	NoCache         bool                    // Build without Docker cache
	Labels          map[string]string       // Labels to apply to the built image
	Target          string                  // Multi-stage build target
	Pull            bool                    // Always pull base image
	SuppressOutput  bool                    // Suppress build output
	NetworkMode     string                  // Network mode for build
	BuildArgs       map[string]*string      // Build-time variables
	Tags            []string                // Additional tags for the image (merged with imageTag)
	BuildKitEnabled bool                    // Use BuildKit builder for cache mount support
	OnProgress      whail.BuildProgressFunc // Progress callback for build events
	OnComplete      whail.BuildCompleteFunc // Fires once with the built image digest/ID
	// HarnessVersion is the concrete harness version baked into the rendered
	// Dockerfile's version ARG default. Resolved upstream at the command
	// layer via bundler.ResolveHarnessVersion (using Factory.HttpClient).
	// Empty string falls back to bundler's DefaultHarnessVersion literal —
	// preserves offline-build behaviour.
	HarnessVersion string
	// HarnessName is the selected harness registry key; stamped onto the
	// image as the harness label (the build→runtime join key).
	HarnessName string
}

// toBuildImageOpts maps BuilderOptions to BuildImageOpts with the given per-call parameters.
func (o BuilderOptions) toBuildImageOpts(tags []string, dockerfile string, contextDir string) BuildImageOpts {
	return BuildImageOpts{
		Tags:            tags,
		Dockerfile:      dockerfile,
		NoCache:         o.NoCache,
		Labels:          o.Labels,
		Target:          o.Target,
		Pull:            o.Pull,
		SuppressOutput:  o.SuppressOutput,
		NetworkMode:     o.NetworkMode,
		BuildArgs:       o.BuildArgs,
		BuildKitEnabled: o.BuildKitEnabled,
		ContextDir:      contextDir,
		OnProgress:      o.OnProgress,
		OnComplete:      o.OnComplete,
	}
}

// NewBuilder creates a new Builder instance.
// projectName is the resolved project identity (from ProjectManager); empty string for unregistered projects.
// The builder inherits the logger from the Client.
func NewBuilder(cli *Client, cfg *config.Project, workDir, projectName string) *Builder {
	return &Builder{
		client:      cli,
		config:      cfg,
		log:         cli.log,
		workDir:     workDir,
		projectName: projectName,
	}
}

// Build builds the project's harness image, first ensuring the per-project
// shared base image (clawker-<project>:base) exists and is fresh. The base
// carries the harness-agnostic layers (packages, user setup, project
// instructions); freshness is keyed by a content hash stamped as an image
// label.
func (b *Builder) Build(ctx context.Context, imageTag string, opts BuilderOptions) error {
	gen := bundler.NewProjectGenerator(b.client.cfg, b.workDir)
	gen.BuildKitEnabled = opts.BuildKitEnabled
	gen.HarnessVersion = opts.HarnessVersion
	gen.Harness = opts.HarnessName

	// Merge image labels into build options (applied via Docker API, not in Dockerfile)
	opts.Labels = b.mergeImageLabels(opts.Labels)
	if opts.HarnessName != "" {
		opts.Labels[consts.LabelHarness] = opts.HarnessName
	}

	// Merge tags: primary tag + any additional tags from options
	tags := mergeTags(imageTag, opts.Tags)

	baseTag := BaseImageTag(b.projectName)
	gen.BaseImageRef = baseTag

	baseDockerfile, err := gen.GenerateBase()
	if err != nil {
		return fmt.Errorf("failed to generate base Dockerfile: %w", err)
	}
	baseHash, err := gen.BaseContentHash(baseDockerfile)
	if err != nil {
		return fmt.Errorf("failed to hash base image inputs: %w", err)
	}

	stale, err := b.baseImageStale(ctx, baseTag, baseHash)
	if err != nil {
		return fmt.Errorf("failed to check base image freshness: %w", err)
	}
	if opts.NoCache || stale {
		b.log.Debug().Str("image", baseTag).Str("hash", baseHash).Bool("no_cache", opts.NoCache).
			Msg("building shared base image")
		if buildErr := b.buildBase(ctx, gen, baseTag, baseHash, baseDockerfile, opts); buildErr != nil {
			return fmt.Errorf("building base image %s: %w", baseTag, buildErr)
		}
	}

	// Stamp the harness image with the base generation it was cut from.
	opts.Labels[consts.LabelBaseContentHash] = baseHash
	// The harness build's parent is the local-only :base tag — a pull
	// attempt would fail against any registry. --pull applies to the base
	// build, where the registry-backed parent lives.
	opts.Pull = false

	b.log.Debug().Str("image", imageTag).Msg("building harness image")

	dockerfile, err := gen.GenerateHarness()
	if err != nil {
		return fmt.Errorf("failed to generate Dockerfile: %w", err)
	}

	// BuildKit reads from the filesystem, not a tar stream.
	// Write the generated Dockerfile + scripts to a temp dir for BuildKit to mount.
	if opts.BuildKitEnabled {
		tempDir, err := os.MkdirTemp("", "clawker-buildctx-*")
		if err != nil {
			return fmt.Errorf("failed to create build context temp dir: %w", err)
		}
		defer func() {
			if err := os.RemoveAll(tempDir); err != nil {
				b.log.Debug().Err(err).Str("dir", tempDir).Msg("failed to clean up build context temp dir")
			}
		}()

		if writeErr := gen.WriteHarnessBuildContextToDir(tempDir, dockerfile); writeErr != nil {
			return fmt.Errorf("failed to write build context: %w", writeErr)
		}

		return b.client.BuildImage(ctx, nil, opts.toBuildImageOpts(tags, "Dockerfile", tempDir))
	}

	// Legacy path: tar stream build context
	buildCtx, err := gen.GenerateHarnessBuildContext(dockerfile)
	if err != nil {
		return fmt.Errorf("failed to generate build context: %w", err)
	}

	return b.client.BuildImage(ctx, buildCtx, opts.toBuildImageOpts(tags, "Dockerfile", gen.GetBuildContext()))
}

// baseImageStale reports whether the shared base image must be (re)built:
// true when no managed image exists at baseTag, or when its content-hash
// label differs from wantHash. Non-NotFound inspect errors propagate.
func (b *Builder) baseImageStale(ctx context.Context, baseTag, wantHash string) (bool, error) {
	result, err := b.client.ImageInspect(ctx, baseTag)
	if err != nil {
		if isNotFoundError(err) {
			return true, nil
		}
		return false, fmt.Errorf("inspecting base image %s: %w", baseTag, err)
	}
	if result.Config == nil {
		return true, nil
	}
	return result.Config.Labels[consts.LabelBaseContentHash] != wantHash, nil
}

// buildBase builds the shared base image. Its context is the project
// build-context directory (user copy srcs live there); the rendered
// Dockerfile is supplied out-of-band. Base labels are clawker's own plus
// the content hash and purpose — never user labels or the harness label,
// which describe the runnable harness image.
func (b *Builder) buildBase(
	ctx context.Context,
	gen *bundler.ProjectGenerator,
	baseTag, baseHash string,
	dockerfile []byte,
	opts BuilderOptions,
) error {
	baseOpts := opts
	baseOpts.Labels = b.client.ImageLabels(b.projectName, build.Version)
	baseOpts.Labels[consts.LabelBaseContentHash] = baseHash
	baseOpts.Labels[consts.LabelPurpose] = consts.PurposeBaseImage
	baseOpts.Target = ""
	baseOpts.OnComplete = nil // the caller's --iidfile wants the runnable harness image
	baseOpts.OnProgress = phaseProgress(opts.OnProgress, basePhasePrefix)

	if opts.BuildKitEnabled {
		tempDir, err := os.MkdirTemp("", "clawker-basectx-*")
		if err != nil {
			return fmt.Errorf("failed to create base Dockerfile temp dir: %w", err)
		}
		defer func() {
			if rmErr := os.RemoveAll(tempDir); rmErr != nil {
				b.log.Debug().Err(rmErr).Str("dir", tempDir).Msg("failed to clean up base Dockerfile temp dir")
			}
		}()

		dockerfilePath := filepath.Join(tempDir, "Dockerfile")
		//nolint:gosec // generated Dockerfile, not a secret
		if writeErr := os.WriteFile(dockerfilePath, dockerfile, 0o644); writeErr != nil {
			return fmt.Errorf("failed to write base Dockerfile: %w", writeErr)
		}

		// Absolute Dockerfile path + the project dir as context: BuildKit
		// mounts them as separate locals, so the rendered Dockerfile never
		// touches the user's project directory.
		return b.client.BuildImage(
			ctx,
			nil,
			baseOpts.toBuildImageOpts([]string{baseTag}, dockerfilePath, gen.GetBuildContext()),
		)
	}

	buildCtx, err := gen.GenerateBaseBuildContext(dockerfile)
	if err != nil {
		return fmt.Errorf("failed to generate base build context: %w", err)
	}

	return b.client.BuildImage(
		ctx,
		buildCtx,
		baseOpts.toBuildImageOpts([]string{baseTag}, bundler.BaseDockerfileName, gen.GetBuildContext()),
	)
}

// basePhasePrefix namespaces the base build's progress events.
const basePhasePrefix = "base"

// phaseProgress decorates a progress callback with a phase namespace so
// two sequential builds' step IDs can't collide (the legacy stream emits
// bare "step-N" IDs per build). Nil-safe: nil in, nil out.
func phaseProgress(fn whail.BuildProgressFunc, phase string) whail.BuildProgressFunc {
	if fn == nil {
		return nil
	}
	return func(event whail.BuildProgressEvent) {
		event.StepID = phase + ":" + event.StepID
		// Internal housekeeping vertices keep their "[internal]" prefix
		// intact — downstream displays filter on it.
		if !whail.IsInternalStep(event.StepName) {
			event.StepName = "[" + phase + "] " + event.StepName
		}
		fn(event)
	}
}

// mergeImageLabels combines clawker internal labels and user-defined labels into opts.Labels.
// User labels from build.instructions.labels are applied first, then clawker internal labels
// (managed, project, version, created) are layered on top so they cannot be overridden.
func (b *Builder) mergeImageLabels(existing map[string]string) map[string]string {
	merged := make(map[string]string)

	// Start with any existing labels from opts
	for k, v := range existing {
		merged[k] = v
	}

	// Add user-defined labels from build instructions
	if b.config.Build.Instructions != nil {
		for k, v := range b.config.Build.Instructions.Labels {
			merged[k] = v
		}
	}

	// Add clawker internal labels last — these cannot be overridden
	for k, v := range b.client.ImageLabels(b.projectName, build.Version) {
		merged[k] = v
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
