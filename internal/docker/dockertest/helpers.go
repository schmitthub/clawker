package dockertest

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/docker/docker/pkg/stdcopy"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/api/types/container"
	dockerimage "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
)

// defaultCfg provides label values for standalone fixture functions.
// Uses mock config so callers don't need to pass config explicitly.
var defaultCfg = config.NewMockConfig()

// ContainerFixture builds a container.Summary with proper clawker labels.
// The container is in "exited" state by default.
func ContainerFixture(project, agent, image string) container.Summary {
	name, err := docker.ContainerName(project, agent)
	if err != nil {
		panic(fmt.Sprintf("ContainerFixture: invalid inputs (project=%q, agent=%q): %v", project, agent, err))
	}
	labels := map[string]string{
		defaultCfg.LabelManaged(): defaultCfg.ManagedLabelValue(),
		defaultCfg.LabelAgent():   agent,
		defaultCfg.LabelImage():   image,
	}
	if project != "" {
		labels[defaultCfg.LabelProject()] = project
	}

	return container.Summary{
		ID:     "sha256:" + name + "-fake-id",
		Names:  []string{"/" + name},
		Image:  image,
		State:  "exited",
		Labels: labels,
	}
}

// RunningContainerFixture builds a container.Summary in "running" state
// with a default image of "node:20-slim".
func RunningContainerFixture(project, agent string) container.Summary {
	c := ContainerFixture(project, agent, "node:20-slim")
	c.State = "running"
	return c
}

// SetupContainerList configures the fake to return the given containers
// from ContainerList calls. The whail jail will inject the managed label
// filter automatically, so callers only need to provide containers with
// proper clawker labels.
func (f *FakeClient) SetupContainerList(containers ...container.Summary) {
	f.FakeAPI.ContainerListFn = func(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{
			Items: containers,
		}, nil
	}
}

// SetupContainerListError configures the fake to return an error from ContainerList calls.
func (f *FakeClient) SetupContainerListError(err error) {
	f.FakeAPI.ContainerListFn = func(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{}, err
	}
}

// SetupFindContainer configures the fake so that FindContainerByAgent
// returns the given container when the matching name is inspected.
// It sets up ContainerInspect to return managed inspect data plus a
// ContainerList that includes the container (FindContainerByName uses
// list + name filter internally).
func (f *FakeClient) SetupFindContainer(name string, c container.Summary) {
	// FindContainerByName uses ContainerList with name filter
	f.FakeAPI.ContainerListFn = func(_ context.Context, opts client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{
			Items: []container.Summary{c},
		}, nil
	}

	// ContainerInspect is used by whail's jail to verify management
	f.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		if id != c.ID {
			return client.ContainerInspectResult{}, notFoundError(id)
		}
		return client.ContainerInspectResult{
			Container: container.InspectResponse{
				ID:   c.ID,
				Name: "/" + name,
				Config: &container.Config{
					Image:  c.Image,
					Labels: c.Labels,
				},
			},
		}, nil
	}
}

// SetupImageExists configures the fake to report whether a managed image exists.
// When exists is true, ImageInspect returns a managed result with clawker labels.
// When exists is false, ImageInspect returns a not-found error.
func (f *FakeClient) SetupImageExists(ref string, exists bool) {
	f.FakeAPI.ImageInspectFn = func(_ context.Context, image string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		if image != ref {
			return client.ImageInspectResult{}, notFoundError(image)
		}
		if exists {
			return managedImageInspect(f.Cfg, ref), nil
		}
		return client.ImageInspectResult{}, notFoundError(image)
	}
}

// SetupImageTag configures the fake to succeed on ImageTag.
// It wires both ImageTag and ImageInspect (for managed label check).
func (f *FakeClient) SetupImageTag() {
	f.FakeAPI.ImageTagFn = func(_ context.Context, _ client.ImageTagOptions) (client.ImageTagResult, error) {
		return client.ImageTagResult{}, nil
	}
	// ImageTag goes through whail's managed check which calls ImageInspect.
	// Restore the default managed ImageInspect so the check passes.
	f.FakeAPI.ImageInspectFn = func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		return managedImageInspect(f.Cfg, ref), nil
	}
}

