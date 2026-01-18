package whail

import (
	"context"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
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
) (client.ContainerCreateResult, error) {
	// Copy the config to avoid mutating caller's struct.
	// Listen, pal - context is sacred. You don't touch what isn't yours.
	configCopy := *config

	// Merge labels into the copy: base managed + config + extra + user-provided
	configCopy.Labels = MergeLabels(
		e.containerLabels(extraLabels...),
		config.Labels,
	)

	opts := client.ContainerCreateOptions{
		Name:             name,
		Config:           &configCopy,
		HostConfig:       hostConfig,
		NetworkingConfig: networkingConfig,
		Platform:         platform,
	}
	resp, err := e.APIClient.ContainerCreate(ctx, opts)
	if err != nil {
		return client.ContainerCreateResult{}, ErrContainerCreateFailed(err)
	}
	return resp, nil
}

// ContainerStart overrides to check if container is managed before starting.
func (e *Engine) ContainerStart(ctx context.Context, containerID string, opts client.ContainerStartOptions) (client.ContainerStartResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerStartResult{}, ErrContainerStartFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerStartResult{}, ErrContainerNotFound(containerID)
	}
	result, err := e.APIClient.ContainerStart(ctx, containerID, opts)
	if err != nil {
		return client.ContainerStartResult{}, ErrContainerStartFailed(containerID, err)
	}
	return result, nil
}

// ContainerStop stops a container with an optional timeout.
// If timeout is nil, the Docker default is used.
// Only stops managed containers.
func (e *Engine) ContainerStop(ctx context.Context, containerID string, timeout *int) (client.ContainerStopResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerStopResult{}, ErrContainerStopFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerStopResult{}, ErrContainerNotFound(containerID)
	}
	stopOptions := client.ContainerStopOptions{}
	if timeout != nil {
		stopOptions.Timeout = timeout
	}
	result, err := e.APIClient.ContainerStop(ctx, containerID, stopOptions)
	if err != nil {
		return client.ContainerStopResult{}, ErrContainerStopFailed(containerID, err)
	}
	return result, nil
}

// ContainerRemove overrides to only remove managed containers.
func (e *Engine) ContainerRemove(ctx context.Context, containerID string, force bool) (client.ContainerRemoveResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerRemoveResult{}, ErrContainerRemoveFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerRemoveResult{}, ErrContainerNotFound(containerID)
	}
	result, err := e.APIClient.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{
		Force:         force,
		RemoveVolumes: false,
	})
	if err != nil {
		return client.ContainerRemoveResult{}, ErrContainerRemoveFailed(containerID, err)
	}
	return result, nil
}

// ContainerList lists containers matching the filter.
// The managed label filter is automatically injected.
func (e *Engine) ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error) {
	options.Filters = e.injectManagedFilter(options.Filters)
	result, err := e.APIClient.ContainerList(ctx, options)
	if err != nil {
		return client.ContainerListResult{}, ErrContainerListFailed(err)
	}
	return result, nil
}

// ContainerListAll lists all containers (including stopped) with the managed filter.
func (e *Engine) ContainerListAll(ctx context.Context) ([]container.Summary, error) {
	result, err := e.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	return result.Items, nil
}

// ContainerListRunning lists only running containers with the managed filter.
func (e *Engine) ContainerListRunning(ctx context.Context) ([]container.Summary, error) {
	result, err := e.ContainerList(ctx, client.ContainerListOptions{All: false})
	if err != nil {
		return nil, err
	}
	return result.Items, nil
}

// ContainerListByLabels lists containers matching additional label filters.
// The managed label filter is automatically injected.
func (e *Engine) ContainerListByLabels(ctx context.Context, labels map[string]string, all bool) ([]container.Summary, error) {
	f := e.newManagedFilter()
	for k, v := range labels {
		f = f.Add("label", k+"="+v)
	}
	result, err := e.APIClient.ContainerList(ctx, client.ContainerListOptions{
		All:     all,
		Filters: f,
	})
	if err != nil {
		return nil, ErrContainerListFailed(err)
	}
	return result.Items, nil
}

// ContainerInspect inspects a container.
// Only inspects managed containers.
func (e *Engine) ContainerInspect(ctx context.Context, containerID string) (client.ContainerInspectResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerInspectResult{}, ErrContainerInspectFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerInspectResult{}, ErrContainerNotFound(containerID)
	}
	result, err := e.APIClient.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return client.ContainerInspectResult{}, ErrContainerInspectFailed(containerID, err)
	}
	return result, nil
}

// ContainerAttach attaches to a container's TTY.
// Only attaches to managed containers.
func (e *Engine) ContainerAttach(ctx context.Context, containerID string, options client.ContainerAttachOptions) (client.ContainerAttachResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerAttachResult{}, ErrAttachFailed(err)
	}
	if !isManaged {
		return client.ContainerAttachResult{}, ErrContainerNotFound(containerID)
	}
	result, err := e.APIClient.ContainerAttach(ctx, containerID, options)
	if err != nil {
		return client.ContainerAttachResult{}, ErrAttachFailed(err)
	}
	return result, nil
}

