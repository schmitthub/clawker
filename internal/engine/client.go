package engine

import (
	"context"
	"io"

	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
)

// Engine wraps the Docker client with Claucker-specific operations
type Engine struct {
	cli *client.Client
	ctx context.Context
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
		ctx: ctx,
	}

	// Verify connection
	if err := engine.HealthCheck(); err != nil {
		cli.Close()
		return nil, err
	}

	logger.Debug().Msg("docker engine connected")

	return engine, nil
}

// HealthCheck verifies Docker daemon connectivity
func (e *Engine) HealthCheck() error {
	_, err := e.cli.Ping(e.ctx)
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

// Context returns the engine's context
func (e *Engine) Context() context.Context {
	return e.ctx
}

// --- Image Operations ---

// ImageExists checks if an image exists locally
func (e *Engine) ImageExists(imageRef string) (bool, error) {
	_, _, err := e.cli.ImageInspectWithRaw(e.ctx, imageRef)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ImagePull pulls an image from a registry
func (e *Engine) ImagePull(imageRef string) (io.ReadCloser, error) {
	logger.Debug().Str("image", imageRef).Msg("pulling image")

	reader, err := e.cli.ImagePull(e.ctx, imageRef, image.PullOptions{})
	if err != nil {
		return nil, ErrImageNotFound(imageRef, err)
	}
	return reader, nil
}

// ImageBuild builds an image from a build context
func (e *Engine) ImageBuild(buildContext io.Reader, options types.ImageBuildOptions) (types.ImageBuildResponse, error) {
	logger.Debug().
		Str("dockerfile", options.Dockerfile).
		Strs("tags", options.Tags).
		Msg("building image")

	resp, err := e.cli.ImageBuild(e.ctx, buildContext, options)
	if err != nil {
		return types.ImageBuildResponse{}, ErrImageBuildFailed(err)
	}
	return resp, nil
}

// ImageRemove removes an image
func (e *Engine) ImageRemove(imageID string, force bool) error {
	_, err := e.cli.ImageRemove(e.ctx, imageID, image.RemoveOptions{Force: force})
	return err
}

// --- Container Operations ---

// ContainerCreate creates a new container
func (e *Engine) ContainerCreate(config *container.Config, hostConfig *container.HostConfig, name string) (container.CreateResponse, error) {
	logger.Debug().
		Str("name", name).
		Str("image", config.Image).
		Msg("creating container")

	resp, err := e.cli.ContainerCreate(e.ctx, config, hostConfig, nil, nil, name)
	if err != nil {
		return container.CreateResponse{}, ErrContainerCreateFailed(err)
	}
	return resp, nil
}

// ContainerStart starts a container
func (e *Engine) ContainerStart(containerID string) error {
	logger.Debug().Str("container", containerID).Msg("starting container")

	err := e.cli.ContainerStart(e.ctx, containerID, container.StartOptions{})
	if err != nil {
		return ErrContainerStartFailed(containerID, err)
	}
	return nil
}

// ContainerStop stops a container with a timeout
func (e *Engine) ContainerStop(containerID string, timeout *int) error {
	logger.Debug().Str("container", containerID).Msg("stopping container")

	var stopOptions container.StopOptions
	if timeout != nil {
		stopOptions.Timeout = timeout
	}

	return e.cli.ContainerStop(e.ctx, containerID, stopOptions)
}

// ContainerRemove removes a container
func (e *Engine) ContainerRemove(containerID string, force bool) error {
	logger.Debug().Str("container", containerID).Bool("force", force).Msg("removing container")

	return e.cli.ContainerRemove(e.ctx, containerID, container.RemoveOptions{
		Force:         force,
		RemoveVolumes: false,
	})
}

// ContainerAttach attaches to a container's TTY
func (e *Engine) ContainerAttach(containerID string, options container.AttachOptions) (types.HijackedResponse, error) {
	logger.Debug().Str("container", containerID).Msg("attaching to container")

	resp, err := e.cli.ContainerAttach(e.ctx, containerID, options)
	if err != nil {
		return types.HijackedResponse{}, ErrAttachFailed(err)
	}
	return resp, nil
}

// ContainerWait waits for a container to exit
func (e *Engine) ContainerWait(containerID string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error) {
	return e.cli.ContainerWait(e.ctx, containerID, condition)
}

// ContainerLogs streams container logs
func (e *Engine) ContainerLogs(containerID string, options container.LogsOptions) (io.ReadCloser, error) {
	return e.cli.ContainerLogs(e.ctx, containerID, options)
}

// ContainerResize resizes a container's TTY
func (e *Engine) ContainerResize(containerID string, height, width uint) error {
	return e.cli.ContainerResize(e.ctx, containerID, container.ResizeOptions{
		Height: height,
		Width:  width,
	})
}

// ContainerInspect inspects a container
func (e *Engine) ContainerInspect(containerID string) (types.ContainerJSON, error) {
	return e.cli.ContainerInspect(e.ctx, containerID)
}

// ContainerExecCreate creates an exec instance
func (e *Engine) ContainerExecCreate(containerID string, config container.ExecOptions) (types.IDResponse, error) {
	return e.cli.ContainerExecCreate(e.ctx, containerID, config)
}

// ContainerExecAttach attaches to an exec instance
func (e *Engine) ContainerExecAttach(execID string, config container.ExecStartOptions) (types.HijackedResponse, error) {
	return e.cli.ContainerExecAttach(e.ctx, execID, config)
}

// ContainerExecResize resizes an exec instance's TTY
func (e *Engine) ContainerExecResize(execID string, height, width uint) error {
	return e.cli.ContainerExecResize(e.ctx, execID, container.ResizeOptions{
		Height: height,
		Width:  width,
	})
}

// ContainerList lists containers matching the filter
func (e *Engine) ContainerList(options container.ListOptions) ([]types.Container, error) {
	return e.cli.ContainerList(e.ctx, options)
}

// FindContainerByName finds a container by name prefix
func (e *Engine) FindContainerByName(namePrefix string) (*types.Container, error) {
	containers, err := e.cli.ContainerList(e.ctx, container.ListOptions{
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

// --- Volume Operations ---

// VolumeCreate creates a new volume
func (e *Engine) VolumeCreate(name string, labels map[string]string) (volume.Volume, error) {
	logger.Debug().Str("volume", name).Msg("creating volume")

	vol, err := e.cli.VolumeCreate(e.ctx, volume.CreateOptions{
		Name:   name,
		Labels: labels,
	})
	if err != nil {
		return volume.Volume{}, ErrVolumeCreateFailed(name, err)
	}
	return vol, nil
}

// VolumeRemove removes a volume
func (e *Engine) VolumeRemove(name string, force bool) error {
	logger.Debug().Str("volume", name).Bool("force", force).Msg("removing volume")
	return e.cli.VolumeRemove(e.ctx, name, force)
}

// VolumeInspect inspects a volume
func (e *Engine) VolumeInspect(name string) (volume.Volume, error) {
	return e.cli.VolumeInspect(e.ctx, name)
}

// VolumeExists checks if a volume exists
func (e *Engine) VolumeExists(name string) (bool, error) {
	_, err := e.cli.VolumeInspect(e.ctx, name)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// VolumeList lists volumes matching the filter
func (e *Engine) VolumeList(filter filters.Args) (volume.ListResponse, error) {
	return e.cli.VolumeList(e.ctx, volume.ListOptions{Filters: filter})
}
