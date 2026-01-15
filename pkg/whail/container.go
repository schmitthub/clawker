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

// ContainerCreate overrides to add managed labels.
// The provided labels are merged with the engine's configured labels.
// Does not mutate the caller's config - creates an internal copy.
func (e *Engine) ContainerCreate(
	ctx context.Context,
	config *container.Config,
	hostConfig *container.HostConfig,
	networkingConfig *network.NetworkingConfig,
	platform *ocispec.Platform,
	name string,
	extraLabels ...map[string]string,
) (container.CreateResponse, error) {
	// Copy the config to avoid mutating caller's struct.
	// Listen, pal - context is sacred. You don't touch what isn't yours.
	configCopy := *config

	// Merge labels into the copy: base managed + config + extra + user-provided
	configCopy.Labels = MergeLabels(
		e.containerLabels(extraLabels...),
		config.Labels,
	)

	resp, err := e.APIClient.ContainerCreate(ctx, &configCopy, hostConfig, networkingConfig, platform, name)
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
	if err := e.APIClient.ContainerStop(ctx, containerID, stopOptions); err != nil {
		return ErrContainerStopFailed(containerID, err)
	}
	return nil
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
	if err := e.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force:         force,
		RemoveVolumes: false,
	}); err != nil {
		return ErrContainerRemoveFailed(containerID, err)
	}
	return nil
}

// ContainerList lists containers matching the filter.
// The managed label filter is automatically injected.
func (e *Engine) ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error) {
	options.Filters = e.injectManagedFilter(options.Filters)
	containers, err := e.APIClient.ContainerList(ctx, options)
	if err != nil {
		return nil, ErrContainerListFailed(err)
	}
	return containers, nil
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
	containers, err := e.APIClient.ContainerList(ctx, container.ListOptions{
		All:     all,
		Filters: f,
	})
	if err != nil {
		return nil, ErrContainerListFailed(err)
	}
	return containers, nil
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
	result, err := e.APIClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return types.ContainerJSON{}, ErrContainerInspectFailed(containerID, err)
	}
	return result, nil
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
	errCh := make(chan error, 1)

	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		errCh <- ErrContainerWaitFailed(containerID, err)
		close(errCh)
		return nil, errCh
	}
	if !isManaged {
		errCh <- ErrContainerNotFound(containerID)
		close(errCh)
		return nil, errCh
	}

	// Get channels from Docker SDK
	waitCh, rawErrCh := e.APIClient.ContainerWait(ctx, containerID, condition)

	// Wrap errors from the SDK to provide consistent user-friendly messages
	wrappedErrCh := make(chan error, 1)
	go func() {
		defer close(wrappedErrCh)
		if err := <-rawErrCh; err != nil {
			wrappedErrCh <- ErrContainerWaitFailed(containerID, err)
		}
	}()

	return waitCh, wrappedErrCh
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
	logs, err := e.APIClient.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return nil, ErrContainerLogsFailed(containerID, err)
	}
	return logs, nil
}

// ContainerResize resizes a container's TTY.
// Only resizes managed containers.
func (e *Engine) ContainerResize(ctx context.Context, containerID string, height, width uint) error {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return ErrContainerResizeFailed(containerID, err)
	}
	if !isManaged {
		return ErrContainerNotFound(containerID)
	}
	if err := e.APIClient.ContainerResize(ctx, containerID, container.ResizeOptions{
		Height: height,
		Width:  width,
	}); err != nil {
		return ErrContainerResizeFailed(containerID, err)
	}
	return nil
}

// ContainerExecCreate creates an exec instance.
// Only creates exec instances for managed containers.
func (e *Engine) ContainerExecCreate(ctx context.Context, containerID string, opts container.ExecOptions) (types.IDResponse, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return types.IDResponse{}, ErrContainerExecFailed(containerID, err)
	}
	if !isManaged {
		return types.IDResponse{}, ErrContainerNotFound(containerID)
	}
	resp, err := e.APIClient.ContainerExecCreate(ctx, containerID, opts)
	if err != nil {
		return types.IDResponse{}, ErrContainerExecFailed(containerID, err)
	}
	return resp, nil
}

// ContainerExecAttach attaches to an exec instance.
func (e *Engine) ContainerExecAttach(ctx context.Context, execID string, opts container.ExecStartOptions) (types.HijackedResponse, error) {
	resp, err := e.APIClient.ContainerExecAttach(ctx, execID, opts)
	if err != nil {
		return types.HijackedResponse{}, ErrExecAttachFailed(execID, err)
	}
	return resp, nil
}

// ContainerExecResize resizes an exec instance's TTY.
func (e *Engine) ContainerExecResize(ctx context.Context, execID string, height, width uint) error {
	if err := e.APIClient.ContainerExecResize(ctx, execID, container.ResizeOptions{
		Height: height,
		Width:  width,
	}); err != nil {
		return ErrExecResizeFailed(execID, err)
	}
	return nil
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
		return nil, ErrContainerListFailed(err)
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
		// Wrap non-NotFound errors for consistent user-friendly messaging
		return false, ErrContainerInspectFailed(containerID, err)
	}

	val, ok := info.Config.Labels[e.managedLabelKey]
	return ok && val == e.managedLabelValue, nil
}

