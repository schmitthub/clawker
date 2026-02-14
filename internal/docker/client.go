package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/pkg/whail"
)

// Client embeds whail.Engine with clawker-specific label configuration.
// All whail.Engine methods are available directly on Client.
// Additional methods provide clawker-specific high-level operations.
type Client struct {
	*whail.Engine
	cfg *config.Config // lazily provides ProjectCfg() and Settings() for image resolution

	// BuildDefaultImageFunc overrides BuildDefaultImage when non-nil.
	// Used by fawker/tests to inject fake build behavior.
	// Follows the same pattern as whail.Engine.BuildKitImageBuilder.
	BuildDefaultImageFunc BuildDefaultImageFn

	// ChownImage overrides the image used for CopyToVolume's chown step.
	// When empty, defaults to "busybox:latest". Tests set this to a locally-built
	// labeled image to avoid DockerHub pulls and ensure test-label propagation.
	ChownImage string
}

// NewClient creates a new clawker Docker client.
// It configures the whail.Engine with clawker's label prefix and conventions.
// clientOptions holds configuration for NewClient.
type clientOptions struct {
	labels whail.LabelConfig
}

// ClientOption configures a NewClient call.
type ClientOption func(*clientOptions)

// WithLabels injects additional labels into the whail engine.
// Use this to add test labels (e.g., dev.clawker.test=true) that propagate
// to all containers, volumes, and networks created by the client.
func WithLabels(labels whail.LabelConfig) ClientOption {
	return func(o *clientOptions) {
		o.labels = labels
	}
}

func NewClient(ctx context.Context, cfg *config.Config, opts ...ClientOption) (*Client, error) {
	var o clientOptions
	for _, opt := range opts {
		opt(&o)
	}

	engineOpts := whail.EngineOptions{
		LabelPrefix:  EngineLabelPrefix,
		ManagedLabel: EngineManagedLabel,
		Labels:       o.labels,
	}

	engine, err := whail.NewWithOptions(ctx, engineOpts)
	if err != nil {
		return nil, err
	}

	return &Client{Engine: engine, cfg: cfg}, nil
}

// SetConfig sets the config gateway on the client. Intended for tests.
func (c *Client) SetConfig(cfg *config.Config) {
	c.cfg = cfg
}

// Close closes the underlying Docker connection.
func (c *Client) Close() error {
	return c.APIClient.Close()
}

// IsMonitoringActive checks if the clawker monitoring stack is running.
// It looks for the otel-collector container on the clawker-net network.
func (c *Client) IsMonitoringActive(ctx context.Context) bool {
	f := whail.Filters{}.Add("name", "otel-collector").Add("status", "running")
	result, err := c.ContainerList(ctx, whail.ContainerListOptions{
		Filters: f,
	})
	if err != nil {
		logger.Debug().Err(err).Msg("failed to check monitoring status")
		return false
	}

	// Check if any otel-collector container is running on clawker-net
	for _, ctr := range result.Items {
		if ctr.NetworkSettings != nil {
			for netName := range ctr.NetworkSettings.Networks {
				if netName == "clawker-net" {
					logger.Debug().Msg("monitoring stack detected as active")
					return true
				}
			}
		}
	}

	return false
}

// TagImage adds an additional tag to an existing managed image.
// source is the existing image reference, target is the new tag to apply.
func (c *Client) TagImage(ctx context.Context, source, target string) error {
	_, err := c.ImageTag(ctx, whail.ImageTagOptions{
		Source: source,
		Target: target,
	})
	return err
}

