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

func newFakeFeeder(t *testing.T, cli EventsClient, reconnectMin time.Duration) (*Feeder, *recorder) {
	t.Helper()
	rec := newRecorder(t)
	f, err := New(cli, rec.topic, Options{
		ManagedLabelKey:   testManagedKey,
		ManagedLabelValue: testManagedValue,
		ReconnectMin:      reconnectMin,
		ReconnectMax:      10 * reconnectMin,
	})
	require.NoError(t, err)
	return f, rec
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

// panicOnEventsClient reconciles cleanly (empty lists) but panics the
// moment Run opens the events stream. It exercises the CP no-panic
// discipline on the feeder's serve-path goroutine: a panic anywhere in
// the Run -> runStream -> dispatch chain must be recovered and converted
// into a returned error, never allowed to kill PID 1.
type panicOnEventsClient struct{ *fakeEventsClient }

func (c *panicOnEventsClient) Events(context.Context, mobyclient.EventsListOptions) mobyclient.EventsResult {
	panic("synthetic dispatch-chain panic")
}

// TestRun_RecoversPanicAsError — a panic in the dispatch chain is
// recovered and surfaced as Run's returned error rather than crashing
// the process. Goes red if the deferred recover in Run is removed (the
// panic would propagate and fail the test goroutine / take down PID 1
// in production).
func TestRun_RecoversPanicAsError(t *testing.T) {
	cli := &panicOnEventsClient{fakeEventsClient: &fakeEventsClient{}}
	f, _ := newFakeFeeder(t, cli, 5*time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- f.Run(context.Background()) }()

	select {
	case err := <-done:
		require.Error(t, err, "panic must be converted to a returned error")
		require.Contains(t, err.Error(), "dockerevents feeder panic")
		require.Contains(t, err.Error(), "synthetic dispatch-chain panic")
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after dispatch-chain panic")
	}
}

// TestSupervise_CancelSwallowed — a clean ctx cancel is the expected
// drain-to-zero / SIGTERM stop, NOT a failure: Supervise returns
// without depositing anything onto failed. Goes red if the
// cancel-vs-error discrimination is dropped (a cancel would be routed
// as a spurious serve failure and exit the daemon non-zero).
func TestSupervise_CancelSwallowed(t *testing.T) {
	cli := &fakeEventsClient{}
	f, _ := newFakeFeeder(t, cli, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	failed := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.Supervise(ctx, failed)
	}()

	require.Eventually(t, func() bool {
		return cli.containerListCalls.Load() > 0
	}, time.Second, time.Millisecond, "expected at least one reconcile pass")
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Supervise did not return after ctx cancel")
	}

	select {
	case err := <-failed:
		t.Fatalf("clean cancel must not be routed as a failure, got: %v", err)
	default:
	}
}

// TestSupervise_RoutesRealFailure — a non-cancel Run return (here a
// recovered dispatch-chain panic) is surfaced onto failed, wrapped with
// the feeder prefix. Goes red if Supervise stops routing failures (the
// daemon would never exit non-zero on an unrecoverable feeder fault and
// the on-failure restart policy would never retrigger).
func TestSupervise_RoutesRealFailure(t *testing.T) {
	cli := &panicOnEventsClient{fakeEventsClient: &fakeEventsClient{}}
	f, _ := newFakeFeeder(t, cli, 5*time.Millisecond)

	failed := make(chan error, 1)
	go f.Supervise(context.Background(), failed)

	select {
	case err := <-failed:
		require.Error(t, err)
		require.Contains(t, err.Error(), "dockerevents feeder")
		require.Contains(t, err.Error(), "synthetic dispatch-chain panic")
	case <-time.After(2 * time.Second):
		t.Fatal("Supervise did not route the feeder failure onto failed")
	}
}

// TestSupervise_NonBlockingSendOnFullChannel — when failed is already
// saturated (the serve select is busy draining a prior error),
// Supervise must NOT block trying to deposit a late feeder failure;
// blocking here would wedge the goroutine and strand the eBPF programs.
// Goes red if the send loses its default case.
func TestSupervise_NonBlockingSendOnFullChannel(t *testing.T) {
	cli := &panicOnEventsClient{fakeEventsClient: &fakeEventsClient{}}
	f, _ := newFakeFeeder(t, cli, 5*time.Millisecond)

	failed := make(chan error, 1)
	failed <- errors.New("prior serve failure")

	done := make(chan struct{})
	go func() {
		defer close(done)
		f.Supervise(context.Background(), failed)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Supervise blocked on a full failed channel")
	}
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

// TestReconcile_PublishesRunningContainer seeds the fake client with one
// running container and asserts reconcile republishes a synthetic
// container/start DockerEvent envelope for it (name slash-trimmed, user
// labels intact on the payload) and populates the feeder's managed-sets.
func TestReconcile_PublishesRunningContainer(t *testing.T) {
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
	f, rec := newFakeFeeder(t, cli, 5*time.Millisecond)

	require.NoError(t, f.reconcile(context.Background()))

	ev := rec.recvByAction(t, events.ContainerEventType, events.ActionStart)
	require.Equal(t, "ctr1", ev.Payload.Actor.ID)
	require.Equal(t, "app", ev.Payload.Actor.Attributes["name"], "leading slash on Names[0] must be trimmed")
	require.True(t, f.containers["ctr1"])
	require.True(t, f.networks["net1"])
}

// TestReconcile_PublishesNetworkConnected — a container with a
// network attachment produces a network/connect envelope after
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
	f, rec := newFakeFeeder(t, cli, 5*time.Millisecond)

	require.NoError(t, f.reconcile(context.Background()))

	ev := rec.recvByAction(t, events.NetworkEventType, events.ActionConnect)
	require.Equal(t, "ctr1", ev.Payload.Actor.Attributes["container"])
	require.Equal(t, "net1", ev.Payload.Actor.ID)
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