// ContainerKill sends a signal to a container.
// Only kills managed containers. Default signal is SIGKILL.
func (e *Engine) ContainerKill(ctx context.Context, containerID, signal string) error {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return ErrContainerKillFailed(containerID, err)
	}
	if !isManaged {
		return ErrContainerNotFound(containerID)
	}
	if signal == "" {
		signal = "SIGKILL"
	}
	if err := e.APIClient.ContainerKill(ctx, containerID, signal); err != nil {
		return ErrContainerKillFailed(containerID, err)
	}
	return nil
}

// ContainerPause pauses a running container.
// Only pauses managed containers.
func (e *Engine) ContainerPause(ctx context.Context, containerID string) error {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return ErrContainerPauseFailed(containerID, err)
	}
	if !isManaged {
		return ErrContainerNotFound(containerID)
	}
	if err := e.APIClient.ContainerPause(ctx, containerID); err != nil {
		return ErrContainerPauseFailed(containerID, err)
	}
	return nil
}

// ContainerUnpause unpauses a paused container.
// Only unpauses managed containers.
func (e *Engine) ContainerUnpause(ctx context.Context, containerID string) error {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return ErrContainerUnpauseFailed(containerID, err)
	}
	if !isManaged {
		return ErrContainerNotFound(containerID)
	}
	if err := e.APIClient.ContainerUnpause(ctx, containerID); err != nil {
		return ErrContainerUnpauseFailed(containerID, err)
	}
	return nil
}

// ContainerRestart restarts a container with an optional timeout.
// If timeout is nil, the Docker default is used.
// Only restarts managed containers.
func (e *Engine) ContainerRestart(ctx context.Context, containerID string, timeout *int) error {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return ErrContainerRestartFailed(containerID, err)
	}
	if !isManaged {
		return ErrContainerNotFound(containerID)
	}
	var stopOptions container.StopOptions
	if timeout != nil {
		stopOptions.Timeout = timeout
	}
	if err := e.APIClient.ContainerRestart(ctx, containerID, stopOptions); err != nil {
		return ErrContainerRestartFailed(containerID, err)
	}
	return nil
}

// ContainerRename renames a container.
// Only renames managed containers.
func (e *Engine) ContainerRename(ctx context.Context, containerID, newName string) error {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return ErrContainerRenameFailed(containerID, err)
	}
	if !isManaged {
		return ErrContainerNotFound(containerID)
	}
	if err := e.APIClient.ContainerRename(ctx, containerID, newName); err != nil {
		return ErrContainerRenameFailed(containerID, err)
	}
	return nil
}

// ContainerTop returns the running processes in a container.
// Only returns processes for managed containers.
func (e *Engine) ContainerTop(ctx context.Context, containerID string, args []string) (container.ContainerTopOKBody, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return container.ContainerTopOKBody{}, ErrContainerTopFailed(containerID, err)
	}
	if !isManaged {
		return container.ContainerTopOKBody{}, ErrContainerNotFound(containerID)
	}
	top, err := e.APIClient.ContainerTop(ctx, containerID, args)
	if err != nil {
		return container.ContainerTopOKBody{}, ErrContainerTopFailed(containerID, err)
	}
	return top, nil
}

// ContainerStats returns resource usage statistics for a container.
// If stream is true, stats are streamed until the context is cancelled.
// Only returns stats for managed containers.
func (e *Engine) ContainerStats(ctx context.Context, containerID string, stream bool) (io.ReadCloser, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return nil, ErrContainerStatsFailed(containerID, err)
	}
	if !isManaged {
		return nil, ErrContainerNotFound(containerID)
	}
	stats, err := e.APIClient.ContainerStats(ctx, containerID, stream)
	if err != nil {
		return nil, ErrContainerStatsFailed(containerID, err)
	}
	return stats.Body, nil
}

// ContainerStatsOneShot returns a single snapshot of container stats.
// The caller is responsible for closing the Body in the returned StatsResponseReader.
// Only returns stats for managed containers.
func (e *Engine) ContainerStatsOneShot(ctx context.Context, containerID string) (container.StatsResponseReader, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return container.StatsResponseReader{}, ErrContainerStatsFailed(containerID, err)
	}
	if !isManaged {
		return container.StatsResponseReader{}, ErrContainerNotFound(containerID)
	}
	stats, err := e.APIClient.ContainerStatsOneShot(ctx, containerID)
	if err != nil {
		return container.StatsResponseReader{}, ErrContainerStatsFailed(containerID, err)
	}
	return stats, nil
}

// ContainerUpdate updates a container's resource constraints.
// Only updates managed containers.
func (e *Engine) ContainerUpdate(ctx context.Context, containerID string, updateConfig container.UpdateConfig) (container.ContainerUpdateOKBody, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return container.ContainerUpdateOKBody{}, ErrContainerUpdateFailed(containerID, err)
	}
	if !isManaged {
		return container.ContainerUpdateOKBody{}, ErrContainerNotFound(containerID)
	}
	resp, err := e.APIClient.ContainerUpdate(ctx, containerID, updateConfig)
	if err != nil {
		return container.ContainerUpdateOKBody{}, ErrContainerUpdateFailed(containerID, err)
	}
	return resp, nil
}
