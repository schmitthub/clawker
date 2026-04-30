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

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
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

// newTestFeeder constructs a feeder backed by a real, started Overseer
// and the given EventsClient. Both are closed via t.Cleanup so drains
// complete before the test exits.
func newTestFeeder(t *testing.T, cli EventsClient) (*Feeder, *overseer.Overseer) {
	t.Helper()
	bus := overseer.New(overseer.Options{Logger: logger.Nop()})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })

	f, err := New(cli, bus, Options{
		ManagedLabelKey:   testManagedKey,
		ManagedLabelValue: testManagedValue,
	})
	require.NoError(t, err)
	return f, bus
}

// snapshotEventually polls Snapshot until cond returns true or the
// deadline elapses. Required because Publish is fire-and-forget — the
// run-loop applies events asynchronously.
func snapshotEventually(t *testing.T, bus *overseer.Overseer, cond func(overseer.State) bool) overseer.State {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		state, ok := bus.Snapshot(context.Background())
		if ok && cond(state) {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	state, _ := bus.Snapshot(context.Background())
	t.Fatalf("snapshot condition never satisfied; final state: %+v", state)
	return state
}

// TestDispatch_ContainerLifecycle_OneToOne pins the 1:1 moby-action →
// typed-event mapping. ActionCreate publishes ContainerCreated and
// does NOT produce a "running" worldview — a created-but-not-started
// container has no ContainerStarted firing. ActionStart later fires
// ContainerStarted + flips Status=running. This invariant is the
// whole point of the dockerevents 1:1 refactor.
func TestDispatch_ContainerLifecycle_OneToOne(t *testing.T) {
	f, bus := newTestFeeder(t, stubClient{})
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

	subCreated, ok := overseer.Subscribe[ContainerCreated](bus, "test.created")
	require.True(t, ok)
	defer subCreated.Unsubscribe()
	subStarted, ok := overseer.Subscribe[ContainerStarted](bus, "test.started")
	require.True(t, ok)
	defer subStarted.Unsubscribe()
	subDied, ok := overseer.Subscribe[ContainerDied](bus, "test.died")
	require.True(t, ok)
	defer subDied.Unsubscribe()
	subDestroyed, ok := overseer.Subscribe[ContainerDestroyed](bus, "test.destroyed")
	require.True(t, ok)
	defer subDestroyed.Unsubscribe()

	// ActionCreate → ContainerCreated; State.Containers must NOT carry
	// running status (no ContainerStarted fired yet).
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionCreate, Actor: managed, TimeNano: time.Now().UnixNano()})
	select {
	case ev := <-subCreated.C:
		require.Equal(t, id, ev.Actor.ID)
		require.Equal(t, "myctr", ev.Actor.Attributes["name"])
	case <-time.After(time.Second):
		t.Fatal("did not receive ContainerCreated")
	}
	// No ContainerStarted fan-out for create alone.
	select {
	case ev := <-subStarted.C:
		t.Fatalf("ActionCreate must not publish ContainerStarted, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	// ActionStart → ContainerStarted with Status=running in worldview.
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionStart, Actor: managed, TimeNano: time.Now().UnixNano()})
	select {
	case ev := <-subStarted.C:
		require.Equal(t, id, ev.Actor.ID)
	case <-time.After(time.Second):
		t.Fatal("did not receive ContainerStarted")
	}
	state := snapshotEventually(t, bus, func(s overseer.State) bool {
		return s.Containers[id].Status == overseer.ContainerStatusRunning
	})
	view := state.Containers[id]
	require.Equal(t, "myctr", view.Name)
	require.Equal(t, "my-svc", view.Labels["app"])
	require.NotContains(t, view.Labels, "image", "engine-set 'image' must not pollute Labels")
	require.NotContains(t, view.Labels, "name", "engine-set 'name' must not pollute Labels")

	// ActionDie → ContainerDied; status flips to Stopped.
	dieActor := managed
	dieActor.Attributes = map[string]string{
		testManagedKey: testManagedValue,
		"image":        "alpine:3",
		"name":         "myctr",
		"exitCode":     "137",
	}
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionDie, Actor: dieActor, TimeNano: time.Now().UnixNano()})
	select {
	case ev := <-subDied.C:
		require.Equal(t, id, ev.Actor.ID)
		require.Equal(t, int32(137), ev.ExitCode())
	case <-time.After(time.Second):
		t.Fatal("did not receive ContainerDied")
	}
	state = snapshotEventually(t, bus, func(s overseer.State) bool {
		return s.Containers[id].Status == overseer.ContainerStatusStopped
	})
	require.Equal(t, overseer.ContainerStatusStopped, state.Containers[id].Status)

	// ActionDestroy → ContainerDestroyed; state entry deleted entirely.
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionDestroy, Actor: dieActor, TimeNano: time.Now().UnixNano()})
	select {
	case ev := <-subDestroyed.C:
		require.Equal(t, id, ev.Actor.ID)
	case <-time.After(time.Second):
		t.Fatal("did not receive ContainerDestroyed")
	}
	state = snapshotEventually(t, bus, func(s overseer.State) bool {
		_, ok := s.Containers[id]
		return !ok
	})
	require.False(t, f.containers[id], "destroy must drop the container from managed-set")
}