// SetupContainerCreate configures the fake to succeed on ContainerCreate,
// returning a container with the given fake ID.
func (f *FakeClient) SetupContainerCreate() {
	f.FakeAPI.ContainerCreateFn = func(_ context.Context, _ client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
		return client.ContainerCreateResult{
			ID: "sha256:fakecontainer1234567890abcdef",
		}, nil
	}
}

// SetupContainerStart configures the fake to succeed on ContainerStart.
func (f *FakeClient) SetupContainerStart() {
	f.FakeAPI.ContainerStartFn = func(_ context.Context, _ string, _ client.ContainerStartOptions) (client.ContainerStartResult, error) {
		return client.ContainerStartResult{}, nil
	}
}

// SetupCopyToContainer configures the fake to succeed on CopyToContainer.
func (f *FakeClient) SetupCopyToContainer() {
	f.FakeAPI.CopyToContainerFn = func(_ context.Context, _ string, _ client.CopyToContainerOptions) (client.CopyToContainerResult, error) {
		return client.CopyToContainerResult{}, nil
	}
}

// SetupVolumeExists configures the fake to report whether a volume exists.
// When exists is true, VolumeInspect returns a managed volume.
// When exists is false, VolumeInspect returns a not-found error.
// If name is empty, the behavior applies to all volume names.
func (f *FakeClient) SetupVolumeExists(name string, exists bool) {
	f.FakeAPI.VolumeInspectFn = func(_ context.Context, volumeID string, _ client.VolumeInspectOptions) (client.VolumeInspectResult, error) {
		if name != "" && volumeID != name {
			return client.VolumeInspectResult{}, notFoundError(volumeID)
		}
		if exists {
			return client.VolumeInspectResult{
				Volume: volume.Volume{
					Name: volumeID,
					Labels: map[string]string{
						f.Cfg.LabelManaged(): f.Cfg.ManagedLabelValue(),
					},
				},
			}, nil
		}
		return client.VolumeInspectResult{}, notFoundError(volumeID)
	}
}

// SetupNetworkExists configures the fake to report whether a network exists.
// When exists is true, NetworkInspect returns a managed network.
// When exists is false, NetworkInspect returns a not-found error.
// If name is empty, the behavior applies to all network names.
func (f *FakeClient) SetupNetworkExists(name string, exists bool) {
	f.FakeAPI.NetworkInspectFn = func(_ context.Context, networkName string, _ client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
		if name != "" && networkName != name {
			return client.NetworkInspectResult{}, notFoundError(networkName)
		}
		if exists {
			return client.NetworkInspectResult{
				Network: network.Inspect{
					Network: network.Network{
						Name: networkName,
						ID:   "net-" + networkName,
						Labels: map[string]string{
							f.Cfg.LabelManaged(): f.Cfg.ManagedLabelValue(),
						},
					},
				},
			}, nil
		}
		return client.NetworkInspectResult{}, notFoundError(networkName)
	}
}

// SetupVolumeCreate configures the fake to succeed on VolumeCreate,
// returning a volume with the requested name and managed labels.
func (f *FakeClient) SetupVolumeCreate() {
	f.FakeAPI.VolumeCreateFn = func(_ context.Context, opts client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
		return client.VolumeCreateResult{
			Volume: volume.Volume{
				Name:   opts.Name,
				Labels: opts.Labels,
			},
		}, nil
	}
}

// SetupNetworkCreate configures the fake to succeed on NetworkCreate.
func (f *FakeClient) SetupNetworkCreate() {
	f.FakeAPI.NetworkCreateFn = func(_ context.Context, name string, _ client.NetworkCreateOptions) (client.NetworkCreateResult, error) {
		return client.NetworkCreateResult{
			ID: "net-" + name,
		}, nil
	}
}

