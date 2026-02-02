package whailtest

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/moby/moby/client"
)

// FakeAPIClient is a test double for client.APIClient using the function-field
// pattern (Docker CLI convention). Each moby method whail calls has a corresponding
// Fn field. If the field is set, the fake delegates to it and records the call.
// If the field is nil, the call panics with "not implemented: MethodName".
//
// The embedded *client.Client (nil) satisfies unexported interface methods.
// Any method not explicitly overridden here will panic on nil dereference,
// providing fail-loud behavior for unexpected calls.
type FakeAPIClient struct {
	// Embed nil *client.Client to satisfy unexported APIClient methods.
	// This is intentionally nil — calling unimplemented methods panics.
	*client.Client

	// mu protects Calls from concurrent access.
	mu sync.Mutex

	// Calls records the method names invoked on this fake, in order.
	Calls []string

	// --- Container methods ---
	ContainerCreateFn   func(ctx context.Context, opts client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	ContainerStartFn    func(ctx context.Context, container string, opts client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerStopFn     func(ctx context.Context, container string, opts client.ContainerStopOptions) (client.ContainerStopResult, error)
	ContainerRemoveFn   func(ctx context.Context, container string, opts client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
	ContainerListFn     func(ctx context.Context, opts client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspectFn  func(ctx context.Context, container string, opts client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerAttachFn   func(ctx context.Context, container string, opts client.ContainerAttachOptions) (client.ContainerAttachResult, error)
	ContainerWaitFn     func(ctx context.Context, container string, opts client.ContainerWaitOptions) client.ContainerWaitResult
	ContainerLogsFn     func(ctx context.Context, container string, opts client.ContainerLogsOptions) (client.ContainerLogsResult, error)
	ContainerResizeFn   func(ctx context.Context, container string, opts client.ContainerResizeOptions) (client.ContainerResizeResult, error)
	ContainerKillFn     func(ctx context.Context, container string, opts client.ContainerKillOptions) (client.ContainerKillResult, error)
	ContainerPauseFn    func(ctx context.Context, container string, opts client.ContainerPauseOptions) (client.ContainerPauseResult, error)
	ContainerUnpauseFn  func(ctx context.Context, container string, opts client.ContainerUnpauseOptions) (client.ContainerUnpauseResult, error)
	ContainerRestartFn  func(ctx context.Context, container string, opts client.ContainerRestartOptions) (client.ContainerRestartResult, error)
	ContainerRenameFn   func(ctx context.Context, container string, opts client.ContainerRenameOptions) (client.ContainerRenameResult, error)
	ContainerTopFn      func(ctx context.Context, container string, opts client.ContainerTopOptions) (client.ContainerTopResult, error)
	ContainerStatsFn    func(ctx context.Context, container string, opts client.ContainerStatsOptions) (client.ContainerStatsResult, error)
	ContainerUpdateFn   func(ctx context.Context, container string, opts client.ContainerUpdateOptions) (client.ContainerUpdateResult, error)
	ContainerStatPathFn func(ctx context.Context, container string, opts client.ContainerStatPathOptions) (client.ContainerStatPathResult, error)

	// --- Exec methods ---
	ExecCreateFn func(ctx context.Context, container string, opts client.ExecCreateOptions) (client.ExecCreateResult, error)

	// --- Copy methods ---
	CopyToContainerFn   func(ctx context.Context, container string, opts client.CopyToContainerOptions) (client.CopyToContainerResult, error)
	CopyFromContainerFn func(ctx context.Context, container string, opts client.CopyFromContainerOptions) (client.CopyFromContainerResult, error)

	// --- Volume methods ---
	VolumeCreateFn  func(ctx context.Context, opts client.VolumeCreateOptions) (client.VolumeCreateResult, error)
	VolumeRemoveFn  func(ctx context.Context, volumeID string, opts client.VolumeRemoveOptions) (client.VolumeRemoveResult, error)
	VolumeInspectFn func(ctx context.Context, volumeID string, opts client.VolumeInspectOptions) (client.VolumeInspectResult, error)
	VolumeListFn    func(ctx context.Context, opts client.VolumeListOptions) (client.VolumeListResult, error)
	VolumePruneFn   func(ctx context.Context, opts client.VolumePruneOptions) (client.VolumePruneResult, error)

	// --- Network methods ---
	NetworkCreateFn     func(ctx context.Context, name string, opts client.NetworkCreateOptions) (client.NetworkCreateResult, error)
	NetworkRemoveFn     func(ctx context.Context, network string, opts client.NetworkRemoveOptions) (client.NetworkRemoveResult, error)
	NetworkInspectFn    func(ctx context.Context, network string, opts client.NetworkInspectOptions) (client.NetworkInspectResult, error)
	NetworkListFn       func(ctx context.Context, opts client.NetworkListOptions) (client.NetworkListResult, error)
	NetworkPruneFn      func(ctx context.Context, opts client.NetworkPruneOptions) (client.NetworkPruneResult, error)
	NetworkConnectFn    func(ctx context.Context, network string, opts client.NetworkConnectOptions) (client.NetworkConnectResult, error)
	NetworkDisconnectFn func(ctx context.Context, network string, opts client.NetworkDisconnectOptions) (client.NetworkDisconnectResult, error)

	// --- Image methods ---
	ImageBuildFn   func(ctx context.Context, buildContext io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error)
	ImageRemoveFn  func(ctx context.Context, image string, opts client.ImageRemoveOptions) (client.ImageRemoveResult, error)
	ImageListFn    func(ctx context.Context, opts client.ImageListOptions) (client.ImageListResult, error)
	ImageInspectFn func(ctx context.Context, image string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImagePruneFn   func(ctx context.Context, opts client.ImagePruneOptions) (client.ImagePruneResult, error)
	ImageTagFn     func(ctx context.Context, opts client.ImageTagOptions) (client.ImageTagResult, error)

	// --- System methods ---
	PingFn func(ctx context.Context, options client.PingOptions) (client.PingResult, error)
}

// record appends a method name to the call log (thread-safe).
func (f *FakeAPIClient) record(method string) {
	f.mu.Lock()
	f.Calls = append(f.Calls, method)
	f.mu.Unlock()
}

// notImplemented panics with a descriptive message for unset function fields.
func notImplemented(method string) {
	panic(fmt.Sprintf("not implemented: %s — set %sFn on FakeAPIClient", method, method))
}

// Reset clears the Calls log.
func (f *FakeAPIClient) Reset() {
	f.mu.Lock()
	f.Calls = nil
	f.mu.Unlock()
}

// --- Container method implementations ---

func (f *FakeAPIClient) ContainerCreate(ctx context.Context, opts client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	if f.ContainerCreateFn == nil {
		notImplemented("ContainerCreate")
	}
	f.record("ContainerCreate")
	return f.ContainerCreateFn(ctx, opts)
}

func (f *FakeAPIClient) ContainerStart(ctx context.Context, container string, opts client.ContainerStartOptions) (client.ContainerStartResult, error) {
	if f.ContainerStartFn == nil {
		notImplemented("ContainerStart")
	}
	f.record("ContainerStart")
	return f.ContainerStartFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerStop(ctx context.Context, container string, opts client.ContainerStopOptions) (client.ContainerStopResult, error) {
	if f.ContainerStopFn == nil {
		notImplemented("ContainerStop")
	}
	f.record("ContainerStop")
	return f.ContainerStopFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerRemove(ctx context.Context, container string, opts client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	if f.ContainerRemoveFn == nil {
		notImplemented("ContainerRemove")
	}
	f.record("ContainerRemove")
	return f.ContainerRemoveFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerList(ctx context.Context, opts client.ContainerListOptions) (client.ContainerListResult, error) {
	if f.ContainerListFn == nil {
		notImplemented("ContainerList")
	}
	f.record("ContainerList")
	return f.ContainerListFn(ctx, opts)
}

func (f *FakeAPIClient) ContainerInspect(ctx context.Context, container string, opts client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	if f.ContainerInspectFn == nil {
		notImplemented("ContainerInspect")
	}
	f.record("ContainerInspect")
	return f.ContainerInspectFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerAttach(ctx context.Context, container string, opts client.ContainerAttachOptions) (client.ContainerAttachResult, error) {
	if f.ContainerAttachFn == nil {
		notImplemented("ContainerAttach")
	}
	f.record("ContainerAttach")
	return f.ContainerAttachFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerWait(ctx context.Context, container string, opts client.ContainerWaitOptions) client.ContainerWaitResult {
	if f.ContainerWaitFn == nil {
		notImplemented("ContainerWait")
	}
	f.record("ContainerWait")
	return f.ContainerWaitFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerLogs(ctx context.Context, container string, opts client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
	if f.ContainerLogsFn == nil {
		notImplemented("ContainerLogs")
	}
	f.record("ContainerLogs")
	return f.ContainerLogsFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerResize(ctx context.Context, container string, opts client.ContainerResizeOptions) (client.ContainerResizeResult, error) {
	if f.ContainerResizeFn == nil {
		notImplemented("ContainerResize")
	}
	f.record("ContainerResize")
	return f.ContainerResizeFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerKill(ctx context.Context, container string, opts client.ContainerKillOptions) (client.ContainerKillResult, error) {
	if f.ContainerKillFn == nil {
		notImplemented("ContainerKill")
	}
	f.record("ContainerKill")
	return f.ContainerKillFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerPause(ctx context.Context, container string, opts client.ContainerPauseOptions) (client.ContainerPauseResult, error) {
	if f.ContainerPauseFn == nil {
		notImplemented("ContainerPause")
	}
	f.record("ContainerPause")
	return f.ContainerPauseFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerUnpause(ctx context.Context, container string, opts client.ContainerUnpauseOptions) (client.ContainerUnpauseResult, error) {
	if f.ContainerUnpauseFn == nil {
		notImplemented("ContainerUnpause")
	}
	f.record("ContainerUnpause")
	return f.ContainerUnpauseFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerRestart(ctx context.Context, container string, opts client.ContainerRestartOptions) (client.ContainerRestartResult, error) {
	if f.ContainerRestartFn == nil {
		notImplemented("ContainerRestart")
	}
	f.record("ContainerRestart")
	return f.ContainerRestartFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerRename(ctx context.Context, container string, opts client.ContainerRenameOptions) (client.ContainerRenameResult, error) {
	if f.ContainerRenameFn == nil {
		notImplemented("ContainerRename")
	}
	f.record("ContainerRename")
	return f.ContainerRenameFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerTop(ctx context.Context, container string, opts client.ContainerTopOptions) (client.ContainerTopResult, error) {
	if f.ContainerTopFn == nil {
		notImplemented("ContainerTop")
	}
	f.record("ContainerTop")
	return f.ContainerTopFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerStats(ctx context.Context, container string, opts client.ContainerStatsOptions) (client.ContainerStatsResult, error) {
	if f.ContainerStatsFn == nil {
		notImplemented("ContainerStats")
	}
	f.record("ContainerStats")
	return f.ContainerStatsFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerUpdate(ctx context.Context, container string, opts client.ContainerUpdateOptions) (client.ContainerUpdateResult, error) {
	if f.ContainerUpdateFn == nil {
		notImplemented("ContainerUpdate")
	}
	f.record("ContainerUpdate")
	return f.ContainerUpdateFn(ctx, container, opts)
}

func (f *FakeAPIClient) ContainerStatPath(ctx context.Context, container string, opts client.ContainerStatPathOptions) (client.ContainerStatPathResult, error) {
	if f.ContainerStatPathFn == nil {
		notImplemented("ContainerStatPath")
	}
	f.record("ContainerStatPath")
	return f.ContainerStatPathFn(ctx, container, opts)
}

// --- Exec method implementations ---

func (f *FakeAPIClient) ExecCreate(ctx context.Context, container string, opts client.ExecCreateOptions) (client.ExecCreateResult, error) {
	if f.ExecCreateFn == nil {
		notImplemented("ExecCreate")
	}
	f.record("ExecCreate")
	return f.ExecCreateFn(ctx, container, opts)
}

// --- Copy method implementations ---

func (f *FakeAPIClient) CopyToContainer(ctx context.Context, container string, opts client.CopyToContainerOptions) (client.CopyToContainerResult, error) {
	if f.CopyToContainerFn == nil {
		notImplemented("CopyToContainer")
	}
	f.record("CopyToContainer")
	return f.CopyToContainerFn(ctx, container, opts)
}

func (f *FakeAPIClient) CopyFromContainer(ctx context.Context, container string, opts client.CopyFromContainerOptions) (client.CopyFromContainerResult, error) {
	if f.CopyFromContainerFn == nil {
		notImplemented("CopyFromContainer")
	}
	f.record("CopyFromContainer")
	return f.CopyFromContainerFn(ctx, container, opts)
}

// --- Volume method implementations ---

func (f *FakeAPIClient) VolumeCreate(ctx context.Context, opts client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
	if f.VolumeCreateFn == nil {
		notImplemented("VolumeCreate")
	}
	f.record("VolumeCreate")
	return f.VolumeCreateFn(ctx, opts)
}

func (f *FakeAPIClient) VolumeRemove(ctx context.Context, volumeID string, opts client.VolumeRemoveOptions) (client.VolumeRemoveResult, error) {
	if f.VolumeRemoveFn == nil {
		notImplemented("VolumeRemove")
	}
	f.record("VolumeRemove")
	return f.VolumeRemoveFn(ctx, volumeID, opts)
}

func (f *FakeAPIClient) VolumeInspect(ctx context.Context, volumeID string, opts client.VolumeInspectOptions) (client.VolumeInspectResult, error) {
	if f.VolumeInspectFn == nil {
		notImplemented("VolumeInspect")
	}
	f.record("VolumeInspect")
	return f.VolumeInspectFn(ctx, volumeID, opts)
}

func (f *FakeAPIClient) VolumeList(ctx context.Context, opts client.VolumeListOptions) (client.VolumeListResult, error) {
	if f.VolumeListFn == nil {
		notImplemented("VolumeList")
	}
	f.record("VolumeList")
	return f.VolumeListFn(ctx, opts)
}

func (f *FakeAPIClient) VolumePrune(ctx context.Context, opts client.VolumePruneOptions) (client.VolumePruneResult, error) {
	if f.VolumePruneFn == nil {
		notImplemented("VolumePrune")
	}
	f.record("VolumePrune")
	return f.VolumePruneFn(ctx, opts)
}

// --- Network method implementations ---

func (f *FakeAPIClient) NetworkCreate(ctx context.Context, name string, opts client.NetworkCreateOptions) (client.NetworkCreateResult, error) {
	if f.NetworkCreateFn == nil {
		notImplemented("NetworkCreate")
	}
	f.record("NetworkCreate")
	return f.NetworkCreateFn(ctx, name, opts)
}

func (f *FakeAPIClient) NetworkRemove(ctx context.Context, network string, opts client.NetworkRemoveOptions) (client.NetworkRemoveResult, error) {
	if f.NetworkRemoveFn == nil {
		notImplemented("NetworkRemove")
	}
	f.record("NetworkRemove")
	return f.NetworkRemoveFn(ctx, network, opts)
}

func (f *FakeAPIClient) NetworkInspect(ctx context.Context, network string, opts client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
	if f.NetworkInspectFn == nil {
		notImplemented("NetworkInspect")
	}
	f.record("NetworkInspect")
	return f.NetworkInspectFn(ctx, network, opts)
}

func (f *FakeAPIClient) NetworkList(ctx context.Context, opts client.NetworkListOptions) (client.NetworkListResult, error) {
	if f.NetworkListFn == nil {
		notImplemented("NetworkList")
	}
	f.record("NetworkList")
	return f.NetworkListFn(ctx, opts)
}

func (f *FakeAPIClient) NetworkPrune(ctx context.Context, opts client.NetworkPruneOptions) (client.NetworkPruneResult, error) {
	if f.NetworkPruneFn == nil {
		notImplemented("NetworkPrune")
	}
	f.record("NetworkPrune")
	return f.NetworkPruneFn(ctx, opts)
}

func (f *FakeAPIClient) NetworkConnect(ctx context.Context, network string, opts client.NetworkConnectOptions) (client.NetworkConnectResult, error) {
	if f.NetworkConnectFn == nil {
		notImplemented("NetworkConnect")
	}
	f.record("NetworkConnect")
	return f.NetworkConnectFn(ctx, network, opts)
}

func (f *FakeAPIClient) NetworkDisconnect(ctx context.Context, network string, opts client.NetworkDisconnectOptions) (client.NetworkDisconnectResult, error) {
	if f.NetworkDisconnectFn == nil {
		notImplemented("NetworkDisconnect")
	}
	f.record("NetworkDisconnect")
	return f.NetworkDisconnectFn(ctx, network, opts)
}

// --- Image method implementations ---

func (f *FakeAPIClient) ImageBuild(ctx context.Context, buildContext io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
	if f.ImageBuildFn == nil {
		notImplemented("ImageBuild")
	}
	f.record("ImageBuild")
	return f.ImageBuildFn(ctx, buildContext, opts)
}

func (f *FakeAPIClient) ImageRemove(ctx context.Context, image string, opts client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
	if f.ImageRemoveFn == nil {
		notImplemented("ImageRemove")
	}
	f.record("ImageRemove")
	return f.ImageRemoveFn(ctx, image, opts)
}

func (f *FakeAPIClient) ImageList(ctx context.Context, opts client.ImageListOptions) (client.ImageListResult, error) {
	if f.ImageListFn == nil {
		notImplemented("ImageList")
	}
	f.record("ImageList")
	return f.ImageListFn(ctx, opts)
}

func (f *FakeAPIClient) ImageInspect(ctx context.Context, image string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	if f.ImageInspectFn == nil {
		notImplemented("ImageInspect")
	}
	f.record("ImageInspect")
	return f.ImageInspectFn(ctx, image, opts...)
}

func (f *FakeAPIClient) ImagePrune(ctx context.Context, opts client.ImagePruneOptions) (client.ImagePruneResult, error) {
	if f.ImagePruneFn == nil {
		notImplemented("ImagePrune")
	}
	f.record("ImagePrune")
	return f.ImagePruneFn(ctx, opts)
}

func (f *FakeAPIClient) ImageTag(ctx context.Context, opts client.ImageTagOptions) (client.ImageTagResult, error) {
	if f.ImageTagFn == nil {
		notImplemented("ImageTag")
	}
	f.record("ImageTag")
	return f.ImageTagFn(ctx, opts)
}

// --- System method implementations ---

func (f *FakeAPIClient) Ping(ctx context.Context, options client.PingOptions) (client.PingResult, error) {
	if f.PingFn == nil {
		notImplemented("Ping")
	}
	f.record("Ping")
	return f.PingFn(ctx, options)
}