// TestDispatch_RestartPublishesContainerRestarted — moby fires a
// single `restart` action atomically (not separate stop+start); the
// dispatcher publishes ContainerRestarted, distinct from
// ContainerStarted.
func TestDispatch_RestartPublishesContainerRestarted(t *testing.T) {
	f, bus := newTestFeeder(t, stubClient{})

	subRestarted, ok := overseer.Subscribe[ContainerRestarted](bus, "test.restarted")
	require.True(t, ok)
	defer subRestarted.Unsubscribe()
	subStarted, ok := overseer.Subscribe[ContainerStarted](bus, "test.started")
	require.True(t, ok)
	defer subStarted.Unsubscribe()

	id := "rstrtaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	a := events.Actor{ID: id, Attributes: map[string]string{
		testManagedKey: testManagedValue, "image": "alpine", "name": "rstr",
	}}
	f.dispatch(context.Background(), events.Message{
		Type: events.ContainerEventType, Action: events.ActionRestart, Actor: a, TimeNano: time.Now().UnixNano(),
	})

	select {
	case ev := <-subRestarted.C:
		require.Equal(t, id, ev.Actor.ID)
	case <-time.After(time.Second):
		t.Fatal("did not receive ContainerRestarted")
	}
	select {
	case ev := <-subStarted.C:
		t.Fatalf("ActionRestart must not publish ContainerStarted, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestDispatch_UnmanagedContainerDropped — events without the managed
// label and unknown to the in-feeder containerSet must not reach the
// bus.
func TestDispatch_UnmanagedContainerDropped(t *testing.T) {
	f, bus := newTestFeeder(t, stubClient{})
	ctx := context.Background()

	id := "deadbeef000000000000000000000000000000000000000000000000000000"
	f.dispatch(ctx, events.Message{
		Type:   events.ContainerEventType,
		Action: events.ActionCreate,
		Actor: events.Actor{
			ID:         id,
			Attributes: map[string]string{"image": "nginx"},
		},
		TimeNano: time.Now().UnixNano(),
	})

	// Allow the loop a moment in case it would have applied (it shouldn't).
	time.Sleep(20 * time.Millisecond)
	state, ok := bus.Snapshot(context.Background())
	require.True(t, ok)
	_, present := state.Containers[id]
	require.False(t, present, "unmanaged container must not appear in Snapshot")
}

// TestDispatch_OOMPublishesContainerOOM — moby's `oom` action gets
// its own typed event, distinct from `die`. Consumers that need a
// single "container terminated" notification dedup at the consumer
// layer; the dispatcher does not collapse oom+die.
func TestDispatch_OOMPublishesContainerOOM(t *testing.T) {
	f, bus := newTestFeeder(t, stubClient{})

	sub, ok := overseer.Subscribe[ContainerOOM](bus, "test.oom")
	require.True(t, ok)
	defer sub.Unsubscribe()

	id := "oom00000000000000000000000000000000000000000000000000000000000aa"
	a := events.Actor{ID: id, Attributes: map[string]string{
		testManagedKey: testManagedValue, "image": "alpine", "name": "oomctr",
	}}
	f.dispatch(context.Background(), events.Message{
		Type: events.ContainerEventType, Action: events.ActionOOM, Actor: a, TimeNano: time.Now().UnixNano(),
	})

	select {
	case ev := <-sub.C:
		require.Equal(t, id, ev.Actor.ID)
	case <-time.After(time.Second):
		t.Fatal("did not receive ContainerOOM within 1s")
	}
}

// TestShouldHandleAction_ExecAttachActionsDropped pins the action
// allowlist. Exec/diagnostic actions must never reach the dispatcher.
func TestShouldHandleAction_ExecAttachActionsDropped(t *testing.T) {
	require.False(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionExecCreate}))
	require.False(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionExecStart}))
	require.False(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionExecDie}))
	require.False(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: "exec_detach"})) // prefix-only
	require.False(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionAttach}))
	require.False(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionResize}))
	require.False(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionTop}))
	require.True(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionStart}))
	require.True(t, shouldHandleAction(events.Message{Type: events.ContainerEventType, Action: events.ActionDie}))
	require.True(t, shouldHandleAction(events.Message{Type: events.NetworkEventType, Action: events.ActionConnect}))
}