// SetupContainerAttach configures the fake to succeed on ContainerAttach,
// returning a HijackedResponse backed by a net.Pipe. The server side of the
// pipe is closed immediately, simulating a container that exits right away.
func (f *FakeClient) SetupContainerAttach() {
	f.FakeAPI.ContainerAttachFn = func(_ context.Context, _ string, _ client.ContainerAttachOptions) (client.ContainerAttachResult, error) {
		// net.Pipe creates a synchronous in-memory connection pair.
		// Close the server side so reads on the client side return EOF.
		clientConn, serverConn := net.Pipe()
		serverConn.Close()
		return client.ContainerAttachResult{
			HijackedResponse: client.NewHijackedResponse(clientConn, "application/vnd.docker.raw-stream"),
		}, nil
	}
}

// SetupContainerAttachWithOutput configures the fake to return a hijacked
// connection for ContainerAttach that writes the given data as stdcopy-framed
// stdout. This allows StartContainer (which uses stdcopy.StdCopy) to demux
// the output correctly. The server side is closed after writing.
func (f *FakeClient) SetupContainerAttachWithOutput(data string) {
	f.FakeAPI.ContainerAttachFn = func(_ context.Context, _ string, _ client.ContainerAttachOptions) (client.ContainerAttachResult, error) {
		clientConn, serverConn := net.Pipe()
		go func() {
			defer serverConn.Close()
			w := stdcopy.NewStdWriter(serverConn, stdcopy.Stdout)
			_, _ = w.Write([]byte(data))
		}()
		return client.ContainerAttachResult{
			HijackedResponse: client.NewHijackedResponse(clientConn, "application/vnd.docker.multiplexed-stream"),
		}, nil
	}
}

// SetupContainerWait configures the fake to succeed on ContainerWait,
// returning the given exit code immediately.
func (f *FakeClient) SetupContainerWait(exitCode int64) {
	f.FakeAPI.ContainerWaitFn = func(_ context.Context, _ string, _ client.ContainerWaitOptions) client.ContainerWaitResult {
		return whailtest.FakeContainerWaitExit(exitCode)
	}
}

// SetupContainerResize configures the fake to succeed on ContainerResize.
func (f *FakeClient) SetupContainerResize() {
	f.FakeAPI.ContainerResizeFn = func(_ context.Context, _ string, _ client.ContainerResizeOptions) (client.ContainerResizeResult, error) {
		return client.ContainerResizeResult{}, nil
	}
}

// SetupContainerRemove configures the fake to succeed on ContainerRemove.
func (f *FakeClient) SetupContainerRemove() {
	f.FakeAPI.ContainerRemoveFn = func(_ context.Context, _ string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
		return client.ContainerRemoveResult{}, nil
	}
}

// SetupContainerStop configures the fake to succeed on ContainerStop.
func (f *FakeClient) SetupContainerStop() {
	f.FakeAPI.ContainerStopFn = func(_ context.Context, _ string, _ client.ContainerStopOptions) (client.ContainerStopResult, error) {
		return client.ContainerStopResult{}, nil
	}
}

// SetupContainerKill configures the fake to succeed on ContainerKill.
func (f *FakeClient) SetupContainerKill() {
	f.FakeAPI.ContainerKillFn = func(_ context.Context, _ string, _ client.ContainerKillOptions) (client.ContainerKillResult, error) {
		return client.ContainerKillResult{}, nil
	}
}

// SetupContainerPause configures the fake to succeed on ContainerPause.
func (f *FakeClient) SetupContainerPause() {
	f.FakeAPI.ContainerPauseFn = func(_ context.Context, _ string, _ client.ContainerPauseOptions) (client.ContainerPauseResult, error) {
		return client.ContainerPauseResult{}, nil
	}
}

// SetupContainerUnpause configures the fake to succeed on ContainerUnpause.
func (f *FakeClient) SetupContainerUnpause() {
	f.FakeAPI.ContainerUnpauseFn = func(_ context.Context, _ string, _ client.ContainerUnpauseOptions) (client.ContainerUnpauseResult, error) {
		return client.ContainerUnpauseResult{}, nil
	}
}

// SetupContainerRename configures the fake to succeed on ContainerRename.
func (f *FakeClient) SetupContainerRename() {
	f.FakeAPI.ContainerRenameFn = func(_ context.Context, _ string, _ client.ContainerRenameOptions) (client.ContainerRenameResult, error) {
		return client.ContainerRenameResult{}, nil
	}
}

