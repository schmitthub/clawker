package whailtest

import (
	"context"
	"slices"
	"testing"
	"time"

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

// --- BuildKit test helpers ---

// BuildKitCapture records calls to a fake BuildKit builder closure.
type BuildKitCapture struct {
	// Opts is the most recently captured ImageBuildKitOptions.
	Opts whail.ImageBuildKitOptions
	// CallCount tracks how many times the builder was invoked.
	CallCount int
	// Err is the error to return from the fake builder. Defaults to nil.
	Err error
	// ProgressEvents are emitted via OnProgress when the fake builder is invoked.
	// If nil or empty, no progress events are sent.
	ProgressEvents []whail.BuildProgressEvent
	// RecordedEvents are emitted with timing delays via OnProgress when the fake
	// builder is invoked. If non-nil, takes precedence over ProgressEvents.
	// Use with FakeTimedBuildKitBuilder for realistic replay.
	RecordedEvents []RecordedBuildEvent
	// DelayMultiplier scales all RecordedEvent delays. 0 or 1 = no change,
	// 2 = twice as slow, 0.5 = twice as fast. Applied in FakeTimedBuildKitBuilder.
	DelayMultiplier float64
}

// FakeBuildKitBuilder returns a BuildKit builder closure that captures
// invocations into the returned BuildKitCapture. Use this to verify
// that ImageBuildKit is called with the expected options.
//
//	capture := &whailtest.BuildKitCapture{}
//	engine.BuildKitImageBuilder = whailtest.FakeBuildKitBuilder(capture)
//	// ... exercise code ...
//	// capture.Opts contains the last call's options
//	// capture.CallCount is the number of invocations
func FakeBuildKitBuilder(capture *BuildKitCapture) func(context.Context, whail.ImageBuildKitOptions) error {
	return func(_ context.Context, opts whail.ImageBuildKitOptions) error {
		capture.Opts = opts
		capture.CallCount++
		if opts.OnProgress != nil {
			for _, event := range capture.ProgressEvents {
				opts.OnProgress(event)
			}
		}
		return capture.Err
	}
}

// FakeTimedBuildKitBuilder returns a BuildKit builder closure that emits events
// with timing delays. If capture.RecordedEvents is set, events are emitted with
// their recorded delays. Otherwise, falls back to instant emission of
// capture.ProgressEvents (same behavior as FakeBuildKitBuilder).
//
// Use this for realistic replay of recorded build scenarios:
//
//	scenario, _ := whailtest.LoadRecordedScenario("testdata/multi-stage.json")
//	capture := &whailtest.BuildKitCapture{RecordedEvents: scenario.Events}
//	engine.BuildKitImageBuilder = whailtest.FakeTimedBuildKitBuilder(capture)
func FakeTimedBuildKitBuilder(capture *BuildKitCapture) func(context.Context, whail.ImageBuildKitOptions) error {
	return func(ctx context.Context, opts whail.ImageBuildKitOptions) error {
		capture.Opts = opts
		capture.CallCount++
		if opts.OnProgress == nil {
			return capture.Err
		}

		mult := capture.DelayMultiplier
		if mult <= 0 {
			mult = 1
		}

		// Prefer RecordedEvents (timed) over ProgressEvents (instant).
		if len(capture.RecordedEvents) > 0 {
			for _, re := range capture.RecordedEvents {
				if re.DelayMs > 0 {
					delay := time.Duration(float64(re.Delay()) * mult)
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(delay):
					}
				}
				opts.OnProgress(re.Event)
			}
		} else {
			for _, event := range capture.ProgressEvents {
				opts.OnProgress(event)
			}
		}

		return capture.Err
	}
}
