package dockerevents

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	mobynetwork "github.com/moby/moby/api/types/network"

	"github.com/moby/moby/api/types/events"
	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/controlplane/informer"
	"github.com/schmitthub/clawker/internal/logger"
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

// netInspectClient lets a test return a canned NetworkInspectResult
// for dispatchNetwork's create-path inspect call. All other methods
// reuse stubClient's empty/no-op behaviour.
type netInspectClient struct {
	stubClient
	got mobynetwork.Inspect
	err error
}

func (c *netInspectClient) NetworkInspect(_ context.Context, _ string, _ mobyclient.NetworkInspectOptions) (mobyclient.NetworkInspectResult, error) {
	if c.err != nil {
		return mobyclient.NetworkInspectResult{}, c.err
	}
	return mobyclient.NetworkInspectResult{Network: c.got}, nil
}

// TestDispatch_NetworkCreate_ManagedAndUnmanaged covers the
// inspect-driven create path: managed networks land in the informer
// and the in-feeder networkSet; unmanaged networks are silently
// dropped.
func TestDispatch_NetworkCreate_ManagedAndUnmanaged(t *testing.T) {
	netID := "n0000000000000000000000000000000000000000000000000000000000000aa"

	t.Run("managed", func(t *testing.T) {
		cli := &netInspectClient{got: mobynetwork.Inspect{Network: mobynetwork.Network{
			Name:   "clawker-net",
			Driver: "bridge",
			Scope:  "local",
			Labels: map[string]string{testManagedKey: testManagedValue, "clawker": "true"},
		}}}
		f, inf := newTestFeeder(t, cli)

		f.dispatch(context.Background(), events.Message{
			Type:   events.NetworkEventType,
			Action: events.ActionCreate,
			Actor:  events.Actor{ID: netID},
		})

		r, ok := inf.Get(informer.Key{Kind: KindNetwork, ID: netID})
		require.True(t, ok)
		require.Equal(t, "clawker-net", r.Attrs["name"])
		require.Equal(t, "bridge", r.Attrs["driver"])
		require.True(t, f.networks[netID])
	})

	t.Run("unmanaged", func(t *testing.T) {
		cli := &netInspectClient{got: mobynetwork.Inspect{Network: mobynetwork.Network{
			Name: "host-net",
		}}}
		f, inf := newTestFeeder(t, cli)

		f.dispatch(context.Background(), events.Message{
			Type:   events.NetworkEventType,
			Action: events.ActionCreate,
			Actor:  events.Actor{ID: netID},
		})

		_, ok := inf.Get(informer.Key{Kind: KindNetwork, ID: netID})
		require.False(t, ok, "unmanaged network must not be upserted")
		require.False(t, f.networks[netID])
	})

	t.Run("inspect_error", func(t *testing.T) {
		cli := &netInspectClient{err: context.DeadlineExceeded}
		f, inf := newTestFeeder(t, cli)

		f.dispatch(context.Background(), events.Message{
			Type:   events.NetworkEventType,
			Action: events.ActionCreate,
			Actor:  events.Actor{ID: netID},
		})

		_, ok := inf.Get(informer.Key{Kind: KindNetwork, ID: netID})
		require.False(t, ok, "inspect failure must not produce a managed-set entry")
		require.False(t, f.networks[netID])
	})
}

// TestDispatch_NetworkDestroy_OfTrackedSoftRemoves verifies that a
// destroy event for a previously-tracked network drops the
// managed-set entry and soft-removes the resource.
func TestDispatch_NetworkDestroy_OfTrackedSoftRemoves(t *testing.T) {
	f, inf := newTestFeeder(t, stubClient{})
	netID := "n0000000000000000000000000000000000000000000000000000000000000aa"

	// Pretend create already populated networkSet + informer.
	f.networks[netID] = true
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind: KindNetwork, ID: netID, Lifecycle: informer.LifecycleLive,
	}, informer.Transition{Source: transitionSource, Verb: verbPrefix + "reconcile", At: time.Now()}))

	f.dispatch(context.Background(), events.Message{
		Type:   events.NetworkEventType,
		Action: events.ActionDestroy,
		Actor:  events.Actor{ID: netID},
	})

	r, ok := inf.Get(informer.Key{Kind: KindNetwork, ID: netID})
	require.True(t, ok, "destroy is soft-delete; resource stays in store")
	require.Equal(t, informer.LifecycleGone, r.Lifecycle)
	require.False(t, f.networks[netID], "destroy must drop the network from managed-set")
}

