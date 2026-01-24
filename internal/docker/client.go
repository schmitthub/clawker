package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/pkg/whail"
)

// Client embeds whail.Engine with clawker-specific label configuration.
// All whail.Engine methods are available directly on Client.
// Additional methods provide clawker-specific high-level operations.
type Client struct {
	*whail.Engine
}

// NewClient creates a new clawker Docker client.
// It configures the whail.Engine with clawker's label prefix and conventions.
func NewClient(ctx context.Context) (*Client, error) {
	opts := whail.EngineOptions{
		LabelPrefix:  "com.clawker",
		ManagedLabel: "managed",
	}

	engine, err := whail.NewWithOptions(ctx, opts)
	if err != nil {
		return nil, err
	}

	return &Client{Engine: engine}, nil
}

// Close closes the underlying Docker connection.
func (c *Client) Close() error {
	return c.APIClient.Close()
}

// IsMonitoringActive checks if the clawker monitoring stack is running.
// It looks for the otel-collector container on the clawker-net network.
// Note: This method bypasses whail's label filtering because monitoring
// containers are not clawker-managed resources.
func (c *Client) IsMonitoringActive(ctx context.Context) bool {
	// Use raw API client to bypass managed label filtering
	// since monitoring containers aren't clawker-managed
	f := whail.Filters{}.Add("name", "otel-collector").Add("status", "running")
	result, err := c.APIClient.ContainerList(ctx, whail.ContainerListOptions{
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

// ImageExists checks if an image exists locally.
// Returns true if the image exists, false if not found.
// Note: This bypasses whail's label filtering since images may or may not be managed.
func (c *Client) ImageExists(ctx context.Context, imageRef string) (bool, error) {
	_, err := c.APIClient.ImageInspect(ctx, imageRef)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// BuildImageOpts contains options for building an image.
type BuildImageOpts struct {
	Tags           []string           // -t, --tag (multiple allowed)
	Dockerfile     string             // -f, --file
	BuildArgs      map[string]*string // --build-arg KEY=VALUE
	NoCache        bool               // --no-cache
	Labels         map[string]string  // --label KEY=VALUE (merged with clawker labels)
	Target         string             // --target
	Pull           bool               // --pull (maps to PullParent)
	SuppressOutput bool               // -q, --quiet
	NetworkMode    string             // --network
}

// BuildImage builds a Docker image from a build context.
// It processes the build output and logs progress.
func (c *Client) BuildImage(ctx context.Context, buildContext io.Reader, opts BuildImageOpts) error {
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
			// Only show step progress in debug mode
			if strings.HasPrefix(stream, "Step ") {
				logger.Info().Msg(stream)
			} else {
				logger.Debug().Msg(stream)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading build output: %w", err)
	}

	logger.Info().Msg("image build complete")
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
	containerName := ContainerName(project, agent)
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

	// Fallback: try removing by known volume names (for unlabeled volumes or if list failed)
	knownVolumes := []string{
		VolumeName(project, agent, "workspace"),
		VolumeName(project, agent, "config"),
		VolumeName(project, agent, "history"),
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

		result = append(result, Container{
			ID:      c.ID,
			Name:    name,
			Project: c.Labels[LabelProject],
			Agent:   c.Labels[LabelAgent],
			Image:   c.Labels[LabelImage],
			Workdir: c.Labels[LabelWorkdir],
			Status:  string(c.State),
			Created: c.Created,
		})
	}

	return result
}
