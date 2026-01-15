package whail

import (
	"context"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ContainerCreate overrides to add managed labels
// The provided labels are merged with the engine's configured labels.
func (e *Engine) ContainerCreate(
	ctx context.Context,
	config *container.Config,
	hostConfig *container.HostConfig,
	networkingConfig *network.NetworkingConfig,
	platform *ocispec.Platform,
	name string,
	extraLabels ...map[string]string,
) (container.CreateResponse, error) {
	// Merge labels: base managed + config + extra + user-provided
	config.Labels = MergeLabels(
		e.containerLabels(extraLabels...),
		config.Labels,
	)

	resp, err := e.APIClient.ContainerCreate(ctx, config, hostConfig, networkingConfig, platform, name)
	if err != nil {
		return container.CreateResponse{}, ErrContainerCreateFailed(err)
	}
	return resp, nil
}

// ContainerStart overrides to check if container is managed before starting.
func (e *Engine) ContainerStart(ctx context.Context, containerID string, opts container.StartOptions) error {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return ErrContainerStartFailed(containerID, err)
	}
	if !isManaged {
		return ErrContainerNotFound(containerID)
	}
	err = e.APIClient.ContainerStart(ctx, containerID, opts)
	if err != nil {
		return ErrContainerStartFailed(containerID, err)
	}
	return nil
}

// ContainerStop stops a container with an optional timeout.
// If timeout is nil, the Docker default is used.
// Only stops managed containers.
func (e *Engine) ContainerStop(ctx context.Context, containerID string, timeout *int) error {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return ErrContainerStopFailed(containerID, err)
	}
	if !isManaged {
		return ErrContainerNotFound(containerID)
	}
	var stopOptions container.StopOptions
	if timeout != nil {
		stopOptions.Timeout = timeout
	}
	return e.APIClient.ContainerStop(ctx, containerID, stopOptions)
}

// ContainerRemove overrides to only remove managed containers.
func (e *Engine) ContainerRemove(ctx context.Context, containerID string, force bool) error {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return ErrContainerRemoveFailed(containerID, err)
	}
	if !isManaged {
		return ErrContainerNotFound(containerID)
	}
	return e.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force:         force,
		RemoveVolumes: false,
	})
}

// ContainerList lists containers matching the filter.
// The managed label filter is automatically injected.
func (e *Engine) ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error) {
	options.Filters = e.injectManagedFilter(options.Filters)
	return e.APIClient.ContainerList(ctx, options)
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
	return e.APIClient.ContainerList(ctx, container.ListOptions{
		All:     all,
		Filters: f,
	})
}

// ContainerInspect inspects a container.
// Only inspects managed containers.
func (e *Engine) ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return types.ContainerJSON{}, ErrContainerInspectFailed(containerID, err)
	}
	if !isManaged {
		return types.ContainerJSON{}, ErrContainerNotFound(containerID)
	}
	return e.APIClient.ContainerInspect(ctx, containerID)
}

// ContainerAttach attaches to a container's TTY.
// Only attaches to managed containers.
func (e *Engine) ContainerAttach(ctx context.Context, containerID string, options container.AttachOptions) (types.HijackedResponse, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return types.HijackedResponse{}, ErrAttachFailed(err)
	}
	if !isManaged {
		return types.HijackedResponse{}, ErrContainerNotFound(containerID)
	}
	resp, err := e.APIClient.ContainerAttach(ctx, containerID, options)
	if err != nil {
		return types.HijackedResponse{}, ErrAttachFailed(err)
	}
	return resp, nil
}

// ContainerWait waits for a container to exit.
// Only waits for managed containers.
func (e *Engine) ContainerWait(ctx context.Context, containerID string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil || !isManaged {
		errCh := make(chan error, 1)
		errCh <- ErrContainerNotFound(containerID)
		close(errCh)
		return nil, errCh
	}
	return e.APIClient.ContainerWait(ctx, containerID, condition)
}

// ContainerLogs streams container logs.
// Only returns logs for managed containers.
func (e *Engine) ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return nil, ErrContainerLogsFailed(containerID, err)
	}
	if !isManaged {
		return nil, ErrContainerNotFound(containerID)
	}
	return e.APIClient.ContainerLogs(ctx, containerID, options)
}

// ContainerResize resizes a container's TTY.
// Only resizes managed containers.
func (e *Engine) ContainerResize(ctx context.Context, containerID string, height, width uint) error {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil || !isManaged {
		return ErrContainerNotFound(containerID)
	}
	return e.APIClient.ContainerResize(ctx, containerID, container.ResizeOptions{
		Height: height,
		Width:  width,
	})
}

// ContainerExecCreate creates an exec instance.
// Only creates exec instances for managed containers.
func (e *Engine) ContainerExecCreate(ctx context.Context, containerID string, opts container.ExecOptions) (types.IDResponse, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil || !isManaged {
		return types.IDResponse{}, ErrContainerNotFound(containerID)
	}
	return e.APIClient.ContainerExecCreate(ctx, containerID, opts)
}

// ContainerExecAttach attaches to an exec instance.
func (e *Engine) ContainerExecAttach(ctx context.Context, execID string, opts container.ExecStartOptions) (types.HijackedResponse, error) {
	return e.APIClient.ContainerExecAttach(ctx, execID, opts)
}

// ContainerExecResize resizes an exec instance's TTY.
func (e *Engine) ContainerExecResize(ctx context.Context, execID string, height, width uint) error {
	return e.APIClient.ContainerExecResize(ctx, execID, container.ResizeOptions{
		Height: height,
		Width:  width,
	})
}

// FindContainerByName finds a managed container by exact name.
// Returns ErrContainerNotFound if not found. Only returns containers with the managed label.
func (e *Engine) FindContainerByName(ctx context.Context, name string) (*types.Container, error) {
	f := e.newManagedFilter()
	f.Add("name", name)

	containers, err := e.APIClient.ContainerList(ctx, container.ListOptions{
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
	info, err := e.APIClient.ContainerInspect(ctx, containerID)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}

	val, ok := info.Config.Labels[e.managedLabelKey]
	return ok && val == e.managedLabelValue, nil
}
