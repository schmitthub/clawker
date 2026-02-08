package dockertest

import (
	"context"
	"io"
	"strings"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/api/types/container"
	dockerimage "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
)

// ContainerFixture builds a container.Summary with proper clawker labels.
// The container is in "exited" state by default.
func ContainerFixture(project, agent, image string) container.Summary {
	name := docker.ContainerName(project, agent)
	labels := map[string]string{
		docker.LabelManaged: docker.ManagedLabelValue,
		docker.LabelAgent:   agent,
		docker.LabelImage:   image,
	}
	if project != "" {
		labels[docker.LabelProject] = project
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
			return managedImageInspect(ref), nil
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
		return managedImageInspect(ref), nil
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
						docker.LabelManaged: docker.ManagedLabelValue,
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
							docker.LabelManaged: docker.ManagedLabelValue,
						},
					},
				},
			}, nil
		}
		return client.NetworkInspectResult{}, notFoundError(networkName)
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
func managedImageInspect(ref string) client.ImageInspectResult {
	return client.ImageInspectResult{
		InspectResponse: dockerimage.InspectResponse{
			ID: "sha256:fake-image-id-" + ref,
			Config: &dockerspec.DockerOCIImageConfig{
				ImageConfig: ocispec.ImageConfig{
					Labels: map[string]string{
						docker.LabelManaged: docker.ManagedLabelValue,
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
