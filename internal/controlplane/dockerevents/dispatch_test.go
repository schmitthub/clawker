package dockerevents

import (
	"context"
	"testing"
	"time"

	"github.com/moby/moby/api/types/events"
	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/controlplane/informer"
)

const (
	testManagedKey   = "dev.clawker.managed"
	testManagedValue = "true"
)

// stubClient satisfies the EventsClient interface with empty/no-op
// implementations. Tests override individual methods on a per-test
// basis via embedding.
type stubClient struct{}

func (stubClient) Events(context.Context, mobyclient.EventsListOptions) mobyclient.EventsResult {
	msgs := make(chan events.Message)
	errs := make(chan error, 1)
	return mobyclient.EventsResult{Messages: msgs, Err: errs}
}
func (stubClient) ContainerList(context.Context, mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
	return mobyclient.ContainerListResult{}, nil
}
func (stubClient) ContainerInspect(context.Context, string, mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
	return mobyclient.ContainerInspectResult{}, nil
}
func (stubClient) NetworkList(context.Context, mobyclient.NetworkListOptions) (mobyclient.NetworkListResult, error) {
	return mobyclient.NetworkListResult{}, nil
}
func (stubClient) NetworkInspect(context.Context, string, mobyclient.NetworkInspectOptions) (mobyclient.NetworkInspectResult, error) {
	return mobyclient.NetworkInspectResult{}, nil
}
func (stubClient) VolumeList(context.Context, mobyclient.VolumeListOptions) (mobyclient.VolumeListResult, error) {
	return mobyclient.VolumeListResult{}, nil
}
func (stubClient) ImageList(context.Context, mobyclient.ImageListOptions) (mobyclient.ImageListResult, error) {
	return mobyclient.ImageListResult{}, nil
}

// newTestFeeder constructs a feeder backed by a real informer (started)
// and the given EventsClient. The informer is closed via t.Cleanup so
// drains complete before the test exits.
func newTestFeeder(t *testing.T, cli EventsClient) (*Feeder, *informer.Informer) {
	t.Helper()
	inf := informer.New(informer.Options{})
	require.NoError(t, inf.Start(context.Background()))
	t.Cleanup(func() { _ = inf.Close() })

	f, err := New(cli, inf, Options{
		ManagedLabelKey:   testManagedKey,
		ManagedLabelValue: testManagedValue,
	})
	require.NoError(t, err)
	return f, inf
}

// TestDispatch_ContainerCreateThenDie verifies the lifecycle transition
// flow: managed-label container appears via create, transitions to
// running on start, captures exit_code on die, and is soft-removed on
// destroy. Asserts on observable informer state, not on intermediate
// calls.
func TestDispatch_ContainerCreateThenDie(t *testing.T) {
	f, inf := newTestFeeder(t, stubClient{})
	ctx := context.Background()

	id := "abc1234567890123456789012345678901234567890123456789012345678901"
	managed := events.Actor{
		ID: id,
		Attributes: map[string]string{
			testManagedKey: testManagedValue,
			"image":        "alpine:3",
			"name":         "myctr",
			"app":          "my-svc", // user label
		},
	}

	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionCreate, Actor: managed})
	r, ok := inf.Get(informer.Key{Kind: KindContainer, ID: id})
	require.True(t, ok, "container should be present after create")
	require.Equal(t, "created", r.Lifecycle)
	require.Equal(t, "alpine:3", r.Attrs["image"])
	require.Equal(t, "myctr", r.Attrs["name"])
	require.Equal(t, "my-svc", r.Labels["app"])
	require.NotContains(t, r.Labels, "image", "engine-set 'image' must not pollute Labels")
	require.NotContains(t, r.Labels, "name", "engine-set 'name' must not pollute Labels")

	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionStart, Actor: managed})
	r, _ = inf.Get(informer.Key{Kind: KindContainer, ID: id})
	require.Equal(t, "running", r.Lifecycle)

	dieActor := managed
	dieActor.Attributes = map[string]string{
		testManagedKey: testManagedValue,
		"image":        "alpine:3",
		"name":         "myctr",
		"exitCode":     "137",
	}
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionDie, Actor: dieActor})
	r, _ = inf.Get(informer.Key{Kind: KindContainer, ID: id})
	require.Equal(t, "stopped", r.Lifecycle)
	require.Equal(t, "137", r.Attrs["exit_code"])

	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionDestroy, Actor: dieActor})
	r, ok = inf.Get(informer.Key{Kind: KindContainer, ID: id})
	require.True(t, ok, "destroy is soft-delete; resource stays in store")
	require.Equal(t, informer.LifecycleGone, r.Lifecycle)
	require.False(t, f.containers[id], "destroy must drop the container from managed-set")
}

// TestDispatch_UnmanagedContainerDropped — events without the managed
// label and unknown to the in-feeder containerSet must not reach the
// informer.
func TestDispatch_UnmanagedContainerDropped(t *testing.T) {
	f, inf := newTestFeeder(t, stubClient{})
	ctx := context.Background()

	id := "deadbeef000000000000000000000000000000000000000000000000000000"
	f.dispatch(ctx, events.Message{
		Type:   events.ContainerEventType,
		Action: events.ActionCreate,
		Actor: events.Actor{
			ID:         id,
			Attributes: map[string]string{"image": "nginx"},
		},
	})
	_, ok := inf.Get(informer.Key{Kind: KindContainer, ID: id})
	require.False(t, ok, "unmanaged container must not be upserted")
}

