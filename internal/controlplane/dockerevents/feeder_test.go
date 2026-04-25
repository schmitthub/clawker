package dockerevents

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mobycontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	mobyimage "github.com/moby/moby/api/types/image"
	mobynetwork "github.com/moby/moby/api/types/network"
	mobyvolume "github.com/moby/moby/api/types/volume"
	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/controlplane/informer"
)

// fakeEventsClient is a programmable EventsClient. Tests assign list
// results / events stream behavior to drive the Feeder loop
// deterministically.
type fakeEventsClient struct {
	mu sync.Mutex

	// list inputs (Reconcile reads these atomically per call).
	containerListResult mobyclient.ContainerListResult
	containerListErr    error
	networkListResult   mobyclient.NetworkListResult
	networkListErr      error
	volumeListResult    mobyclient.VolumeListResult
	volumeListErr       error
	imageListResult     mobyclient.ImageListResult
	imageListErr        error

	// streamFactory builds the Messages+Err channels for each Events
	// call. Per-call so tests can simulate reconnects.
	streamFactory func() mobyclient.EventsResult

	containerListCalls atomic.Int64
}

func (c *fakeEventsClient) Events(ctx context.Context, _ mobyclient.EventsListOptions) mobyclient.EventsResult {
	c.mu.Lock()
	factory := c.streamFactory
	c.mu.Unlock()
	if factory == nil {
		// Default: idle stream that closes when ctx cancels.
		msgs := make(chan events.Message)
		errs := make(chan error, 1)
		go func() {
			<-ctx.Done()
			close(msgs)
		}()
		return mobyclient.EventsResult{Messages: msgs, Err: errs}
	}
	return factory()
}

func (c *fakeEventsClient) ContainerList(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
	c.containerListCalls.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.containerListResult, c.containerListErr
}

func (c *fakeEventsClient) ContainerInspect(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
	return mobyclient.ContainerInspectResult{}, nil
}

func (c *fakeEventsClient) NetworkList(_ context.Context, _ mobyclient.NetworkListOptions) (mobyclient.NetworkListResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.networkListResult, c.networkListErr
}

func (c *fakeEventsClient) NetworkInspect(_ context.Context, _ string, _ mobyclient.NetworkInspectOptions) (mobyclient.NetworkInspectResult, error) {
	return mobyclient.NetworkInspectResult{}, nil
}

func (c *fakeEventsClient) VolumeList(_ context.Context, _ mobyclient.VolumeListOptions) (mobyclient.VolumeListResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.volumeListResult, c.volumeListErr
}

func (c *fakeEventsClient) ImageList(_ context.Context, _ mobyclient.ImageListOptions) (mobyclient.ImageListResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.imageListResult, c.imageListErr
}

func newFakeFeeder(t *testing.T, cli EventsClient, reconnectMin time.Duration) (*Feeder, *informer.Informer) {
	t.Helper()
	inf := informer.New(informer.Options{})
	require.NoError(t, inf.Start(context.Background()))
	t.Cleanup(func() { _ = inf.Close() })
	f, err := New(cli, inf, Options{
		ManagedLabelKey:   testManagedKey,
		ManagedLabelValue: testManagedValue,
		ReconnectMin:      reconnectMin,
		ReconnectMax:      10 * reconnectMin,
	})
	require.NoError(t, err)
	return f, inf
}