// TestDispatch_NetworkConnectDisconnect_BothManaged — connect/
// disconnect with both endpoints managed publishes typed
// NetworkConnected / NetworkDisconnected events. Container ID lives
// in Actor.Attributes["container"]; network ID is Actor.ID.
func TestDispatch_NetworkConnectDisconnect_BothManaged(t *testing.T) {
	f, bus := newTestFeeder(t, stubClient{})

	subConnected, ok := overseer.Subscribe[NetworkConnected](bus, "test.connected")
	require.True(t, ok)
	defer subConnected.Unsubscribe()

	subDisconnected, ok := overseer.Subscribe[NetworkDisconnected](bus, "test.disconnected")
	require.True(t, ok)
	defer subDisconnected.Unsubscribe()

	netID := "n0000000000000000000000000000000000000000000000000000000000000aa"
	ctrID := "c0000000000000000000000000000000000000000000000000000000000000aa"
	f.networks[netID] = true
	f.containers[ctrID] = true

	f.dispatch(context.Background(), events.Message{
		Type:     events.NetworkEventType,
		Action:   events.ActionConnect,
		Actor:    events.Actor{ID: netID, Attributes: map[string]string{"container": ctrID}},
		TimeNano: time.Now().UnixNano(),
	})

	select {
	case ev := <-subConnected.C:
		require.Equal(t, ctrID, ev.Actor.Attributes["container"])
		require.Equal(t, netID, ev.Actor.ID)
	case <-time.After(time.Second):
		t.Fatal("did not receive NetworkConnected")
	}

	f.dispatch(context.Background(), events.Message{
		Type:     events.NetworkEventType,
		Action:   events.ActionDisconnect,
		Actor:    events.Actor{ID: netID, Attributes: map[string]string{"container": ctrID}},
		TimeNano: time.Now().UnixNano(),
	})

	select {
	case ev := <-subDisconnected.C:
		require.Equal(t, ctrID, ev.Actor.Attributes["container"])
		require.Equal(t, netID, ev.Actor.ID)
	case <-time.After(time.Second):
		t.Fatal("did not receive NetworkDisconnected")
	}
}