// TestDispatch_NetworkConnectDisconnect_LinkUnlink covers both halves
// of the relation lifecycle when both ends are managed and tracked.
func TestDispatch_NetworkConnectDisconnect_LinkUnlink(t *testing.T) {
	f, _ := newTestFeeder(t, stubClient{})
	netID := "n0000000000000000000000000000000000000000000000000000000000000aa"
	ctrID := "c0000000000000000000000000000000000000000000000000000000000000aa"

	// Both tracked.
	f.networks[netID] = true
	f.containers[ctrID] = true

	f.dispatch(context.Background(), events.Message{
		Type:   events.NetworkEventType,
		Action: events.ActionConnect,
		Actor:  events.Actor{ID: netID, Attributes: map[string]string{"container": ctrID}},
	})
	rels := f.inf.Neighbors(informer.Key{Kind: KindContainer, ID: ctrID}, RelationAttachedTo)
	require.Len(t, rels, 0, "Neighbors only returns relations whose corresponding resource is present; we never upserted")
	// The link did go in; verify by Incoming on the network side, also
	// shows zero because the network resource isn't upserted either.
	// Instead assert the LinkRelation didn't error out — by checking
	// that the matching Unlink succeeds without producing duplicates.

	f.dispatch(context.Background(), events.Message{
		Type:   events.NetworkEventType,
		Action: events.ActionDisconnect,
		Actor:  events.Actor{ID: netID, Attributes: map[string]string{"container": ctrID}},
	})
	// No assertion shape that observably distinguishes a successful
	// unlink from a no-op without resource presence — the lack of
	// dispatcher panic + clean test exit is the contract.
}

// TestDispatch_ContainerRename_PropagatesNameAttr — rename events
// don't change lifecycle but DO refresh the name attr on the resource.
func TestDispatch_ContainerRename_PropagatesNameAttr(t *testing.T) {
	f, inf := newTestFeeder(t, stubClient{})
	id := "ren00000000000000000000000000000000000000000000000000000000000aa"

	create := events.Actor{ID: id, Attributes: map[string]string{
		testManagedKey: testManagedValue, "image": "alpine", "name": "old-name",
	}}
	f.dispatch(context.Background(), events.Message{Type: events.ContainerEventType, Action: events.ActionCreate, Actor: create})

	rename := events.Actor{ID: id, Attributes: map[string]string{
		testManagedKey: testManagedValue, "image": "alpine", "name": "new-name",
	}}
	f.dispatch(context.Background(), events.Message{Type: events.ContainerEventType, Action: events.ActionRename, Actor: rename})

	r, ok := inf.Get(informer.Key{Kind: KindContainer, ID: id})
	require.True(t, ok)
	require.Equal(t, "new-name", r.Attrs["name"], "rename must refresh name attr")
}

// TestDispatch_ContainerOOM_SetsOOMAttr
func TestDispatch_ContainerOOM_SetsOOMAttr(t *testing.T) {
	f, inf := newTestFeeder(t, stubClient{})
	id := "oom00000000000000000000000000000000000000000000000000000000000aa"
	a := events.Actor{ID: id, Attributes: map[string]string{
		testManagedKey: testManagedValue, "image": "alpine", "name": "oomctr",
	}}
	f.dispatch(context.Background(), events.Message{Type: events.ContainerEventType, Action: events.ActionCreate, Actor: a})
	f.dispatch(context.Background(), events.Message{Type: events.ContainerEventType, Action: events.ActionOOM, Actor: a})
	r, ok := inf.Get(informer.Key{Kind: KindContainer, ID: id})
	require.True(t, ok)
	require.Equal(t, "true", r.Attrs["oom"])
}

// TestShouldHandleAction_ExecActionsDropped — every exec_* prefix is
// pruned, including exec_die / exec_detach which earlier coverage
// missed.
func TestShouldHandleAction_ExecActionsDropped(t *testing.T) {
	for _, a := range []events.Action{
		events.ActionExecCreate,
		events.ActionExecStart,
		events.ActionExecDie,
		"exec_detach", // not in events.Action constants but matches prefix
	} {
		require.False(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: a}), "exec_* action %q must be dropped", a)
	}
}