// ContainerWait waits for a container to exit.
// Only waits for managed containers.
func (e *Engine) ContainerWait(ctx context.Context, containerID string, condition container.WaitCondition) client.ContainerWaitResult {
	errCh := make(chan error, 1)

	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		errCh <- ErrContainerWaitFailed(containerID, err)
		close(errCh)
		return client.ContainerWaitResult{Result: nil, Error: errCh}
	}
	if !isManaged {
		errCh <- ErrContainerNotFound(containerID)
		close(errCh)
		return client.ContainerWaitResult{Result: nil, Error: errCh}
	}

	// Get result from Docker SDK - new API returns a ContainerWaitResult with Result and Error channels
	waitResult := e.APIClient.ContainerWait(ctx, containerID, client.ContainerWaitOptions{Condition: condition})

	// Wrap errors from the SDK to provide consistent user-friendly messages
	wrappedErrCh := make(chan error, 1)
	go func() {
		defer close(wrappedErrCh)
		if err := <-waitResult.Error; err != nil {
			wrappedErrCh <- ErrContainerWaitFailed(containerID, err)
		}
	}()

	return client.ContainerWaitResult{Result: waitResult.Result, Error: wrappedErrCh}
}

// ContainerLogs streams container logs.
// Only returns logs for managed containers.
func (e *Engine) ContainerLogs(ctx context.Context, containerID string, options client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return nil, ErrContainerLogsFailed(containerID, err)
	}
	if !isManaged {
		return nil, ErrContainerNotManaged(containerID)
	}
	logs, err := e.APIClient.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return nil, ErrContainerLogsFailed(containerID, err)
	}
	return logs, nil
}

// ContainerResize resizes a container's TTY.
// Only resizes managed containers.
func (e *Engine) ContainerResize(ctx context.Context, containerID string, height, width uint) (client.ContainerResizeResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerResizeResult{}, ErrContainerResizeFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerResizeResult{}, ErrContainerNotManaged(containerID)
	}
	result, err := e.APIClient.ContainerResize(ctx, containerID, client.ContainerResizeOptions{
		Height: height,
		Width:  width,
	})
	if err != nil {
		return client.ContainerResizeResult{}, ErrContainerResizeFailed(containerID, err)
	}
	return result, nil
}

// ExecCreate creates an exec instance.
// Only creates exec instances for managed containers.
func (e *Engine) ExecCreate(ctx context.Context, containerID string, opts client.ExecCreateOptions) (client.ExecCreateResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ExecCreateResult{}, ErrExecCreateFailed(containerID, err)
	}
	if !isManaged {
		return client.ExecCreateResult{}, ErrContainerNotManaged(containerID)
	}
	resp, err := e.APIClient.ExecCreate(ctx, containerID, opts)
	if err != nil {
		return client.ExecCreateResult{}, ErrExecCreateFailed(containerID, err)
	}
	return resp, nil
}

// FindContainerByName finds a managed container by exact name.
// Returns ErrContainerNotFound if not found. Only returns containers with the managed label.
func (e *Engine) FindContainerByName(ctx context.Context, name string) (*container.Summary, error) {
	f := e.newManagedFilter()
	f = f.Add("name", name)

	result, err := e.APIClient.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return nil, ErrContainerListFailed(err)
	}

	// Find exact match
	for _, c := range result.Items {
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
	info, err := e.APIClient.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return false, nil
		}
		// Wrap non-NotFound errors for consistent user-friendly messaging
		return false, ErrContainerInspectFailed(containerID, err)
	}

	val, ok := info.Container.Config.Labels[e.managedLabelKey]
	return ok && val == e.managedLabelValue, nil
}

// ContainerKill sends a signal to a container.
// Only kills managed containers. Default signal is SIGKILL.
func (e *Engine) ContainerKill(ctx context.Context, containerID, signal string) (client.ContainerKillResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerKillResult{}, ErrContainerKillFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerKillResult{}, ErrContainerNotFound(containerID)
	}
	if signal == "" {
		signal = "SIGKILL"
	}
	result, err := e.APIClient.ContainerKill(ctx, containerID, client.ContainerKillOptions{Signal: signal})
	if err != nil {
		return client.ContainerKillResult{}, ErrContainerKillFailed(containerID, err)
	}
	return result, nil
}

// ContainerPause pauses a running container.
// Only pauses managed containers.
func (e *Engine) ContainerPause(ctx context.Context, containerID string) (client.ContainerPauseResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerPauseResult{}, ErrContainerPauseFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerPauseResult{}, ErrContainerNotFound(containerID)
	}
	result, err := e.APIClient.ContainerPause(ctx, containerID, client.ContainerPauseOptions{})
	if err != nil {
		return client.ContainerPauseResult{}, ErrContainerPauseFailed(containerID, err)
	}
	return result, nil
}

