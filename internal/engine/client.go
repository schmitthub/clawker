package engine

import (
	"context"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/schmitthub/clawker/pkg/logger"
)

// Engine wraps the Docker client with Clawker-specific operations
type Engine struct {
	cli *client.Client
}

// NewEngine creates a new Docker engine wrapper
func NewEngine(ctx context.Context) (*Engine, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, ErrDockerNotRunning(err)
	}

	engine := &Engine{
		cli: cli,
	}

	// Verify connection
	if err := engine.HealthCheck(ctx); err != nil {
		cli.Close()
		return nil, err
	}

	logger.Debug().Msg("docker engine connected")

	return engine, nil
}

// HealthCheck verifies engine external dependency health
func (e *Engine) HealthCheck(ctx context.Context) error {
	// check if Docker daemon is reachable
	_, err := e.cli.Ping(ctx)
	if err != nil {
		return ErrDockerNotRunning(err)
	}
	return nil
}

// Close releases Docker client resources
func (e *Engine) Close() error {
	return e.cli.Close()
}

// Client returns the underlying Docker client for advanced operations
func (e *Engine) Client() *client.Client {
	return e.cli
}

// --- Image Operations ---

// ImageExists checks if an image exists locally
func (e *Engine) ImageExists(ctx context.Context, imageRef string) (bool, error) {
	_, _, err := e.cli.ImageInspectWithRaw(ctx, imageRef)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ImagePull pulls an image from a registry
func (e *Engine) ImagePull(ctx context.Context, imageRef string) (io.ReadCloser, error) {
	logger.Debug().Str("image", imageRef).Msg("pulling image")

	reader, err := e.cli.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return nil, ErrImageNotFound(imageRef, err)
	}
	return reader, nil
}

// ImageBuild builds an image from a build context
func (e *Engine) ImageBuild(ctx context.Context, buildContext io.Reader, options types.ImageBuildOptions) (types.ImageBuildResponse, error) {
	logger.Debug().
		Str("dockerfile", options.Dockerfile).
		Strs("tags", options.Tags).
		Msg("building image")

	resp, err := e.cli.ImageBuild(ctx, buildContext, options)
	if err != nil {
		return types.ImageBuildResponse{}, ErrImageBuildFailed(err)
	}
	return resp, nil
}

// ImageRemove removes an image
func (e *Engine) ImageRemove(ctx context.Context, imageID string, force bool) error {
	_, err := e.cli.ImageRemove(ctx, imageID, image.RemoveOptions{Force: force})
	return err
}

// --- Container Operations ---

// ContainerCreate creates a new container
func (e *Engine) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, name string) (container.CreateResponse, error) {
	logger.Debug().
		Str("name", name).
		Str("image", config.Image).
		Msg("creating container")

	resp, err := e.cli.ContainerCreate(ctx, config, hostConfig, nil, nil, name)
	if err != nil {
		return container.CreateResponse{}, ErrContainerCreateFailed(err)
	}
	return resp, nil
}

// ContainerStart starts a container
func (e *Engine) ContainerStart(ctx context.Context, containerID string) error {
	logger.Debug().Str("container", containerID).Msg("starting container")

	err := e.cli.ContainerStart(ctx, containerID, container.StartOptions{})
	if err != nil {
		return ErrContainerStartFailed(containerID, err)
	}
	return nil
}

// ContainerStop stops a container with a timeout
func (e *Engine) ContainerStop(ctx context.Context, containerID string, timeout *int) error {
	logger.Debug().Str("container", containerID).Msg("stopping container")

	var stopOptions container.StopOptions
	if timeout != nil {
		stopOptions.Timeout = timeout
	}

	return e.cli.ContainerStop(ctx, containerID, stopOptions)
}

// ContainerRemove removes a container
func (e *Engine) ContainerRemove(ctx context.Context, containerID string, force bool) error {
	logger.Debug().Str("container", containerID).Bool("force", force).Msg("removing container")

	return e.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force:         force,
		RemoveVolumes: false,
	})
}

// ContainerAttach attaches to a container's TTY
func (e *Engine) ContainerAttach(ctx context.Context, containerID string, options container.AttachOptions) (types.HijackedResponse, error) {
	logger.Debug().Str("container", containerID).Msg("attaching to container")

	resp, err := e.cli.ContainerAttach(ctx, containerID, options)
	if err != nil {
		return types.HijackedResponse{}, ErrAttachFailed(err)
	}
	return resp, nil
}

// ContainerWait waits for a container to exit
func (e *Engine) ContainerWait(ctx context.Context, containerID string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error) {
	return e.cli.ContainerWait(ctx, containerID, condition)
}

// ContainerLogs streams container logs
func (e *Engine) ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error) {
	return e.cli.ContainerLogs(ctx, containerID, options)
}