// TestDispatch_NetworkConnect_UnknownNetwork — connect with the
// network not in managed-set must not publish anything.
func TestDispatch_NetworkConnect_UnknownNetwork(t *testing.T) {
	f, bus := newTestFeeder(t, stubClient{})
	sub, ok := overseer.Subscribe[NetworkConnected](bus, "test")
	require.True(t, ok)
	defer sub.Unsubscribe()

	ctrID := "c0000000000000000000000000000000000000000000000000000000000000bb"
	f.containers[ctrID] = true

	f.dispatch(context.Background(), events.Message{
		Type:   events.NetworkEventType,
		Action: events.ActionConnect,
		Actor: events.Actor{
			ID:         "n0000000000000000000000000000000000000000000000000000000000000aa",
			Attributes: map[string]string{"container": ctrID, "name": "user-net", "type": "bridge"},
		},
		TimeNano: time.Now().UnixNano(),
	})

	select {
	case ev := <-sub.C:
		t.Fatalf("unexpected event for unmanaged network: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// ok — no event
	}
}

// netInspectClient lets a test return a canned NetworkInspectResult
// for dispatchNetwork's create-path inspect call.
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

// TestDispatch_NetworkCreate_PublishesForManaged — inspect-driven
// create path: managed networks land in the in-feeder networkSet and
// publish a NetworkCreated event; unmanaged networks are silently
// dropped (no inspect-failure recheck flag for the managed case).
func TestDispatch_NetworkCreate_PublishesForManaged(t *testing.T) {
	netID := "n0000000000000000000000000000000000000000000000000000000000000aa"

	t.Run("managed", func(t *testing.T) {
		cli := &netInspectClient{got: mobynetwork.Inspect{Network: mobynetwork.Network{
			Name:   "clawker-net",
			Driver: "bridge",
			Scope:  "local",
			Labels: map[string]string{testManagedKey: testManagedValue, "clawker": "true"},
		}}}
		f, bus := newTestFeeder(t, cli)
		sub, ok := overseer.Subscribe[NetworkCreated](bus, "test.netcreated")
		require.True(t, ok)
		defer sub.Unsubscribe()

		f.dispatch(context.Background(), events.Message{
			Type:   events.NetworkEventType,
			Action: events.ActionCreate,
			Actor:  events.Actor{ID: netID},
		})

		require.True(t, f.networks[netID], "managed network must populate networkSet")
		select {
		case ev := <-sub.C:
			require.Equal(t, netID, ev.Actor.ID)
		case <-time.After(time.Second):
			t.Fatal("did not receive NetworkCreated for managed network")
		}
	})

	t.Run("unmanaged", func(t *testing.T) {
		cli := &netInspectClient{got: mobynetwork.Inspect{Network: mobynetwork.Network{
			Name: "host-net",
		}}}
		f, _ := newTestFeeder(t, cli)

		f.dispatch(context.Background(), events.Message{
			Type:   events.NetworkEventType,
			Action: events.ActionCreate,
			Actor:  events.Actor{ID: netID},
		})

		require.False(t, f.networks[netID], "unmanaged network must not populate networkSet")
	})

	t.Run("inspect_error", func(t *testing.T) {
		cli := &netInspectClient{err: context.DeadlineExceeded}
		f, _ := newTestFeeder(t, cli)

		f.dispatch(context.Background(), events.Message{
			Type:   events.NetworkEventType,
			Action: events.ActionCreate,
			Actor:  events.Actor{ID: netID},
		})

		require.False(t, f.networks[netID], "inspect failure must not produce a managed-set entry")
		require.True(t, f.networksNeedRecheck[netID], "failed inspect must register a recheck")
	})
}

// TestDispatch_NetworkDestroy_PublishesAndDropsManagedSet — destroy
// publishes NetworkDestroyed for previously-managed networks AND
// drops them from the managed-set.
func TestDispatch_NetworkDestroy_PublishesAndDropsManagedSet(t *testing.T) {
	f, bus := newTestFeeder(t, stubClient{})
	sub, ok := overseer.Subscribe[NetworkDestroyed](bus, "test.netdestroyed")
	require.True(t, ok)
	defer sub.Unsubscribe()

	netID := "n0000000000000000000000000000000000000000000000000000000000000aa"
	f.networks[netID] = true

	f.dispatch(context.Background(), events.Message{
		Type:   events.NetworkEventType,
		Action: events.ActionDestroy,
		Actor:  events.Actor{ID: netID},
	})

	require.False(t, f.networks[netID], "destroy must drop the network from managed-set")
	select {
	case ev := <-sub.C:
		require.Equal(t, netID, ev.Actor.ID)
	case <-time.After(time.Second):
		t.Fatal("did not receive NetworkDestroyed")
	}
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

// TestLogEventReceived_ActorAttributesSchema pins the structured-log
// contract for Actor.Attributes. Operators rely on these field names.
func TestLogEventReceived_ActorAttributesSchema(t *testing.T) {
	var buf bytes.Buffer
	bus := overseer.New(overseer.Options{Logger: logger.Nop()})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })

	f, err := New(stubClient{}, bus, Options{
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
	assert.Equal(t, "demo", line["actor_attr.name"])
	_, hasOldPrefix := line["attr.image"]
	assert.False(t, hasOldPrefix, "stale attr.<k> prefix must not appear")

	agg, ok := line["actor_attributes"].(map[string]any)
	require.True(t, ok, "actor_attributes must be a JSON object: got %T", line["actor_attributes"])
	assert.Equal(t, "alpine:3", agg["image"])
	assert.Equal(t, "demo", agg["name"])
}

// TestLogEventReceived_NoAttributes_OmitsAggregate — no attributes
// means no actor_attributes JSON aggregate (avoids noisy `={}` lines).
func TestLogEventReceived_NoAttributes_OmitsAggregate(t *testing.T) {
	var buf bytes.Buffer
	bus := overseer.New(overseer.Options{Logger: logger.Nop()})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })

	f, err := New(stubClient{}, bus, Options{
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