// ImageExists checks if a managed image exists locally.
// Returns true if the image exists and is managed, false if not found or unmanaged.
func (c *Client) ImageExists(ctx context.Context, imageRef string) (bool, error) {
	_, err := c.ImageInspect(ctx, imageRef)
	if err != nil {
		if isNotFoundError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// imageExistsRaw checks if an image exists locally without the managed label check.
// Use this for external images (e.g. busybox) that are never clawker-managed.
func (c *Client) imageExistsRaw(ctx context.Context, ref string) (bool, error) {
	_, err := c.APIClient.ImageInspect(ctx, ref)
	if err != nil {
		if isNotFoundError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// chownImage returns the image used for CopyToVolume's chown step.
// Defaults to "busybox:latest" when ChownImage is empty.
func (c *Client) chownImage() string {
	if c.ChownImage != "" {
		return c.ChownImage
	}
	return "busybox:latest"
}

// BuildImageOpts contains options for building an image.
type BuildImageOpts struct {
	Tags            []string                // -t, --tag (multiple allowed)
	Dockerfile      string                  // -f, --file
	BuildArgs       map[string]*string      // --build-arg KEY=VALUE
	NoCache         bool                    // --no-cache
	Labels          map[string]string       // --label KEY=VALUE (merged with clawker labels)
	Target          string                  // --target
	Pull            bool                    // --pull (maps to PullParent)
	SuppressOutput  bool                    // -q, --quiet
	NetworkMode     string                  // --network
	BuildKitEnabled bool                    // Use BuildKit builder via whail.ImageBuildKit
	ContextDir      string                  // Build context directory (required for BuildKit)
	OnProgress      whail.BuildProgressFunc // Progress callback for build events
}

// BuildImage builds a Docker image from a build context.
// When BuildKitEnabled and ContextDir are set, uses whail's BuildKit path
// (which supports --mount=type=cache). Otherwise falls back to the legacy
// Docker SDK ImageBuild API.
func (c *Client) BuildImage(ctx context.Context, buildContext io.Reader, opts BuildImageOpts) error {
	// Route to BuildKit when enabled and a context directory is available.
	// BuildKit uses the filesystem directly (not a tar stream) for local mounts.
	if opts.BuildKitEnabled && opts.ContextDir != "" {
		return c.ImageBuildKit(ctx, whail.ImageBuildKitOptions{
			Tags:           opts.Tags,
			ContextDir:     opts.ContextDir,
			Dockerfile:     opts.Dockerfile,
			BuildArgs:      opts.BuildArgs,
			NoCache:        opts.NoCache,
			Labels:         opts.Labels,
			Target:         opts.Target,
			Pull:           opts.Pull,
			SuppressOutput: opts.SuppressOutput,
			NetworkMode:    opts.NetworkMode,
			OnProgress:     opts.OnProgress,
		})
	}

	// Legacy SDK path
	options := whail.ImageBuildOptions{
		Tags:           opts.Tags,
		Dockerfile:     opts.Dockerfile,
		Remove:         true,
		NoCache:        opts.NoCache,
		BuildArgs:      opts.BuildArgs,
		Labels:         opts.Labels,
		Target:         opts.Target,
		PullParent:     opts.Pull,
		SuppressOutput: opts.SuppressOutput,
		NetworkMode:    opts.NetworkMode,
	}
	resp, err := c.ImageBuild(ctx, buildContext, options)
	if err != nil {
		return fmt.Errorf("building image: %w", err)
	}
	defer resp.Body.Close()

	// Process the build output
	// Even with SuppressOutput, we must still check for errors
	if opts.SuppressOutput {
		return c.processBuildOutputQuiet(resp.Body)
	}
	if opts.OnProgress != nil {
		return c.processBuildOutputWithProgress(resp.Body, opts.OnProgress)
	}
	return c.processBuildOutput(resp.Body)
}

// buildEvent represents a Docker build stream event.
type buildEvent struct {
	Stream      string `json:"stream"`
	Error       string `json:"error"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

// processBuildOutput processes and displays Docker build output.
func (c *Client) processBuildOutput(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)
	var parseErrors int

	for scanner.Scan() {
		var event buildEvent

		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			parseErrors++
			logger.Debug().
				Err(err).
				Str("raw", string(scanner.Bytes())).
				Msg("failed to parse build output event")
			// After many consecutive failures, consider this an error condition
			if parseErrors > 10 {
				return fmt.Errorf("build output stream appears corrupted: %d consecutive parse failures", parseErrors)
			}
			continue
		}
		parseErrors = 0 // Reset on successful parse

		if event.Error != "" {
			return fmt.Errorf("build error: %s", event.Error)
		}

		if event.ErrorDetail.Message != "" {
			return fmt.Errorf("build error: %s", event.ErrorDetail.Message)
		}

		// Log build output (trimmed)
		if stream := strings.TrimSpace(event.Stream); stream != "" {
			logger.Debug().Msg(stream)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading build output: %w", err)
	}

	logger.Debug().Msg("image build complete")
	return nil
}

// processBuildOutputQuiet processes Docker build output without displaying it,
// but still returns any build errors. Used for quiet/suppressed output modes.
func (c *Client) processBuildOutputQuiet(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)
	var parseErrors int

	for scanner.Scan() {
		var event buildEvent

		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			parseErrors++
			logger.Debug().
				Err(err).
				Str("raw", string(scanner.Bytes())).
				Msg("failed to parse build output event")
			if parseErrors > 10 {
				return fmt.Errorf("build output stream appears corrupted: %d consecutive parse failures", parseErrors)
			}
			continue
		}
		parseErrors = 0

		if event.Error != "" {
			return fmt.Errorf("build error: %s", event.Error)
		}

		if event.ErrorDetail.Message != "" {
			return fmt.Errorf("build error: %s", event.ErrorDetail.Message)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading build output: %w", err)
	}

	return nil
}

// legacyStepRe matches legacy Docker build step lines: "Step N/M : INSTRUCTION args".
var legacyStepRe = regexp.MustCompile(`^Step (\d+)/(\d+) : (.+)$`)

// processBuildOutputWithProgress processes legacy Docker build output and
// forwards structured progress events via the callback. Error checking is
// identical to processBuildOutput.
func (c *Client) processBuildOutputWithProgress(reader io.Reader, onProgress whail.BuildProgressFunc) error {
	scanner := bufio.NewScanner(reader)
	var parseErrors int
	var currentStepID string
	var currentStepIndex int
	var totalSteps int
	var currentStepCached bool

	for scanner.Scan() {
		var event buildEvent

		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			parseErrors++
			logger.Debug().
				Err(err).
				Str("raw", string(scanner.Bytes())).
				Msg("failed to parse build output event")
			if parseErrors > 10 {
				return fmt.Errorf("build output stream appears corrupted: %d consecutive parse failures", parseErrors)
			}
			continue
		}
		parseErrors = 0

		if event.Error != "" {
			if currentStepID != "" {
				onProgress(whail.BuildProgressEvent{
					StepID:     currentStepID,
					StepIndex:  currentStepIndex,
					TotalSteps: totalSteps,
					Status:     whail.BuildStepError,
					Error:      event.Error,
				})
			}
			return fmt.Errorf("build error: %s", event.Error)
		}

		if event.ErrorDetail.Message != "" {
			if currentStepID != "" {
				onProgress(whail.BuildProgressEvent{
					StepID:     currentStepID,
					StepIndex:  currentStepIndex,
					TotalSteps: totalSteps,
					Status:     whail.BuildStepError,
					Error:      event.ErrorDetail.Message,
				})
			}
			return fmt.Errorf("build error: %s", event.ErrorDetail.Message)
		}

		stream := strings.TrimSpace(event.Stream)
		if stream == "" {
			continue
		}

		// Check for step header: "Step N/M : INSTRUCTION args"
		if m := legacyStepRe.FindStringSubmatch(stream); m != nil {
			stepNum := 0
			total := 0
			fmt.Sscanf(m[1], "%d", &stepNum)
			fmt.Sscanf(m[2], "%d", &total)
			totalSteps = total

			// Complete previous step if there was one.
			if currentStepID != "" {
				status := whail.BuildStepComplete
				if currentStepCached {
					status = whail.BuildStepCached
				}
				onProgress(whail.BuildProgressEvent{
					StepID:     currentStepID,
					StepIndex:  currentStepIndex,
					TotalSteps: totalSteps,
					Status:     status,
					Cached:     currentStepCached,
				})
			}

			currentStepIndex = stepNum - 1 // 0-based
			currentStepID = fmt.Sprintf("step-%d", currentStepIndex)
			currentStepCached = false
			stepName := m[3]

			onProgress(whail.BuildProgressEvent{
				StepID:     currentStepID,
				StepName:   stepName,
				StepIndex:  currentStepIndex,
				TotalSteps: totalSteps,
				Status:     whail.BuildStepRunning,
			})
			continue
		}

		// Check for cache hit indicator.
		if strings.HasPrefix(stream, "---> Using cache") && currentStepID != "" {
			currentStepCached = true
			onProgress(whail.BuildProgressEvent{
				StepID:     currentStepID,
				StepIndex:  currentStepIndex,
				TotalSteps: totalSteps,
				Status:     whail.BuildStepCached,
				Cached:     true,
			})
			continue
		}

		// Regular output line for the current step.
		if currentStepID != "" && stream != "" {
			onProgress(whail.BuildProgressEvent{
				StepID:     currentStepID,
				StepIndex:  currentStepIndex,
				TotalSteps: totalSteps,
				Status:     whail.BuildStepRunning,
				LogLine:    stream,
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading build output: %w", err)
	}

	// Complete the final step.
	if currentStepID != "" {
		status := whail.BuildStepComplete
		if currentStepCached {
			status = whail.BuildStepCached
		}
		onProgress(whail.BuildProgressEvent{
			StepID:     currentStepID,
			StepIndex:  currentStepIndex,
			TotalSteps: totalSteps,
			Status:     status,
			Cached:     currentStepCached,
		})
	}

	logger.Debug().Msg("image build complete")
	return nil
}

// Container represents a clawker-managed container with parsed metadata.
type Container struct {
	ID      string
	Name    string
	Project string
	Agent   string
	Image   string
	Workdir string
	Status  string
	Created int64
}

// ListContainers returns all clawker-managed containers.
func (c *Client) ListContainers(ctx context.Context, includeAll bool) ([]Container, error) {
	opts := whail.ContainerListOptions{
		All:     includeAll,
		Filters: ClawkerFilter(),
	}

	result, err := c.ContainerList(ctx, opts)
	if err != nil {
		return nil, err
	}

	return parseContainers(result.Items), nil
}

// ListContainersByProject returns containers for a specific project.
func (c *Client) ListContainersByProject(ctx context.Context, project string, includeAll bool) ([]Container, error) {
	opts := whail.ContainerListOptions{
		All:     includeAll,
		Filters: ProjectFilter(project),
	}

	result, err := c.ContainerList(ctx, opts)
	if err != nil {
		return nil, err
	}

	return parseContainers(result.Items), nil
}

// FindContainerByAgent finds a container by project and agent name.
// Returns the container name, container details, and any error.
// Returns (name, nil, nil) if container not found.
func (c *Client) FindContainerByAgent(ctx context.Context, project, agent string) (string, *container.Summary, error) {
	containerName, err := ContainerName(project, agent)
	if err != nil {
		return "", nil, err
	}
	ctr, err := c.FindContainerByName(ctx, containerName)
	if err != nil {
		// Only treat "not found" as a non-error condition
		var dockerErr *whail.DockerError
		if errors.As(err, &dockerErr) && strings.Contains(dockerErr.Message, "not found") {
			return containerName, nil, nil
		}
		// All other errors should be propagated
		return "", nil, fmt.Errorf("failed to find container %q: %w", containerName, err)
	}
	return containerName, ctr, nil
}

// RemoveContainerWithVolumes removes a container and its associated volumes.
// If force is true, volume cleanup errors are logged but not returned.
// If force is false, volume cleanup errors are returned.
func (c *Client) RemoveContainerWithVolumes(ctx context.Context, containerID string, force bool) error {
	// Get container info to find associated project/agent
	info, err := c.ContainerInspect(ctx, containerID, whail.ContainerInspectOptions{})
	if err != nil {
		return err
	}

	project := info.Container.Config.Labels[LabelProject]
	agent := info.Container.Config.Labels[LabelAgent]

	// Stop container if running
	if info.Container.State.Running {
		timeout := 10
		if _, err := c.ContainerStop(ctx, containerID, &timeout); err != nil {
			if !force {
				return err
			}
			// Graceful stop failed, but force is true so we'll attempt forced removal
			logger.Warn().Err(err).Msg("failed to stop container gracefully, proceeding with forced removal")
		}
	}

	// Remove container
	if _, err := c.ContainerRemove(ctx, containerID, force); err != nil {
		return err
	}

	// Find and remove associated volumes
	if project != "" && agent != "" {
		if err := c.removeAgentVolumes(ctx, project, agent, force); err != nil {
			if !force {
				return fmt.Errorf("container removed but volume cleanup failed: %w", err)
			}
			// Force mode: log but don't fail
			logger.Warn().Err(err).Msg("volume cleanup incomplete")
		}
	}

	return nil
}

// removeAgentVolumes removes all volumes associated with an agent.
// Returns an error if any volume removal fails (unless the volume doesn't exist).
//
// NOTE: Global volumes (e.g. "clawker-globals") are NOT affected by this function.
// Label-based lookup filters by project+agent, which global volumes lack.
// Name fallback only targets "clawker.<project>.<agent>-*" patterns, which don't
// match the "clawker-<purpose>" naming convention used by global volumes.
func (c *Client) removeAgentVolumes(ctx context.Context, project, agent string, force bool) error {
	var errs []string
	removedByLabel := make(map[string]bool)

	// Try label-based lookup first
	volumes, err := c.VolumeList(ctx, map[string]string{
		LabelProject: project,
		LabelAgent:   agent,
	})
	if err != nil {
		logger.Warn().Err(err).Msg("failed to list volumes for cleanup, trying fallback")
		// Continue to fallback - don't return yet
	} else {
		// Only iterate if VolumeList succeeded (volumes.Items is valid)
		for _, vol := range volumes.Items {
			if _, err := c.VolumeRemove(ctx, vol.Name, force); err != nil {
				// Check if it's a "not found" error (shouldn't happen but be safe)
				if !isNotFoundError(err) {
					logger.Warn().Err(err).Str("volume", vol.Name).Msg("failed to remove volume")
					errs = append(errs, fmt.Sprintf("%s: %v", vol.Name, err))
				}
			} else {
				logger.Debug().Str("volume", vol.Name).Msg("removed volume")
				removedByLabel[vol.Name] = true
			}
		}
	}

	// Fallback: try removing by known volume names (for unlabeled volumes or if list failed).
	// Names come from Docker labels on existing resources — should always be valid.
	var knownVolumes []string
	for _, purpose := range []string{"workspace", "config", "history"} {
		vn, vnErr := VolumeName(project, agent, purpose)
		if vnErr != nil {
			logger.Warn().Err(vnErr).Str("purpose", purpose).Msg("skipping volume cleanup: invalid name")
			continue
		}
		knownVolumes = append(knownVolumes, vn)
	}
	for _, volName := range knownVolumes {
		if removedByLabel[volName] {
			continue // Already removed via label lookup
		}
		if _, err := c.VolumeRemove(ctx, volName, force); err != nil {
			// Only report errors that aren't "not found"
			if !isNotFoundError(err) {
				logger.Warn().Err(err).Str("volume", volName).Msg("failed to remove volume")
				errs = append(errs, fmt.Sprintf("%s: %v", volName, err))
			} else {
				logger.Debug().Str("volume", volName).Msg("volume does not exist, skipping")
			}
		} else {
			logger.Debug().Str("volume", volName).Msg("removed volume via name fallback")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to remove %d volume(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}

// isNotFoundError checks if an error indicates a resource was not found.
func isNotFoundError(err error) bool {
	if cerrdefs.IsNotFound(err) {
		return true
	}
	var dockerErr *whail.DockerError
	if errors.As(err, &dockerErr) {
		return strings.Contains(dockerErr.Message, "not found") ||
			strings.Contains(dockerErr.Message, "No such")
	}
	// Also check for raw error message
	return strings.Contains(err.Error(), "not found") ||
		strings.Contains(err.Error(), "No such")
}

// parseContainers converts Docker container list to Container slice.
func parseContainers(containers []container.Summary) []Container {
	var result = make([]Container, 0, len(containers))
	for _, c := range containers {
		// Extract container name (remove leading slash)
		name := ""
		if len(c.Names) > 0 {
			name = c.Names[0]
			if len(name) > 0 && name[0] == '/' {
				name = name[1:]
			}
		}

		// Use label image if set, otherwise fall back to Docker-provided image
		image := c.Labels[LabelImage]
		if image == "" {
			image = c.Image
		}

		result = append(result, Container{
			ID:      c.ID,
			Name:    name,
			Project: c.Labels[LabelProject],
			Agent:   c.Labels[LabelAgent],
			Image:   image,
			Workdir: c.Labels[LabelWorkdir],
			Status:  string(c.State),
			Created: c.Created,
		})
	}

	return result
}
