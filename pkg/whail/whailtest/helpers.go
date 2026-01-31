package whailtest

import (
	"context"
	"slices"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	dockerimage "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/schmitthub/clawker/pkg/whail"
)

const (
	// TestLabelPrefix is the label prefix used by test engines.
	TestLabelPrefix = "com.whailtest"

	// TestManagedLabel is the managed label suffix used by test engines.
	TestManagedLabel = "managed"
)

// testManagedLabelKey is the full managed label key for test engines.
var testManagedLabelKey = TestLabelPrefix + "." + TestManagedLabel

// TestEngineOptions returns EngineOptions configured for unit testing.
func TestEngineOptions() whail.EngineOptions {
	return whail.EngineOptions{
		LabelPrefix:  TestLabelPrefix,
		ManagedLabel: TestManagedLabel,
	}
}

// NewFakeAPIClient creates a FakeAPIClient with sensible defaults.
// The default inspect methods return managed resources so that whail's
// internal IsManaged checks pass transparently.
func NewFakeAPIClient() *FakeAPIClient {
	f := &FakeAPIClient{}

	// Default: ContainerInspect returns a managed container
	f.ContainerInspectFn = func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return ManagedContainerInspect(id), nil
	}

	// Default: VolumeInspect returns a managed volume
	f.VolumeInspectFn = func(_ context.Context, id string, _ client.VolumeInspectOptions) (client.VolumeInspectResult, error) {
		return ManagedVolumeInspect(id), nil
	}

	// Default: NetworkInspect returns a managed network
	f.NetworkInspectFn = func(_ context.Context, name string, _ client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
		return ManagedNetworkInspect(name), nil
	}

	// Default: ImageInspect returns a managed image
	f.ImageInspectFn = func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		return ManagedImageInspect(ref), nil
	}

	return f
}

// --- Managed resource factories ---

// ManagedContainerInspect returns a ContainerInspectResult with managed labels set.
func ManagedContainerInspect(id string) client.ContainerInspectResult {
	return client.ContainerInspectResult{
		Container: container.InspectResponse{
			ID: id,
			Config: &container.Config{
				Labels: map[string]string{
					testManagedLabelKey: "true",
				},
			},
		},
	}
}

// UnmanagedContainerInspect returns a ContainerInspectResult without managed labels.
func UnmanagedContainerInspect(id string) client.ContainerInspectResult {
	return client.ContainerInspectResult{
		Container: container.InspectResponse{
			ID: id,
			Config: &container.Config{
				Labels: map[string]string{},
			},
		},
	}
}

// ManagedVolumeInspect returns a VolumeInspectResult with managed labels set.
func ManagedVolumeInspect(name string) client.VolumeInspectResult {
	return client.VolumeInspectResult{
		Volume: volume.Volume{
			Name: name,
			Labels: map[string]string{
				testManagedLabelKey: "true",
			},
		},
	}
}

// UnmanagedVolumeInspect returns a VolumeInspectResult without managed labels.
func UnmanagedVolumeInspect(name string) client.VolumeInspectResult {
	return client.VolumeInspectResult{
		Volume: volume.Volume{
			Name:   name,
			Labels: map[string]string{},
		},
	}
}

// ManagedNetworkInspect returns a NetworkInspectResult with managed labels set.
func ManagedNetworkInspect(name string) client.NetworkInspectResult {
	return client.NetworkInspectResult{
		Network: network.Inspect{
			Network: network.Network{
				Name: name,
				ID:   "net-" + name,
				Labels: map[string]string{
					testManagedLabelKey: "true",
				},
			},
		},
	}
}

// UnmanagedNetworkInspect returns a NetworkInspectResult without managed labels.
func UnmanagedNetworkInspect(name string) client.NetworkInspectResult {
	return client.NetworkInspectResult{
		Network: network.Inspect{
			Network: network.Network{
				Name:   name,
				ID:     "net-" + name,
				Labels: map[string]string{},
			},
		},
	}
}

// ManagedImageInspect returns an ImageInspectResult with managed labels set.
func ManagedImageInspect(ref string) client.ImageInspectResult {
	return client.ImageInspectResult{
		InspectResponse: dockerimage.InspectResponse{
			ID: ref,
			Config: &dockerspec.DockerOCIImageConfig{
				DockerOCIImageConfigExt: dockerspec.DockerOCIImageConfigExt{},
				ImageConfig: ocispec.ImageConfig{
					Labels: map[string]string{
						testManagedLabelKey: "true",
					},
				},
			},
		},
	}
}

// UnmanagedImageInspect returns an ImageInspectResult without managed labels.
func UnmanagedImageInspect(ref string) client.ImageInspectResult {
	return client.ImageInspectResult{
		InspectResponse: dockerimage.InspectResponse{
			ID: ref,
			Config: &dockerspec.DockerOCIImageConfig{
				DockerOCIImageConfigExt: dockerspec.DockerOCIImageConfigExt{},
				ImageConfig: ocispec.ImageConfig{
					Labels: map[string]string{},
				},
			},
		},
	}
}

// --- Wait helpers ---

// FakeContainerWaitOK returns a ContainerWaitResult with exit code 0.
func FakeContainerWaitOK() client.ContainerWaitResult {
	return FakeContainerWaitExit(0)
}

// FakeContainerWaitExit returns a ContainerWaitResult with the given exit code.
func FakeContainerWaitExit(code int64) client.ContainerWaitResult {
	resultCh := make(chan container.WaitResponse, 1)
	errCh := make(chan error, 1)
	resultCh <- container.WaitResponse{StatusCode: code}
	return client.ContainerWaitResult{
		Result: resultCh,
		Error:  errCh,
	}
}

// --- Assertion helpers ---

// AssertCalled fails the test if the given method was not called on the fake.
func AssertCalled(t *testing.T, fake *FakeAPIClient, method string) {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !slices.Contains(fake.Calls, method) {
		t.Errorf("expected %s to be called, but it was not; calls: %v", method, fake.Calls)
	}
}

// AssertNotCalled fails the test if the given method was called on the fake.
func AssertNotCalled(t *testing.T, fake *FakeAPIClient, method string) {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if slices.Contains(fake.Calls, method) {
		t.Errorf("expected %s to NOT be called, but it was; calls: %v", method, fake.Calls)
	}
}

// AssertCalledN fails the test if the given method was not called exactly n times.
func AssertCalledN(t *testing.T, fake *FakeAPIClient, method string, n int) {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	count := 0
	for _, c := range fake.Calls {
		if c == method {
			count++
		}
	}
	if count != n {
		t.Errorf("expected %s to be called %d times, but was called %d times; calls: %v", method, n, count, fake.Calls)
	}
}