// TestDispatch_ExecAttachActionsDropped — high-volume diagnostic
// actions are pruned at shouldHandleAction.
func TestDispatch_ExecAttachActionsDropped(t *testing.T) {
	require.False(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionExecCreate}))
	require.False(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionExecStart}))
	require.False(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionAttach}))
	require.False(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionResize}))
	require.False(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionTop}))
	require.False(t, shouldHandleAction(events.Message{Type: events.VolumeEventType, Action: events.ActionMount}))
	require.True(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionStart}))
	require.True(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionDie}))
	require.True(t, shouldHandleAction(events.Message{Type: events.NetworkEventType, Action: events.ActionConnect}))
}

// TestDispatch_HealthStatus — health_status: <verdict> events flow
// through to Resource.Attrs["health"].
func TestDispatch_HealthStatus(t *testing.T) {
	f, inf := newTestFeeder(t, stubClient{})
	ctx := context.Background()

	id := "fa110000000000000000000000000000000000000000000000000000000000aa"
	managed := events.Actor{ID: id, Attributes: map[string]string{
		testManagedKey: testManagedValue,
		"image":        "alpine",
		"name":         "ctr",
	}}
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionCreate, Actor: managed})

	f.dispatch(ctx, events.Message{
		Type:   events.ContainerEventType,
		Action: events.ActionHealthStatusUnhealthy, // "health_status: unhealthy"
		Actor:  managed,
	})
	r, ok := inf.Get(informer.Key{Kind: KindContainer, ID: id})
	require.True(t, ok)
	require.Equal(t, "unhealthy", r.Attrs["health"])
}

// TestDispatch_NetworkConnectMissingNetwork — a connect event for a
// network we don't track must not produce an orphan relation. This
// exercises the networkSet gate.
func TestDispatch_NetworkConnectMissingNetwork(t *testing.T) {
	f, inf := newTestFeeder(t, stubClient{})
	ctx := context.Background()

	netID := "net0000000000000000000000000000000000000000000000000000000000aa"
	ctrID := "ctr0000000000000000000000000000000000000000000000000000000000bb"

	// Container is managed and present, but networkSet is empty.
	f.containers[ctrID] = true
	f.dispatch(ctx, events.Message{
		Type:   events.NetworkEventType,
		Action: events.ActionConnect,
		Actor: events.Actor{
			ID:         netID,
			Attributes: map[string]string{"container": ctrID, "name": "user-net", "type": "bridge"},
		},
	})

	rels := inf.Neighbors(informer.Key{Kind: KindContainer, ID: ctrID}, RelationAttachedTo)
	require.Empty(t, rels, "connect to unmanaged network must not create relation")
}

// TestDispatch_VolumeCreateThenDestroy
func TestDispatch_VolumeCreateThenDestroy(t *testing.T) {
	f, inf := newTestFeeder(t, stubClient{})
	ctx := context.Background()

	name := "clawker-test-vol"
	managedActor := events.Actor{
		ID: name,
		Attributes: map[string]string{
			testManagedKey: testManagedValue,
			"driver":       "local",
		},
	}
	f.dispatch(ctx, events.Message{Type: events.VolumeEventType, Action: events.ActionCreate, Actor: managedActor, TimeNano: time.Now().UnixNano()})
	r, ok := inf.Get(informer.Key{Kind: KindVolume, ID: name})
	require.True(t, ok)
	require.Equal(t, informer.LifecycleLive, r.Lifecycle)

	f.dispatch(ctx, events.Message{Type: events.VolumeEventType, Action: events.ActionDestroy, Actor: managedActor, TimeNano: time.Now().UnixNano()})
	r, _ = inf.Get(informer.Key{Kind: KindVolume, ID: name})
	require.Equal(t, informer.LifecycleGone, r.Lifecycle)
	require.False(t, f.volumes[name], "destroy must drop the volume from managed-set")
}

// TestStripEngineKeys
func TestStripEngineKeys(t *testing.T) {
	out := stripEngineKeys(map[string]string{
		"image":  "alpine",
		"name":   "ctr",
		"app":    "svc",
		"team":   "platform",
		"weight": "0.5",
	}, "image", "name")
	require.Equal(t, map[string]string{
		"app":    "svc",
		"team":   "platform",
		"weight": "0.5",
	}, out)

	require.Nil(t, stripEngineKeys(nil), "nil in → nil out")
	require.Nil(t, stripEngineKeys(map[string]string{"image": "alpine"}, "image"), "empty after strip → nil")
}

// TestHealthStatusFrom
func TestHealthStatusFrom(t *testing.T) {
	cases := []struct {
		in       events.Action
		expectOK bool
		expect   string
	}{
		{events.ActionHealthStatusHealthy, true, "healthy"},
		{events.ActionHealthStatusUnhealthy, true, "unhealthy"},
		{events.ActionHealthStatusRunning, true, "running"},
		{events.ActionHealthStatus, true, "unknown"},
		{events.ActionStart, false, ""},
		{events.ActionDie, false, ""},
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			got, ok := healthStatusFrom(c.in)
			require.Equal(t, c.expectOK, ok)
			require.Equal(t, c.expect, got)
		})
	}
}

// TestContainerLifecycleFromAction
func TestContainerLifecycleFromAction(t *testing.T) {
	cases := []struct {
		in     events.Action
		expect string
	}{
		{events.ActionCreate, "created"},
		{events.ActionStart, "running"},
		{events.ActionRestart, "running"},
		{events.ActionUnPause, "running"},
		{events.ActionPause, "paused"},
		{events.ActionDie, "stopped"},
		{events.ActionStop, "stopped"},
		{events.ActionKill, "stopped"},
		{events.ActionRename, ""}, // no lifecycle change
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			require.Equal(t, c.expect, containerLifecycleFromAction(c.in))
		})
	}
}