// ContainerResize resizes a container's TTY
func (e *Engine) ContainerResize(ctx context.Context, containerID string, height, width uint) error {
	return e.cli.ContainerResize(ctx, containerID, container.ResizeOptions{
		Height: height,
		Width:  width,
	})
}

// ContainerInspect inspects a container
func (e *Engine) ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	return e.cli.ContainerInspect(ctx, containerID)
}

// ContainerExecCreate creates an exec instance
func (e *Engine) ContainerExecCreate(ctx context.Context, containerID string, config container.ExecOptions) (types.IDResponse, error) {
	return e.cli.ContainerExecCreate(ctx, containerID, config)
}

// ContainerExecAttach attaches to an exec instance
func (e *Engine) ContainerExecAttach(ctx context.Context, execID string, config container.ExecStartOptions) (types.HijackedResponse, error) {
	return e.cli.ContainerExecAttach(ctx, execID, config)
}

// ContainerExecResize resizes an exec instance's TTY
func (e *Engine) ContainerExecResize(ctx context.Context, execID string, height, width uint) error {
	return e.cli.ContainerExecResize(ctx, execID, container.ResizeOptions{
		Height: height,
		Width:  width,
	})
}

// ContainerList lists containers matching the filter
func (e *Engine) ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error) {
	return e.cli.ContainerList(ctx, options)
}

// FindContainerByName finds a container by name prefix
func (e *Engine) FindContainerByName(ctx context.Context, namePrefix string) (*types.Container, error) {
	containers, err := e.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("name", namePrefix),
		),
	})
	if err != nil {
		return nil, err
	}

	// Find exact match or prefix match
	for _, c := range containers {
		for _, name := range c.Names {
			// Container names have a leading slash
			if name == "/"+namePrefix || name == namePrefix {
				return &c, nil
			}
		}
	}

	return nil, nil
}

func (e *Engine) FindContainerByAgent(ctx context.Context, project string, agent string) (string, *types.Container, error) {
	containerName := ContainerName(project, agent)
	c, err := e.FindContainerByName(ctx, containerName)
	if err != nil {
		return "", nil, err
	}
	return containerName, c, nil
}

// --- Volume Operations ---

// VolumeCreate creates a new volume
func (e *Engine) VolumeCreate(ctx context.Context, name string, labels map[string]string) (volume.Volume, error) {
	logger.Debug().Str("volume", name).Msg("creating volume")

	vol, err := e.cli.VolumeCreate(ctx, volume.CreateOptions{
		Name:   name,
		Labels: labels,
	})
	if err != nil {
		return volume.Volume{}, ErrVolumeCreateFailed(name, err)
	}
	return vol, nil
}

// VolumeRemove removes a volume
func (e *Engine) VolumeRemove(ctx context.Context, name string, force bool) error {
	logger.Debug().Str("volume", name).Bool("force", force).Msg("removing volume")
	return e.cli.VolumeRemove(ctx, name, force)
}

// VolumeInspect inspects a volume
func (e *Engine) VolumeInspect(ctx context.Context, name string) (volume.Volume, error) {
	return e.cli.VolumeInspect(ctx, name)
}

// VolumeExists checks if a volume exists
func (e *Engine) VolumeExists(ctx context.Context, name string) (bool, error) {
	_, err := e.cli.VolumeInspect(ctx, name)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// VolumeList lists volumes matching the filter
func (e *Engine) VolumeList(ctx context.Context, filter filters.Args) (volume.ListResponse, error) {
	return e.cli.VolumeList(ctx, volume.ListOptions{Filters: filter})
}

// --- Network Operations ---

// NetworkExists checks if a network exists
func (e *Engine) NetworkExists(ctx context.Context, name string) (bool, error) {
	_, err := e.cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// NetworkCreate creates a new network
func (e *Engine) NetworkCreate(ctx context.Context, name string) (network.CreateResponse, error) {
	logger.Debug().Str("network", name).Msg("creating network")

	resp, err := e.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{
			"com.clawker.managed": "true",
		},
	})
	if err != nil {
		return network.CreateResponse{}, ErrNetworkCreateFailed(name, err)
	}
	return resp, nil
}

// EnsureNetwork creates a network if it doesn't exist
func (e *Engine) EnsureNetwork(ctx context.Context, name string) error {
	exists, err := e.NetworkExists(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		logger.Debug().Str("network", name).Msg("network already exists")
		return nil
	}
	_, err = e.NetworkCreate(ctx, name)
	return err
}

// NetworkRemove removes a network
func (e *Engine) NetworkRemove(ctx context.Context, name string) error {
	logger.Debug().Str("network", name).Msg("removing network")
	return e.cli.NetworkRemove(ctx, name)
}