// SetupContainerRestart configures the fake to succeed on ContainerRestart.
func (f *FakeClient) SetupContainerRestart() {
	f.FakeAPI.ContainerRestartFn = func(_ context.Context, _ string, _ client.ContainerRestartOptions) (client.ContainerRestartResult, error) {
		return client.ContainerRestartResult{}, nil
	}
}

// SetupContainerUpdate configures the fake to succeed on ContainerUpdate.
func (f *FakeClient) SetupContainerUpdate() {
	f.FakeAPI.ContainerUpdateFn = func(_ context.Context, _ string, _ client.ContainerUpdateOptions) (client.ContainerUpdateResult, error) {
		return client.ContainerUpdateResult{}, nil
	}
}

// SetupContainerInspect configures the fake to return inspect data for the
// given container ID. Unlike SetupFindContainer (which also wires ContainerList
// for find-by-name), this only wires ContainerInspect — suitable for commands
// that already have a container ID and just need inspect data.
func (f *FakeClient) SetupContainerInspect(containerID string, c container.Summary) {
	f.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		if id != c.ID && id != containerID {
			return client.ContainerInspectResult{}, notFoundError(id)
		}
		name := containerID
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		return client.ContainerInspectResult{
			Container: container.InspectResponse{
				ID:   c.ID,
				Name: "/" + name,
				Config: &container.Config{
					Image:  c.Image,
					Labels: c.Labels,
				},
				State: &container.State{
					Status:  c.State,
					Running: c.State == "running",
				},
			},
		}, nil
	}
}

// SetupContainerLogs configures the fake to return the given string as log
// output. The logs are returned as a plain io.ReadCloser (suitable for
// non-multiplexed TTY output).
func (f *FakeClient) SetupContainerLogs(logs string) {
	f.FakeAPI.ContainerLogsFn = func(_ context.Context, _ string, _ client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
		return io.NopCloser(strings.NewReader(logs)), nil
	}
}

// SetupContainerTop configures the fake to return the given process table.
func (f *FakeClient) SetupContainerTop(titles []string, processes [][]string) {
	f.FakeAPI.ContainerTopFn = func(_ context.Context, _ string, _ client.ContainerTopOptions) (client.ContainerTopResult, error) {
		return client.ContainerTopResult{
			Titles:    titles,
			Processes: processes,
		}, nil
	}
}

// SetupContainerStats configures the fake to return a single JSON stats
// response. The body is a one-shot io.ReadCloser containing the given JSON.
// Pass an empty string for a minimal default stats response.
func (f *FakeClient) SetupContainerStats(statsJSON string) {
	if statsJSON == "" {
		statsJSON = `{"read":"2024-01-01T00:00:00Z","cpu_stats":{},"memory_stats":{}}`
	}
	f.FakeAPI.ContainerStatsFn = func(_ context.Context, _ string, _ client.ContainerStatsOptions) (client.ContainerStatsResult, error) {
		return client.ContainerStatsResult{
			Body: io.NopCloser(strings.NewReader(statsJSON)),
		}, nil
	}
}

// SetupCopyFromContainer configures the fake to succeed on CopyFromContainer,
// returning an empty tar stream.
func (f *FakeClient) SetupCopyFromContainer() {
	f.FakeAPI.CopyFromContainerFn = func(_ context.Context, _ string, _ client.CopyFromContainerOptions) (client.CopyFromContainerResult, error) {
		return client.CopyFromContainerResult{
			Content: io.NopCloser(strings.NewReader("")),
		}, nil
	}
}

// SetupExecCreate configures the fake to succeed on ExecCreate, returning
// the given exec ID.
func (f *FakeClient) SetupExecCreate(execID string) {
	f.FakeAPI.ExecCreateFn = func(_ context.Context, _ string, _ client.ExecCreateOptions) (client.ExecCreateResult, error) {
		return client.ExecCreateResult{
			ID: execID,
		}, nil
	}
}

// SetupExecStart configures the fake to succeed on ExecStart (detach mode).
func (f *FakeClient) SetupExecStart() {
	f.FakeAPI.ExecStartFn = func(_ context.Context, _ string, _ client.ExecStartOptions) (client.ExecStartResult, error) {
		return client.ExecStartResult{}, nil
	}
}