// TestRun_CtxCancelExits — Run returns ctx.Err() after cancel even
// when the events stream is healthy. Smoke test of the cancel path
// before exercising backoff scenarios.
func TestRun_CtxCancelExits(t *testing.T) {
	cli := &fakeEventsClient{}
	f, _ := newFakeFeeder(t, cli, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()

	// Wait until reconcile has been called at least once so we know we
	// entered the loop, then cancel.
	require.Eventually(t, func() bool {
		return cli.containerListCalls.Load() > 0
	}, time.Second, time.Millisecond, "expected at least one reconcile pass")
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// TestRun_ReconcileFailureBacksOffAndRetries — when reconcile returns
// an error, Run sleeps and retries. Backoff doubles to ReconnectMax.
// We assert at least 2 reconcile attempts within bounded wall time.
func TestRun_ReconcileFailureBacksOffAndRetries(t *testing.T) {
	wantErr := errors.New("docker daemon down")
	cli := &fakeEventsClient{containerListErr: wantErr}
	f, _ := newFakeFeeder(t, cli, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()

	require.Eventually(t, func() bool {
		return cli.containerListCalls.Load() >= 2
	}, time.Second, time.Millisecond, "expected at least two reconcile attempts under backoff")

	cancel()
	<-done
}

type recordedCall struct {
	name string
	at   time.Time
}

type containerListRecorder struct {
	*fakeEventsClient
	mu    *sync.Mutex
	order *[]recordedCall
}

func (r *containerListRecorder) ContainerList(ctx context.Context, opts mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
	r.mu.Lock()
	*r.order = append(*r.order, recordedCall{name: "list", at: time.Now()})
	r.mu.Unlock()
	return r.fakeEventsClient.ContainerList(ctx, opts)
}

// TestRun_AnchorsSinceBeforeListing — the events Since= timestamp is
// captured BEFORE the list call so any event between t0 and the
// listing landing in the informer replays on the events channel.
// Asserted by checking Events is called only AFTER the first
// container list call.
func TestRun_AnchorsSinceBeforeListing(t *testing.T) {
	var (
		mu    sync.Mutex
		order []recordedCall
	)

	cli := &fakeEventsClient{}
	cli.streamFactory = func() mobyclient.EventsResult {
		mu.Lock()
		order = append(order, recordedCall{name: "events", at: time.Now()})
		mu.Unlock()
		msgs := make(chan events.Message)
		errs := make(chan error, 1)
		go func() {
			time.Sleep(20 * time.Millisecond)
			close(msgs)
		}()
		return mobyclient.EventsResult{Messages: msgs, Err: errs}
	}

	wrapped := &containerListRecorder{fakeEventsClient: cli, mu: &mu, order: &order}

	f, _ := newFakeFeeder(t, wrapped, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) >= 2
	}, time.Second, time.Millisecond, "expected list+events sequence")

	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(order), 2)
	require.Equal(t, "list", order[0].name, "ContainerList must run before Events on first iteration")
	require.Equal(t, "events", order[1].name)
}

// TestRun_StreamErrorReopens — a non-EOF stream error triggers a
// reconcile + reopen. Assert ContainerList is called more than once
// because the stream errored.
func TestRun_StreamErrorReopens(t *testing.T) {
	streamCalls := atomic.Int64{}
	cli := &fakeEventsClient{}
	cli.streamFactory = func() mobyclient.EventsResult {
		n := streamCalls.Add(1)
		msgs := make(chan events.Message)
		errs := make(chan error, 1)
		if n == 1 {
			// First stream errors immediately.
			go func() {
				errs <- errors.New("connection reset")
			}()
		} else {
			// Subsequent streams idle until close.
			go func() {
				time.Sleep(50 * time.Millisecond)
				close(msgs)
			}()
		}
		return mobyclient.EventsResult{Messages: msgs, Err: errs}
	}

	f, _ := newFakeFeeder(t, cli, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()

	require.Eventually(t, func() bool {
		return cli.containerListCalls.Load() >= 2
	}, time.Second, time.Millisecond, "stream error must cause reconcile+reopen")

	cancel()
	<-done
}

// TestDrainErrAfterClose_SurfacesDelayedErr — when Messages channel
// closes without an immediate Err, the function waits briefly for a
// late-arriving error and surfaces it instead of returning EOF.
func TestDrainErrAfterClose_SurfacesDelayedErr(t *testing.T) {
	errCh := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		errCh <- errors.New("real failure arrived late")
	}()

	got := drainErrAfterClose(context.Background(), errCh)
	require.Error(t, got)
	require.Contains(t, got.Error(), "real failure arrived late")
}

// TestDrainErrAfterClose_ReturnsEOFOnTimeout — without a delayed
// error, drainErrAfterClose returns io.EOF after the grace window.
func TestDrainErrAfterClose_ReturnsEOFOnTimeout(t *testing.T) {
	errCh := make(chan error, 1)
	got := drainErrAfterClose(context.Background(), errCh)
	require.ErrorIs(t, got, io.EOF)
}

// --- reconcile coverage ---