// ContainerUnpause unpauses a paused container.
// Only unpauses managed containers.
func (e *Engine) ContainerUnpause(ctx context.Context, containerID string) (client.ContainerUnpauseResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerUnpauseResult{}, ErrContainerUnpauseFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerUnpauseResult{}, ErrContainerNotFound(containerID)
	}
	result, err := e.APIClient.ContainerUnpause(ctx, containerID, client.ContainerUnpauseOptions{})
	if err != nil {
		return client.ContainerUnpauseResult{}, ErrContainerUnpauseFailed(containerID, err)
	}
	return result, nil
}

// ContainerRestart restarts a container with an optional timeout.
// If timeout is nil, the Docker default is used.
// Only restarts managed containers.
func (e *Engine) ContainerRestart(ctx context.Context, containerID string, timeout *int) (client.ContainerRestartResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerRestartResult{}, ErrContainerRestartFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerRestartResult{}, ErrContainerNotFound(containerID)
	}
	restartOpts := client.ContainerRestartOptions{}
	if timeout != nil {
		restartOpts.Timeout = timeout
	}
	result, err := e.APIClient.ContainerRestart(ctx, containerID, restartOpts)
	if err != nil {
		return client.ContainerRestartResult{}, ErrContainerRestartFailed(containerID, err)
	}
	return result, nil
}

// ContainerRename renames a container.
// Only renames managed containers.
func (e *Engine) ContainerRename(ctx context.Context, containerID, newName string) (client.ContainerRenameResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerRenameResult{}, ErrContainerRenameFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerRenameResult{}, ErrContainerNotFound(containerID)
	}
	result, err := e.APIClient.ContainerRename(ctx, containerID, client.ContainerRenameOptions{NewName: newName})
	if err != nil {
		return client.ContainerRenameResult{}, ErrContainerRenameFailed(containerID, err)
	}
	return result, nil
}

// ContainerTop returns the running processes in a container.
// Only returns processes for managed containers.
func (e *Engine) ContainerTop(ctx context.Context, containerID string, args []string) (client.ContainerTopResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerTopResult{}, ErrContainerTopFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerTopResult{}, ErrContainerNotFound(containerID)
	}
	top, err := e.APIClient.ContainerTop(ctx, containerID, client.ContainerTopOptions{Arguments: args})
	if err != nil {
		return client.ContainerTopResult{}, ErrContainerTopFailed(containerID, err)
	}
	return top, nil
}

// ContainerStats returns resource usage statistics for a container.
// If stream is true, stats are streamed until the context is cancelled.
// Only returns stats for managed containers.
func (e *Engine) ContainerStats(ctx context.Context, containerID string, stream bool) (client.ContainerStatsResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerStatsResult{}, ErrContainerStatsFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerStatsResult{}, ErrContainerNotFound(containerID)
	}
	result, err := e.APIClient.ContainerStats(ctx, containerID, client.ContainerStatsOptions{Stream: stream})
	if err != nil {
		return client.ContainerStatsResult{}, ErrContainerStatsFailed(containerID, err)
	}
	return result, nil
}

// ContainerStatsOneShot returns a single snapshot of container stats.
// The caller is responsible for closing the Body in the returned io.ReadCloser.
// Only returns stats for managed containers.
func (e *Engine) ContainerStatsOneShot(ctx context.Context, containerID string) (client.ContainerStatsResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerStatsResult{}, ErrContainerStatsFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerStatsResult{}, ErrContainerNotFound(containerID)
	}
	// Use non-streaming mode with IncludePreviousSample for one-shot behavior
	result, err := e.APIClient.ContainerStats(ctx, containerID, client.ContainerStatsOptions{
		Stream:                false,
		IncludePreviousSample: true,
	})
	if err != nil {
		return client.ContainerStatsResult{}, ErrContainerStatsFailed(containerID, err)
	}
	return result, nil
}

// ContainerUpdate updates a container's resource constraints.
// Only updates managed containers.
func (e *Engine) ContainerUpdate(ctx context.Context, containerID string, resources *container.Resources, restartPolicy *container.RestartPolicy) (client.ContainerUpdateResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerUpdateResult{}, ErrContainerUpdateFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerUpdateResult{}, ErrContainerNotFound(containerID)
	}
	opts := client.ContainerUpdateOptions{
		Resources:     resources,
		RestartPolicy: restartPolicy,
	}
	resp, err := e.APIClient.ContainerUpdate(ctx, containerID, opts)
	if err != nil {
		return client.ContainerUpdateResult{}, ErrContainerUpdateFailed(containerID, err)
	}
	return resp, nil
}
