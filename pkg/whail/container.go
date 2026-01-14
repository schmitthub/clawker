package whail

import (
	"context"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// ContainerCreate creates a new container with managed labels automatically applied.
// The provided labels are merged with the engine's configured labels.
func (e *Engine) ContainerCreate(
	ctx context.Context,
	config *container.Config,
	hostConfig *container.HostConfig,
	name string,
	extraLabels ...map[string]string,
) (container.CreateResponse, error) {
	// Merge labels: base managed + config + extra + user-provided
	config.Labels = MergeLabels(
		e.containerLabels(extraLabels...),
		config.Labels,
	)

	resp, err := e.cli.ContainerCreate(ctx, config, hostConfig, nil, nil, name)
	if err != nil {
		return container.CreateResponse{}, ErrContainerCreateFailed(err)
	}
	return resp, nil
}

// ContainerStart starts a container.
func (e *Engine) ContainerStart(ctx context.Context, containerID string) error {
	err := e.cli.ContainerStart(ctx, containerID, container.StartOptions{})
	if err != nil {
		return ErrContainerStartFailed(containerID, err)
	}
	return nil
}

// ContainerStop stops a container with an optional timeout.
// If timeout is nil, the Docker default is used.
func (e *Engine) ContainerStop(ctx context.Context, containerID string, timeout *int) error {
	var stopOptions container.StopOptions
	if timeout != nil {
		stopOptions.Timeout = timeout
	}
	return e.cli.ContainerStop(ctx, containerID, stopOptions)
}

// ContainerRemove removes a container.
func (e *Engine) ContainerRemove(ctx context.Context, containerID string, force bool) error {
	return e.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force:         force,
		RemoveVolumes: false,
	})
}

// ContainerList lists containers matching the filter.
// The managed label filter is automatically injected.
func (e *Engine) ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error) {
	options.Filters = e.injectManagedFilter(options.Filters)
	return e.cli.ContainerList(ctx, options)
}

// ContainerListAll lists all containers (including stopped) with the managed filter.
func (e *Engine) ContainerListAll(ctx context.Context) ([]types.Container, error) {
	return e.ContainerList(ctx, container.ListOptions{All: true})
}

// ContainerListRunning lists only running containers with the managed filter.
func (e *Engine) ContainerListRunning(ctx context.Context) ([]types.Container, error) {
	return e.ContainerList(ctx, container.ListOptions{All: false})
}

// ContainerListByLabels lists containers matching additional label filters.
// The managed label filter is automatically injected.
func (e *Engine) ContainerListByLabels(ctx context.Context, labels map[string]string, all bool) ([]types.Container, error) {
	f := e.newManagedFilter()
	for k, v := range labels {
		f.Add("label", k+"="+v)
	}
	return e.cli.ContainerList(ctx, container.ListOptions{
		All:     all,
		Filters: f,
	})
}

// ContainerInspect inspects a container.
func (e *Engine) ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	return e.cli.ContainerInspect(ctx, containerID)
}

// ContainerAttach attaches to a container's TTY.
func (e *Engine) ContainerAttach(ctx context.Context, containerID string, options container.AttachOptions) (types.HijackedResponse, error) {
	resp, err := e.cli.ContainerAttach(ctx, containerID, options)
	if err != nil {
		return types.HijackedResponse{}, ErrAttachFailed(err)
	}
	return resp, nil
}

// ContainerWait waits for a container to exit.
func (e *Engine) ContainerWait(ctx context.Context, containerID string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error) {
	return e.cli.ContainerWait(ctx, containerID, condition)
}

// ContainerLogs streams container logs.
func (e *Engine) ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error) {
	return e.cli.ContainerLogs(ctx, containerID, options)
}

// ContainerResize resizes a container's TTY.
func (e *Engine) ContainerResize(ctx context.Context, containerID string, height, width uint) error {
	return e.cli.ContainerResize(ctx, containerID, container.ResizeOptions{
		Height: height,
		Width:  width,
	})
}

// ContainerExecCreate creates an exec instance.
func (e *Engine) ContainerExecCreate(ctx context.Context, containerID string, config container.ExecOptions) (types.IDResponse, error) {
	return e.cli.ContainerExecCreate(ctx, containerID, config)
}

// ContainerExecAttach attaches to an exec instance.
func (e *Engine) ContainerExecAttach(ctx context.Context, execID string, config container.ExecStartOptions) (types.HijackedResponse, error) {
	return e.cli.ContainerExecAttach(ctx, execID, config)
}

// ContainerExecResize resizes an exec instance's TTY.
func (e *Engine) ContainerExecResize(ctx context.Context, execID string, height, width uint) error {
	return e.cli.ContainerExecResize(ctx, execID, container.ResizeOptions{
		Height: height,
		Width:  width,
	})
}

// FindContainerByName finds a container by exact name.
// Returns nil if not found.
func (e *Engine) FindContainerByName(ctx context.Context, name string) (*types.Container, error) {
	containers, err := e.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("name", name),
		),
	})
	if err != nil {
		return nil, err
	}

	// Find exact match
	for _, c := range containers {
		for _, cname := range c.Names {
			// Container names have a leading slash
			if cname == "/"+name || cname == name {
				return &c, nil
			}
		}
	}

	return nil, ErrContainerNotFound(name)
}

// FindManagedContainerByName finds a managed container by exact name.
// Returns ErrContainerNotFound if not found. Only returns containers with the managed label.
func (e *Engine) FindManagedContainerByName(ctx context.Context, name string) (*types.Container, error) {
	f := e.newManagedFilter()
	f.Add("name", name)

	containers, err := e.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return nil, err
	}

	// Find exact match
	for _, c := range containers {
		for _, cname := range c.Names {
			if cname == "/"+name || cname == name {
				return &c, nil
			}
		}
	}

	return nil, ErrContainerNotFound(name)
}

// IsContainerManaged checks if a container has the managed label.
func (e *Engine) IsContainerManaged(ctx context.Context, containerID string) (bool, error) {
	info, err := e.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}

	val, ok := info.Config.Labels[e.managedLabelKey]
	return ok && val == e.managedLabelValue, nil
}