// TestReconcile_PopulatesManagedSetsAndPublishes seeds the fake
// client with one container + one network + volume + image and
// asserts the informer winds up holding all four after reconcile.
func TestReconcile_PopulatesManagedSetsAndPublishes(t *testing.T) {
	cli := &fakeEventsClient{
		containerListResult: mobyclient.ContainerListResult{Items: []mobycontainer.Summary{
			{
				ID:     "ctr1",
				Names:  []string{"/app"},
				Image:  "alpine:3",
				State:  mobycontainer.StateRunning,
				Labels: map[string]string{testManagedKey: testManagedValue},
				NetworkSettings: &mobycontainer.NetworkSettingsSummary{
					Networks: map[string]*mobynetwork.EndpointSettings{
						"clawker-net": {NetworkID: "net1"},
					},
				},
			},
		}},
		networkListResult: mobyclient.NetworkListResult{Items: []mobynetwork.Summary{
			{Network: mobynetwork.Network{ID: "net1", Name: "clawker-net", Driver: "bridge", Scope: "local",
				Labels: map[string]string{testManagedKey: testManagedValue}}},
		}},
		volumeListResult: mobyclient.VolumeListResult{Items: []mobyvolume.Volume{
			{Name: "vol1", Driver: "local", Mountpoint: "/var/lib/docker/volumes/vol1/_data",
				Labels: map[string]string{testManagedKey: testManagedValue}},
		}},
		imageListResult: mobyclient.ImageListResult{Items: []mobyimage.Summary{
			{ID: "img1", RepoTags: []string{"clawker:latest"},
				Labels: map[string]string{testManagedKey: testManagedValue}},
		}},
	}
	f, inf := newFakeFeeder(t, cli, 5*time.Millisecond)

	require.NoError(t, f.reconcile(context.Background()))

	// Resources present.
	r, ok := inf.Get(informer.Key{Kind: KindContainer, ID: "ctr1"})
	require.True(t, ok)
	require.Equal(t, "running", r.Lifecycle)
	require.Equal(t, "app", r.Attrs["name"], "leading slash on Names[0] must be trimmed")
	require.Equal(t, "alpine:3", r.Attrs["image"])

	rn, ok := inf.Get(informer.Key{Kind: KindNetwork, ID: "net1"})
	require.True(t, ok)
	require.Equal(t, "clawker-net", rn.Attrs["name"])

	rv, ok := inf.Get(informer.Key{Kind: KindVolume, ID: "vol1"})
	require.True(t, ok)
	require.Equal(t, "local", rv.Attrs["driver"])

	ri, ok := inf.Get(informer.Key{Kind: KindImage, ID: "img1"})
	require.True(t, ok)
	require.Equal(t, "clawker:latest", ri.Attrs["repo_tags"])

	// Managed-sets repopulated.
	require.True(t, f.containers["ctr1"])
	require.True(t, f.networks["net1"])
	require.True(t, f.volumes["vol1"])
	require.True(t, f.images["img1"])
}

// TestReconcile_LinksContainerToNetwork — a container with a
// network attachment produces a relation only AFTER networks are
// populated. Verifies the second-pass link is in place after the
// dead-pass deletion.
func TestReconcile_LinksContainerToNetwork(t *testing.T) {
	cli := &fakeEventsClient{
		containerListResult: mobyclient.ContainerListResult{Items: []mobycontainer.Summary{
			{
				ID:     "ctr1",
				State:  mobycontainer.StateRunning,
				Labels: map[string]string{testManagedKey: testManagedValue},
				NetworkSettings: &mobycontainer.NetworkSettingsSummary{
					Networks: map[string]*mobynetwork.EndpointSettings{
						"clawker-net": {NetworkID: "net1"},
					},
				},
			},
		}},
		networkListResult: mobyclient.NetworkListResult{Items: []mobynetwork.Summary{
			{Network: mobynetwork.Network{ID: "net1", Name: "clawker-net",
				Labels: map[string]string{testManagedKey: testManagedValue}}},
		}},
	}
	f, inf := newFakeFeeder(t, cli, 5*time.Millisecond)

	require.NoError(t, f.reconcile(context.Background()))

	// Both resource Upserts must land before LinkRelation can be
	// observable through Neighbors (which only returns relations whose
	// corresponding resource is present).
	rels := inf.Neighbors(informer.Key{Kind: KindContainer, ID: "ctr1"}, RelationAttachedTo)
	require.Len(t, rels, 1, "container→network edge must exist after reconcile")
	require.Equal(t, "net1", rels[0].ID)
}

// TestReconcile_PartialListErrorReturned — when any of the four list
// calls fail, reconcile aggregates via errors.Join and returns. The
// managed-sets are still rebuilt to whatever state was readable.
func TestReconcile_PartialListErrorReturned(t *testing.T) {
	cli := &fakeEventsClient{
		containerListResult: mobyclient.ContainerListResult{Items: []mobycontainer.Summary{
			{ID: "ctr1", Labels: map[string]string{testManagedKey: testManagedValue}, State: mobycontainer.StateRunning},
		}},
		volumeListErr: errors.New("volumes endpoint borked"),
	}
	f, _ := newFakeFeeder(t, cli, 5*time.Millisecond)

	err := f.reconcile(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "volumes endpoint borked")
}

// TestContainerLifecycleFromState_Restarting — the reconcile path
// uses State, not Action. Restarting/Dead/Removing must map sanely.
func TestContainerLifecycleFromState_RestartingDeadRemoving(t *testing.T) {
	require.Equal(t, "restarting", containerLifecycleFromState(mobycontainer.StateRestarting))
	require.Equal(t, "stopped", containerLifecycleFromState(mobycontainer.StateDead))
	require.Equal(t, "stopped", containerLifecycleFromState(mobycontainer.StateRemoving))
	require.Equal(t, "", containerLifecycleFromState(mobycontainer.ContainerState("nonsense")))
}
