package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/schmitthub/clawker/pkg/logger"
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
	opts := container.ListOptions{
		All:     includeAll,
		Filters: ClawkerFilter(),
	}

	containers, err := c.ContainerList(ctx, opts)
	if err != nil {
		return nil, err
	}

	return parseContainers(containers), nil
}

// ListContainersByProject returns containers for a specific project.
func (c *Client) ListContainersByProject(ctx context.Context, project string, includeAll bool) ([]Container, error) {
	opts := container.ListOptions{
		All:     includeAll,
		Filters: ProjectFilter(project),
	}

	containers, err := c.ContainerList(ctx, opts)
	if err != nil {
		return nil, err
	}

	return parseContainers(containers), nil
}

// FindContainerByAgent finds a container by project and agent name.
// Returns the container name, container details, and any error.
// Returns (name, nil, nil) if container not found.
func (c *Client) FindContainerByAgent(ctx context.Context, project, agent string) (string, *types.Container, error) {
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
	info, err := c.ContainerInspect(ctx, containerID)
	if err != nil {
		return err
	}

	project := info.Config.Labels[LabelProject]
	agent := info.Config.Labels[LabelAgent]

	// Stop container if running
	if info.State.Running {
		timeout := 10
		if err := c.ContainerStop(ctx, containerID, &timeout); err != nil {
			if !force {
				return err
			}
			// Graceful stop failed, but force is true so we'll attempt forced removal
			logger.Warn().Err(err).Msg("failed to stop container gracefully, proceeding with forced removal")
		}
	}

	// Remove container
	if err := c.ContainerRemove(ctx, containerID, force); err != nil {
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
		// Only iterate if VolumeList succeeded (volumes.Volumes is valid)
		for _, vol := range volumes.Volumes {
			if err := c.VolumeRemove(ctx, vol.Name, force); err != nil {
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
		if err := c.VolumeRemove(ctx, volName, force); err != nil {
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
func parseContainers(containers []types.Container) []Container {
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
			Status:  c.State,
			Created: c.Created,
		})
	}

	return result
}
