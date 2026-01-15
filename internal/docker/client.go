package docker

import (
	"context"
	"errors"

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
		// Check if it's a "not found" error
		var dockerErr *whail.DockerError
		if errors.As(err, &dockerErr) && dockerErr.Op == "find" {
			return containerName, nil, nil
		}
		return "", nil, err
	}
	return containerName, ctr, nil
}

// RemoveContainerWithVolumes removes a container and its associated volumes.
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
			// Force kill if stop fails
			logger.Warn().Err(err).Msg("failed to stop container gracefully, forcing")
		}
	}

	// Remove container
	if err := c.ContainerRemove(ctx, containerID, force); err != nil {
		return err
	}

	// Find and remove associated volumes
	if project != "" && agent != "" {
		c.removeAgentVolumes(ctx, project, agent, force)
	}

	return nil
}

// removeAgentVolumes removes all volumes associated with an agent.
func (c *Client) removeAgentVolumes(ctx context.Context, project, agent string, force bool) {
	// Try label-based lookup first
	// whail.VolumeList accepts map[string]string for extra labels
	volumes, err := c.VolumeList(ctx, map[string]string{
		LabelProject: project,
		LabelAgent:   agent,
	})
	if err != nil {
		logger.Warn().Err(err).Msg("failed to list volumes for cleanup")
	}

	removedByLabel := make(map[string]bool)
	for _, vol := range volumes.Volumes {
		if err := c.VolumeRemove(ctx, vol.Name, force); err != nil {
			logger.Warn().Err(err).Str("volume", vol.Name).Msg("failed to remove volume")
		} else {
			logger.Debug().Str("volume", vol.Name).Msg("removed volume")
			removedByLabel[vol.Name] = true
		}
	}

	// Fallback: try removing by known volume names (for unlabeled volumes)
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
			// Ignore errors - volume may not exist
			logger.Debug().Str("volume", volName).Err(err).Msg("fallback volume removal skipped")
		} else {
			logger.Debug().Str("volume", volName).Msg("removed volume via name fallback")
		}
	}
}

// parseContainers converts Docker container list to Container slice.
func parseContainers(containers []types.Container) []Container {
	var result []Container
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