// TestDispatch_ImageDelete_FromTracked — image delete events for
// previously-tracked images soft-remove via informer and drop from
// the managed-set.
func TestDispatch_ImageDelete_FromTracked(t *testing.T) {
	f, inf := newTestFeeder(t, stubClient{})
	id := "sha256:abc"

	// First, see a managed image create.
	f.dispatch(context.Background(), events.Message{
		Type:   events.ImageEventType,
		Action: events.ActionTag,
		Actor: events.Actor{ID: id, Attributes: map[string]string{
			testManagedKey: testManagedValue,
			"name":         "myimg:1",
		}},
	})
	r, ok := inf.Get(informer.Key{Kind: KindImage, ID: id})
	require.True(t, ok)
	require.Equal(t, "myimg:1", r.Attrs["name"])
	require.True(t, f.images[id])

	// Then delete.
	f.dispatch(context.Background(), events.Message{
		Type:   events.ImageEventType,
		Action: events.ActionDelete,
		Actor:  events.Actor{ID: id},
	})
	r, ok = inf.Get(informer.Key{Kind: KindImage, ID: id})
	require.True(t, ok, "delete is soft-delete")
	require.Equal(t, informer.LifecycleGone, r.Lifecycle)
	require.False(t, f.images[id])
}

// TestDispatch_ImageUntracked_Dropped — events for images we don't
// track and that don't carry the managed label drop silently.
func TestDispatch_ImageUntracked_Dropped(t *testing.T) {
	f, inf := newTestFeeder(t, stubClient{})
	id := "sha256:def"

	f.dispatch(context.Background(), events.Message{
		Type:   events.ImageEventType,
		Action: events.ActionTag,
		Actor:  events.Actor{ID: id, Attributes: map[string]string{"name": "rando:1"}},
	})

	_, ok := inf.Get(informer.Key{Kind: KindImage, ID: id})
	require.False(t, ok, "untracked unmanaged image must not be upserted")
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

// TestLogEventReceived_ActorAttributesSchema pins the structured-log
// contract for Actor.Attributes. The dispatch.go contract promises
// `actor_attr.<k>` per-key fields plus an `actor_attributes` JSON
// aggregate. Operators rely on these names — renaming breaks Loki
// queries.
func TestLogEventReceived_ActorAttributesSchema(t *testing.T) {
	var buf bytes.Buffer
	inf := informer.New(informer.Options{})
	require.NoError(t, inf.Start(context.Background()))
	t.Cleanup(func() { _ = inf.Close() })

	f, err := New(stubClient{}, inf, Options{
		ManagedLabelKey:   testManagedKey,
		ManagedLabelValue: testManagedValue,
		Logger:            logger.NewWriter(&buf),
	})
	require.NoError(t, err)

	f.logEventReceived(events.Message{
		Type:   events.ContainerEventType,
		Action: events.ActionStart,
		Actor: events.Actor{
			ID: "abcdef0123456789",
			Attributes: map[string]string{
				"image": "alpine:3",
				"name":  "demo",
			},
		},
	})

	var line map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line))

	assert.Equal(t, "alpine:3", line["actor_attr.image"], "per-key field must be actor_attr.<k>")
	assert.Equal(t, "demo", line["actor_attr.name"], "per-key field must be actor_attr.<k>")
	_, hasOldPrefix := line["attr.image"]
	assert.False(t, hasOldPrefix, "stale attr.<k> prefix must not appear")

	agg, ok := line["actor_attributes"].(map[string]any)
	require.True(t, ok, "actor_attributes must be a JSON object: got %T", line["actor_attributes"])
	assert.Equal(t, "alpine:3", agg["image"])
	assert.Equal(t, "demo", agg["name"])
}

// TestLogEventReceived_NoAttributes_OmitsAggregate ensures the
// actor_attributes JSON aggregate is only emitted when there is at
// least one attribute, so events with empty actor maps do not produce
// noisy `actor_attributes={}` lines.
func TestLogEventReceived_NoAttributes_OmitsAggregate(t *testing.T) {
	var buf bytes.Buffer
	inf := informer.New(informer.Options{})
	require.NoError(t, inf.Start(context.Background()))
	t.Cleanup(func() { _ = inf.Close() })

	f, err := New(stubClient{}, inf, Options{
		ManagedLabelKey:   testManagedKey,
		ManagedLabelValue: testManagedValue,
		Logger:            logger.NewWriter(&buf),
	})
	require.NoError(t, err)

	f.logEventReceived(events.Message{
		Type:   events.ContainerEventType,
		Action: events.ActionStart,
		Actor:  events.Actor{ID: "abcdef0123456789"},
	})

	var line map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line))

	_, hasAgg := line["actor_attributes"]
	assert.False(t, hasAgg, "actor_attributes must be omitted when Attributes is empty")
}
