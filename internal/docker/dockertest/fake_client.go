// Package dockertest provides test doubles for internal/docker.Client.
//
// It composes whailtest.FakeAPIClient into a real *docker.Client, so
// docker-layer methods (ListContainers, FindContainerByAgent, etc.) execute
// real code through the whail jail — giving better coverage than mocking
// the docker.Client interface directly.
//
// Usage:
//
//	fake := dockertest.NewFakeClient(cfg)
//	fake.SetupContainerList(dockertest.RunningContainerFixture("myapp", "dev"))
//	containers, err := fake.Client.ListContainers(ctx, true)
//
//	fake.AssertCalled(t, "ContainerList")
package dockertest

import (
	"context"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	dockerimage "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"
	moby "github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
)

// FakeClient wraps a real *docker.Client backed by a whailtest.FakeAPIClient.
// Configure behavior via FakeAPI's Fn fields; pass Client to code under test.
type FakeClient struct {
	// Client is the real *docker.Client to inject into command Options.
	// Its embedded Engine delegates to FakeAPI through whail's jail layer.
	Client *docker.Client

	// FakeAPI is the underlying function-field fake. Set Fn fields here
	// to control what the Docker SDK "returns" for each operation.
	FakeAPI *whailtest.FakeAPIClient

	// Cfg is the config used to construct this client. Exposed so that
	// Setup helpers and callers can read label keys without hardcoding.
	Cfg config.Config
}

// FakeClientOption configures a FakeClient.
type FakeClientOption func(*FakeClient)

// NewFakeClient constructs a FakeClient with production-equivalent label
// configuration. The returned Client.Engine uses clawker's label prefix,
// so docker-layer methods (ListContainers, FindContainerByAgent,
// etc.) exercise real label filtering logic.
func NewFakeClient(cfg config.Config, opts ...FakeClientOption) *FakeClient {
	fakeAPI := whailtest.NewFakeAPIClient()
	engine := whail.NewFromExisting(fakeAPI, whail.EngineOptions{
		LabelPrefix:  cfg.EngineLabelPrefix(),
		ManagedLabel: cfg.EngineManagedLabel(),
		Labels:       docker.TestLabelConfig(cfg),
	})
	client := docker.NewClientFromEngine(engine, cfg)

	// Override whailtest's default ContainerInspect to return clawker labels
	// instead of whailtest's "com.whailtest.managed" default. This prevents
	// latent bugs when tests skip SetupFindContainer.
	fakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ moby.ContainerInspectOptions) (moby.ContainerInspectResult, error) {
		return moby.ContainerInspectResult{
			Container: container.InspectResponse{
				ID: id,
				Config: &container.Config{
					Labels: map[string]string{
						cfg.LabelManaged(): cfg.ManagedLabelValue(),
					},
				},
			},
		}, nil
	}

	// Override whailtest's default VolumeInspect to return clawker labels.
	fakeAPI.VolumeInspectFn = func(_ context.Context, name string, _ moby.VolumeInspectOptions) (moby.VolumeInspectResult, error) {
		return moby.VolumeInspectResult{
			Volume: volume.Volume{
				Name: name,
				Labels: map[string]string{
					cfg.LabelManaged(): cfg.ManagedLabelValue(),
				},
			},
		}, nil
	}

	// Override whailtest's default NetworkInspect to return clawker labels.
	fakeAPI.NetworkInspectFn = func(_ context.Context, name string, _ moby.NetworkInspectOptions) (moby.NetworkInspectResult, error) {
		return moby.NetworkInspectResult{
			Network: network.Inspect{
				Network: network.Network{
					Name: name,
					ID:   "net-" + name,
					Labels: map[string]string{
						cfg.LabelManaged(): cfg.ManagedLabelValue(),
					},
				},
			},
		}, nil
	}

	// Default ContainerList returns an empty list.
	fakeAPI.ContainerListFn = func(_ context.Context, _ moby.ContainerListOptions) (moby.ContainerListResult, error) {
		return moby.ContainerListResult{}, nil
	}

	// Override whailtest's default ImageInspect to return clawker labels.
	fakeAPI.ImageInspectFn = func(_ context.Context, ref string, _ ...moby.ImageInspectOption) (moby.ImageInspectResult, error) {
		return moby.ImageInspectResult{
			InspectResponse: dockerimage.InspectResponse{
				ID: ref,
				Config: &dockerspec.DockerOCIImageConfig{
					ImageConfig: ocispec.ImageConfig{
						Labels: map[string]string{
							cfg.LabelManaged(): cfg.ManagedLabelValue(),
						},
					},
				},
			},
		}, nil
	}

	fc := &FakeClient{
		Client:  client,
		FakeAPI: fakeAPI,
		Cfg:     cfg,
	}

	for _, opt := range opts {
		opt(fc)
	}

	return fc
}

// AssertCalled asserts that the given method was called at least once.
func (f *FakeClient) AssertCalled(t *testing.T, method string) {
	t.Helper()
	whailtest.AssertCalled(t, f.FakeAPI, method)
}

// AssertNotCalled asserts that the given method was never called.
func (f *FakeClient) AssertNotCalled(t *testing.T, method string) {
	t.Helper()
	whailtest.AssertNotCalled(t, f.FakeAPI, method)
}

// AssertCalledN asserts that the given method was called exactly n times.
func (f *FakeClient) AssertCalledN(t *testing.T, method string, n int) {
	t.Helper()
	whailtest.AssertCalledN(t, f.FakeAPI, method, n)
}

// Reset clears the call recording log.
func (f *FakeClient) Reset() {
	f.FakeAPI.Reset()
}
