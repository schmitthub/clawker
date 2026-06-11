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
	mobynetwork "github.com/moby/moby/api/types/network"
	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// fakeEventsClient is a programmable EventsClient. Tests assign list
// results / events stream behavior to drive the Feeder loop
// deterministically.
type fakeEventsClient struct {
	mu sync.Mutex

	containerListResult mobyclient.ContainerListResult
	containerListErr    error
	networkListResult   mobyclient.NetworkListResult
	networkListErr      error

	streamFactory func() mobyclient.EventsResult

	containerListCalls atomic.Int64
}

func (c *fakeEventsClient) Events(ctx context.Context, _ mobyclient.EventsListOptions) mobyclient.EventsResult {
	c.mu.Lock()
	factory := c.streamFactory
	c.mu.Unlock()
	if factory == nil {
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

func newFakeFeeder(t *testing.T, cli EventsClient, reconnectMin time.Duration) (*Feeder, *overseer.Overseer) {
	t.Helper()
	bus := overseer.New(overseer.Options{Logger: logger.Nop()})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })
	f, err := New(cli, bus, Options{
		ManagedLabelKey:   testManagedKey,
		ManagedLabelValue: testManagedValue,
		ReconnectMin:      reconnectMin,
		ReconnectMax:      10 * reconnectMin,
	})
	require.NoError(t, err)
	return f, bus
}

// TestRun_CtxCancelExits — Run returns ctx.Err() after cancel even
// when the events stream is healthy.
func TestRun_CtxCancelExits(t *testing.T) {
	cli := &fakeEventsClient{}
	f, _ := newFakeFeeder(t, cli, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()

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
// captured BEFORE the list call.
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
// reconcile + reopen.
func TestRun_StreamErrorReopens(t *testing.T) {
	streamCalls := atomic.Int64{}
	cli := &fakeEventsClient{}
	cli.streamFactory = func() mobyclient.EventsResult {
		n := streamCalls.Add(1)
		msgs := make(chan events.Message)
		errs := make(chan error, 1)
		if n == 1 {
			go func() {
				errs <- errors.New("connection reset")
			}()
		} else {
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

// TestReconcile_PopulatesContainersInWorldview seeds the fake client
// with one running container and asserts the Overseer State reflects
// it as ContainerStatusRunning.
func TestReconcile_PopulatesContainersInWorldview(t *testing.T) {
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
						consts.Network: {NetworkID: "net1"},
					},
				},
			},
		}},
		networkListResult: mobyclient.NetworkListResult{Items: []mobynetwork.Summary{
			{Network: mobynetwork.Network{ID: "net1", Name: consts.Network, Driver: "bridge", Scope: "local",
				Labels: map[string]string{testManagedKey: testManagedValue}}},
		}},
	}
	f, bus := newFakeFeeder(t, cli, 5*time.Millisecond)

	require.NoError(t, f.reconcile(context.Background()))

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		state, _ := bus.Snapshot(context.Background())
		if v, ok := state.Containers["ctr1"]; ok && v.Status == overseer.ContainerStatusRunning {
			require.Equal(t, "app", v.Name, "leading slash on Names[0] must be trimmed")
			require.True(t, f.containers["ctr1"])
			require.True(t, f.networks["net1"])
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("reconcile did not populate worldview within 500ms")
}

// TestReconcile_PublishesNetworkConnected — a container with a
// network attachment produces a NetworkConnected event after
// reconcile. The synthetic envelope carries the container_id in
// Actor.Attributes["container"] and the network_id in Actor.ID,
// matching the wire-delivered shape so subscribers can't tell
// reconcile-observed events from stream-delivered ones apart.
func TestReconcile_PublishesNetworkConnected(t *testing.T) {
	cli := &fakeEventsClient{
		containerListResult: mobyclient.ContainerListResult{Items: []mobycontainer.Summary{
			{
				ID:     "ctr1",
				State:  mobycontainer.StateRunning,
				Labels: map[string]string{testManagedKey: testManagedValue},
				NetworkSettings: &mobycontainer.NetworkSettingsSummary{
					Networks: map[string]*mobynetwork.EndpointSettings{
						consts.Network: {NetworkID: "net1"},
					},
				},
			},
		}},
		networkListResult: mobyclient.NetworkListResult{Items: []mobynetwork.Summary{
			{Network: mobynetwork.Network{ID: "net1", Name: consts.Network,
				Labels: map[string]string{testManagedKey: testManagedValue}}},
		}},
	}
	f, bus := newFakeFeeder(t, cli, 5*time.Millisecond)

	sub, ok := overseer.Subscribe[DockerEvent](bus, "test")
	require.True(t, ok)
	defer sub.Unsubscribe()

	require.NoError(t, f.reconcile(context.Background()))

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-sub.C:
			if ev.Type == events.NetworkEventType && ev.Action == events.ActionConnect {
				require.Equal(t, "ctr1", ev.Actor.Attributes["container"])
				require.Equal(t, "net1", ev.Actor.ID)
				return
			}
		case <-deadline:
			t.Fatal("did not receive network/connect after reconcile")
		}
	}
}

// TestReconcile_PartialListErrorReturned — when any list call fails,
// reconcile aggregates via errors.Join and returns.
func TestReconcile_PartialListErrorReturned(t *testing.T) {
	cli := &fakeEventsClient{
		networkListErr: errors.New("network endpoint borked"),
	}
	f, _ := newFakeFeeder(t, cli, 5*time.Millisecond)

	err := f.reconcile(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "network endpoint borked")
}

// TestContainerActionFromState — reconcile observation maps moby
// state to the action that the live event stream would have used.
// StateCreated and StateRestarting return "" — created-but-not-
// started has no running transition to fabricate, restarting is
// transient and the next real event will redrive.
func TestContainerActionFromState(t *testing.T) {
	require.Equal(t, events.ActionStart, containerActionFromState(mobycontainer.StateRunning))
	require.Equal(t, events.ActionPause, containerActionFromState(mobycontainer.StatePaused))
	require.Equal(t, events.ActionDie, containerActionFromState(mobycontainer.StateExited))
	require.Equal(t, events.ActionDie, containerActionFromState(mobycontainer.StateDead))
	require.Equal(t, events.ActionDestroy, containerActionFromState(mobycontainer.StateRemoving))
	require.Equal(t, events.Action(""), containerActionFromState(mobycontainer.StateCreated))
	require.Equal(t, events.Action(""), containerActionFromState(mobycontainer.StateRestarting))
	require.Equal(t, events.Action(""), containerActionFromState(mobycontainer.ContainerState("nonsense")))
}