// NetworkInspect inspects a network
func (e *Engine) NetworkInspect(ctx context.Context, name string) (network.Inspect, error) {
	return e.cli.NetworkInspect(ctx, name, network.InspectOptions{})
}

// NetworkList lists networks matching the filter
func (e *Engine) NetworkList(ctx context.Context, filter filters.Args) ([]network.Summary, error) {
	return e.cli.NetworkList(ctx, network.ListOptions{Filters: filter})
}

// IsMonitoringActive checks if the clawker monitoring stack is running.
// It looks for the otel-collector container on the clawker-net network.
func (e *Engine) IsMonitoringActive(ctx context.Context) bool {
	// Look for otel-collector container
	containers, err := e.cli.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("name", "otel-collector"),
			filters.Arg("status", "running"),
		),
	})
	if err != nil {
		logger.Debug().Err(err).Msg("failed to check monitoring status")
		return false
	}

	// Check if any otel-collector container is running on clawker-net
	for _, c := range containers {
		if c.NetworkSettings != nil {
			for netName := range c.NetworkSettings.Networks {
				if netName == "clawker-net" {
					logger.Debug().Msg("monitoring stack detected as active")
					return true
				}
			}
		}
	}

	return false
}

// ClawkerContainer represents a clawker-managed container
type ClawkerContainer struct {
	ID      string
	Name    string
	Project string
	Agent   string
	Image   string
	Workdir string
	Status  string
	Created int64
}

// ListClawkerContainers returns all containers with com.clawker.managed=true label
func (e *Engine) ListClawkerContainers(ctx context.Context, includeAll bool) ([]ClawkerContainer, error) {
	opts := container.ListOptions{
		All:     includeAll,
		Filters: ClawkerFilter(),
	}

	containers, err := e.cli.ContainerList(ctx, opts)
	if err != nil {
		return nil, err
	}

	return e.parseClawkerContainers(containers), nil
}

// ListClawkerContainersByProject returns containers for a specific project
func (e *Engine) ListClawkerContainersByProject(ctx context.Context, project string, includeAll bool) ([]ClawkerContainer, error) {
	opts := container.ListOptions{
		All:     includeAll,
		Filters: ProjectFilter(project),
	}

	containers, err := e.cli.ContainerList(ctx, opts)
	if err != nil {
		return nil, err
	}

	return e.parseClawkerContainers(containers), nil
}

// parseClawkerContainers converts Docker container list to ClawkerContainer slice
func (e *Engine) parseClawkerContainers(containers []types.Container) []ClawkerContainer {
	var result []ClawkerContainer
	for _, c := range containers {
		// Extract container name (remove leading slash)
		name := ""
		if len(c.Names) > 0 {
			name = c.Names[0]
			if len(name) > 0 && name[0] == '/' {
				name = name[1:]
			}
		}

		result = append(result, ClawkerContainer{
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

// RemoveContainerWithVolumes removes a container and its associated volumes
func (e *Engine) RemoveContainerWithVolumes(ctx context.Context, containerID string, force bool) error {
	// Get container info to find associated project/agent
	info, err := e.ContainerInspect(ctx, containerID)
	if err != nil {
		return err
	}

	project := info.Config.Labels[LabelProject]
	agent := info.Config.Labels[LabelAgent]

	// Stop container if running
	if info.State.Running {
		timeout := 10
		if err := e.ContainerStop(ctx, containerID, &timeout); err != nil {
			if !force {
				return err
			}
			// Force kill if stop fails
			logger.Warn().Err(err).Msg("failed to stop container gracefully, forcing")
		}
	}

	// Remove container
	if err := e.ContainerRemove(ctx, containerID, force); err != nil {
		return err
	}

	// Find and remove associated volumes
	if project != "" && agent != "" {
		// Try label-based lookup first
		volumes, err := e.VolumeList(ctx, AgentFilter(project, agent))
		if err != nil {
			logger.Warn().Err(err).Msg("failed to list volumes for cleanup")
		}

		removedByLabel := make(map[string]bool)
		for _, vol := range volumes.Volumes {
			if err := e.VolumeRemove(ctx, vol.Name, force); err != nil {
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
			if err := e.VolumeRemove(ctx, volName, force); err != nil {
				// Ignore errors - volume may not exist
				logger.Debug().Str("volume", volName).Err(err).Msg("fallback volume removal skipped")
			} else {
				logger.Debug().Str("volume", volName).Msg("removed volume via name fallback")
			}
		}
	}

	return nil
}

// ListRunningClawkerContainers returns all running containers managed by clawker
// on the clawker-net network. Returns container info including project name.
// Deprecated: Use ListClawkerContainers instead
func (e *Engine) ListRunningClawkerContainers(ctx context.Context) ([]ClawkerContainer, error) {
	return e.ListClawkerContainers(ctx, false)
}