// SetupExecAttach configures the fake to return a hijacked connection for ExecAttach.
// The server side is closed immediately (suitable for non-TTY tests).
func (f *FakeClient) SetupExecAttach() {
	f.FakeAPI.ExecAttachFn = func(_ context.Context, _ string, _ client.ExecAttachOptions) (client.ExecAttachResult, error) {
		// net.Pipe creates a synchronous in-memory connection pair.
		// Close the server side so reads on the client side return EOF.
		clientConn, serverConn := net.Pipe()
		serverConn.Close()
		return client.ExecAttachResult{
			HijackedResponse: client.NewHijackedResponse(clientConn, "application/vnd.docker.raw-stream"),
		}, nil
	}
}

// SetupExecAttachWithOutput configures the fake to return a hijacked connection
// for ExecAttach that writes the given data as stdcopy-framed stdout.
// This allows ExecCapture (which uses stdcopy.StdCopy) to demultiplex
// the output correctly. The server side is closed after writing, so the
// client side reads the data then gets EOF.
func (f *FakeClient) SetupExecAttachWithOutput(data string) {
	f.FakeAPI.ExecAttachFn = func(_ context.Context, _ string, _ client.ExecAttachOptions) (client.ExecAttachResult, error) {
		clientConn, serverConn := net.Pipe()
		go func() {
			defer serverConn.Close()
			w := stdcopy.NewStdWriter(serverConn, stdcopy.Stdout)
			_, _ = w.Write([]byte(data))
		}()
		return client.ExecAttachResult{
			HijackedResponse: client.NewHijackedResponse(clientConn, "application/vnd.docker.multiplexed-stream"),
		}, nil
	}
}

// SetupExecInspect configures the fake to return an ExecInspect result with
// the given exit code. Running is set to false (exec completed).
func (f *FakeClient) SetupExecInspect(exitCode int) {
	f.FakeAPI.ExecInspectFn = func(_ context.Context, _ string, _ client.ExecInspectOptions) (client.ExecInspectResult, error) {
		return client.ExecInspectResult{
			ExitCode: exitCode,
			Running:  false,
		}, nil
	}
}

// SetupImageList configures the fake to return the given image summaries
// from ImageList calls.
func (f *FakeClient) SetupImageList(summaries ...whail.ImageSummary) {
	f.FakeAPI.ImageListFn = func(_ context.Context, _ client.ImageListOptions) (client.ImageListResult, error) {
		return client.ImageListResult{
			Items: summaries,
		}, nil
	}
}

// MinimalCreateOpts returns the minimum ContainerCreateOptions needed for
// whail's ContainerCreate to succeed (requires non-nil Config for label merging).
func MinimalCreateOpts() docker.ContainerCreateOptions {
	return docker.ContainerCreateOptions{
		Config: &container.Config{
			Image: "alpine:latest",
		},
		Name: "test-container",
	}
}

// MinimalStartOpts returns ContainerStartOptions for a given container ID.
func MinimalStartOpts(containerID string) docker.ContainerStartOptions {
	return docker.ContainerStartOptions{
		ContainerID: containerID,
	}
}

// ImageSummaryFixture returns an ImageSummary with the given repo tag.
func ImageSummaryFixture(repoTag string) whail.ImageSummary {
	return whail.ImageSummary{
		ID:       "sha256:a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4e5f67890",
		RepoTags: []string{repoTag},
		Created:  1700000000,
		Size:     256 * 1024 * 1024,
	}
}

// BuildKitCapture records calls to the fake BuildKit builder wired via SetupBuildKit.
type BuildKitCapture = whailtest.BuildKitCapture

// SetupBuildKit wires a fake BuildKit builder onto the FakeClient's Engine.
// Returns a capture struct for asserting the builder was called with the
// expected options. The fake builder succeeds by default (returns nil error).
//
//	fake := dockertest.NewFakeClient()
//	capture := fake.SetupBuildKit()
//	// exercise code that calls BuildImage with BuildKitEnabled=true
//	if capture.CallCount != 1 { ... }
func (f *FakeClient) SetupBuildKit() *BuildKitCapture {
	capture := &BuildKitCapture{}
	f.Client.Engine.BuildKitImageBuilder = whailtest.FakeBuildKitBuilder(capture)
	return capture
}

// SetupBuildKitWithProgress wires a fake BuildKit builder that emits the given
// progress events via the OnProgress callback. Returns a capture struct for
// asserting the builder was called with expected options.
//
//	events := whailtest.SimpleBuildEvents()
//	capture := fake.SetupBuildKitWithProgress(events)
//	// exercise code that calls BuildImage with BuildKitEnabled=true
//	assert.Equal(t, 1, capture.CallCount)
func (f *FakeClient) SetupBuildKitWithProgress(events []whail.BuildProgressEvent) *BuildKitCapture {
	capture := &BuildKitCapture{ProgressEvents: events}
	f.Client.Engine.BuildKitImageBuilder = whailtest.FakeBuildKitBuilder(capture)
	return capture
}

// SetupBuildKitWithRecordedProgress wires a fake BuildKit builder that emits
// recorded events with timing delays. Use with scenarios loaded from JSON
// testdata files for realistic replay.
//
//	scenario, _ := whailtest.LoadRecordedScenario("testdata/multi-stage.json")
//	capture := fake.SetupBuildKitWithRecordedProgress(scenario.Events)
func (f *FakeClient) SetupBuildKitWithRecordedProgress(events []whailtest.RecordedBuildEvent) *BuildKitCapture {
	capture := &BuildKitCapture{RecordedEvents: events}
	f.Client.Engine.BuildKitImageBuilder = whailtest.FakeTimedBuildKitBuilder(capture)
	return capture
}

// SetupPingBuildKit wires PingFn to report BuildKit as the preferred builder.
// Use this when exercising code paths that call BuildKitEnabled() for detection
// (e.g. real buildRun in the fawker demo CLI).
func (f *FakeClient) SetupPingBuildKit() {
	f.FakeAPI.PingFn = func(_ context.Context, _ client.PingOptions) (client.PingResult, error) {
		return client.PingResult{
			BuilderVersion: build.BuilderBuildKit,
			OSType:         "linux",
		}, nil
	}
}

// BuildKitBuildOpts returns a BuildImageOpts configured for the BuildKit path.
func BuildKitBuildOpts(tag, contextDir string) docker.BuildImageOpts {
	return docker.BuildImageOpts{
		Tags:            []string{tag},
		BuildKitEnabled: true,
		ContextDir:      contextDir,
		SuppressOutput:  true,
	}
}

// SetupLegacyBuild wires a fake legacy (non-BuildKit) image build that succeeds.
// Returns an empty build response body. Use this for code paths that call
// client.BuildImage without BuildKitEnabled (e.g., init command).
func (f *FakeClient) SetupLegacyBuild() {
	f.FakeAPI.ImageBuildFn = func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
		return client.ImageBuildResult{
			Body: io.NopCloser(strings.NewReader("")),
		}, nil
	}
}

// SetupLegacyBuildError wires a fake legacy image build that returns the given error.
func (f *FakeClient) SetupLegacyBuildError(err error) {
	f.FakeAPI.ImageBuildFn = func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
		return client.ImageBuildResult{}, err
	}
}

// managedImageInspect returns an ImageInspectResult with clawker managed labels.
func managedImageInspect(cfg config.Config, ref string) client.ImageInspectResult {
	return client.ImageInspectResult{
		InspectResponse: dockerimage.InspectResponse{
			ID: "sha256:fake-image-id-" + ref,
			Config: &dockerspec.DockerOCIImageConfig{
				ImageConfig: ocispec.ImageConfig{
					Labels: map[string]string{
						cfg.LabelManaged(): cfg.ManagedLabelValue(),
					},
				},
			},
		},
	}
}

// notFoundError creates an error that satisfies errdefs.IsNotFound.
type errNotFound struct {
	msg string
}

func (e errNotFound) Error() string { return e.msg }
func (e errNotFound) NotFound()     {}

func notFoundError(ref string) error {
	return errNotFound{msg: "No such image: " + ref}
}
