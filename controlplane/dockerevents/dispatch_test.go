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

	"github.com/schmitthub/clawker/internal/consts"
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

// recvEventByAction drains the bus's DockerEvent subscription until it
// observes one whose Action equals want, or the deadline elapses.
// Other actions are returned to the caller's tests by other matchers
// — but for this single-channel shape we read past unrelated entries
// since the test seeds them deliberately.
func recvEventByAction(t *testing.T, ch <-chan DockerEvent, wantType events.Type, wantAction events.Action) DockerEvent {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == wantType && ev.Action == wantAction {
				return ev
			}
		case <-deadline:
			t.Fatalf("did not receive %s/%s within deadline", wantType, wantAction)
			return DockerEvent{}
		}
	}
}

// TestDispatch_ContainerLifecycle_OneToOne pins the 1:1 moby-action →
// envelope mapping. ActionCreate publishes a DockerEvent BUT does not
// produce a "running" worldview — a created-but-not-started container
// has no Status flip. ActionStart later flips Status=running. This
// invariant is the whole point of the dockerevents 1:1 refactor.
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

	sub, ok := overseer.Subscribe[DockerEvent](bus, "test")
	require.True(t, ok)
	defer sub.Unsubscribe()

	// ActionCreate → DockerEvent published; State.Containers must NOT
	// carry running status (no start fired yet).
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionCreate, Actor: managed, TimeNano: time.Now().UnixNano()})
	ev := recvEventByAction(t, sub.C, events.ContainerEventType, events.ActionCreate)
	require.Equal(t, id, ev.Actor.ID)
	require.Equal(t, "myctr", ev.Actor.Attributes["name"])

	// No Status=running yet from create alone.
	state, _ := bus.Snapshot(ctx)
	require.NotEqual(t, overseer.ContainerStatusRunning, state.Containers[id].Status, "ActionCreate must not flip Status=running")

	// ActionStart → publish + Status=running.
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionStart, Actor: managed, TimeNano: time.Now().UnixNano()})
	ev = recvEventByAction(t, sub.C, events.ContainerEventType, events.ActionStart)
	require.Equal(t, id, ev.Actor.ID)
	state = snapshotEventually(t, bus, func(s overseer.State) bool {
		return s.Containers[id].Status == overseer.ContainerStatusRunning
	})
	view := state.Containers[id]
	require.Equal(t, "myctr", view.Name)
	require.Equal(t, "my-svc", view.Labels["app"])
	require.NotContains(t, view.Labels, "image", "engine-set 'image' must not pollute Labels")
	require.NotContains(t, view.Labels, "name", "engine-set 'name' must not pollute Labels")

	// ActionDie → publish + Status=stopped.
	dieActor := managed
	dieActor.Attributes = map[string]string{
		testManagedKey: testManagedValue,
		"image":        "alpine:3",
		"name":         "myctr",
		"exitCode":     "137",
	}
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionDie, Actor: dieActor, TimeNano: time.Now().UnixNano()})
	ev = recvEventByAction(t, sub.C, events.ContainerEventType, events.ActionDie)
	require.Equal(t, id, ev.Actor.ID)
	require.Equal(t, "137", ev.Actor.Attributes["exitCode"], "exitCode must ride through on Actor.Attributes")
	state = snapshotEventually(t, bus, func(s overseer.State) bool {
		return s.Containers[id].Status == overseer.ContainerStatusStopped
	})
	require.Equal(t, overseer.ContainerStatusStopped, state.Containers[id].Status)

	// ActionDestroy → publish + state entry deleted.
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionDestroy, Actor: dieActor, TimeNano: time.Now().UnixNano()})
	ev = recvEventByAction(t, sub.C, events.ContainerEventType, events.ActionDestroy)
	require.Equal(t, id, ev.Actor.ID)
	snapshotEventually(t, bus, func(s overseer.State) bool {
		_, ok := s.Containers[id]
		return !ok
	})
	require.False(t, f.containers[id], "destroy must drop the container from managed-set")
}

// TestDispatch_RestartPublishesRestartAction — moby fires a single
// `restart` action atomically (not separate stop+start); the
// dispatcher publishes DockerEvent{Action=restart}, distinct from
// ActionStart.
func TestDispatch_RestartPublishesRestartAction(t *testing.T) {
	f, bus := newTestFeeder(t, stubClient{})

	sub, ok := overseer.Subscribe[DockerEvent](bus, "test")
	require.True(t, ok)
	defer sub.Unsubscribe()

	id := "rstrtaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	a := events.Actor{ID: id, Attributes: map[string]string{
		testManagedKey: testManagedValue, "image": "alpine", "name": "rstr",
	}}
	f.dispatch(context.Background(), events.Message{
		Type: events.ContainerEventType, Action: events.ActionRestart, Actor: a, TimeNano: time.Now().UnixNano(),
	})

	ev := recvEventByAction(t, sub.C, events.ContainerEventType, events.ActionRestart)
	require.Equal(t, id, ev.Actor.ID)
	require.Equal(t, events.ActionRestart, ev.Action, "must remain Action=restart, not collapse to Action=start")
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

// TestDispatch_OOMPublishesOOMAction — moby's `oom` action stays
// distinct on the wire; subscribers that need a single "container
// terminated" notification dedup at the consumer layer.
func TestDispatch_OOMPublishesOOMAction(t *testing.T) {
	f, bus := newTestFeeder(t, stubClient{})

	sub, ok := overseer.Subscribe[DockerEvent](bus, "test")
	require.True(t, ok)
	defer sub.Unsubscribe()

	id := "oom00000000000000000000000000000000000000000000000000000000000aa"
	a := events.Actor{ID: id, Attributes: map[string]string{
		testManagedKey: testManagedValue, "image": "alpine", "name": "oomctr",
	}}
	f.dispatch(context.Background(), events.Message{
		Type: events.ContainerEventType, Action: events.ActionOOM, Actor: a, TimeNano: time.Now().UnixNano(),
	})

	ev := recvEventByAction(t, sub.C, events.ContainerEventType, events.ActionOOM)
	require.Equal(t, id, ev.Actor.ID)
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
// disconnect with both endpoints managed publishes DockerEvent
// envelopes typed as network/connect and network/disconnect.
// Container ID lives in Actor.Attributes["container"]; network ID
// is Actor.ID.
func TestDispatch_NetworkConnectDisconnect_BothManaged(t *testing.T) {
	f, bus := newTestFeeder(t, stubClient{})

	sub, ok := overseer.Subscribe[DockerEvent](bus, "test")
	require.True(t, ok)
	defer sub.Unsubscribe()

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
	ev := recvEventByAction(t, sub.C, events.NetworkEventType, events.ActionConnect)
	require.Equal(t, ctrID, ev.Actor.Attributes["container"])
	require.Equal(t, netID, ev.Actor.ID)

	f.dispatch(context.Background(), events.Message{
		Type:     events.NetworkEventType,
		Action:   events.ActionDisconnect,
		Actor:    events.Actor{ID: netID, Attributes: map[string]string{"container": ctrID}},
		TimeNano: time.Now().UnixNano(),
	})
	ev = recvEventByAction(t, sub.C, events.NetworkEventType, events.ActionDisconnect)
	require.Equal(t, ctrID, ev.Actor.Attributes["container"])
	require.Equal(t, netID, ev.Actor.ID)
}

// TestDispatch_NetworkConnect_UnknownNetwork — connect with the
// network not in managed-set must not publish anything.
func TestDispatch_NetworkConnect_UnknownNetwork(t *testing.T) {
	f, bus := newTestFeeder(t, stubClient{})
	sub, ok := overseer.Subscribe[DockerEvent](bus, "test")
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
// publish a DockerEvent{Type=network, Action=create}; unmanaged
// networks are silently dropped.
func TestDispatch_NetworkCreate_PublishesForManaged(t *testing.T) {
	netID := "n0000000000000000000000000000000000000000000000000000000000000aa"

	t.Run("managed", func(t *testing.T) {
		cli := &netInspectClient{got: mobynetwork.Inspect{Network: mobynetwork.Network{
			Name:   consts.Network,
			Driver: "bridge",
			Scope:  "local",
			Labels: map[string]string{testManagedKey: testManagedValue, "clawker": "true"},
		}}}
		f, bus := newTestFeeder(t, cli)
		sub, ok := overseer.Subscribe[DockerEvent](bus, "test")
		require.True(t, ok)
		defer sub.Unsubscribe()

		f.dispatch(context.Background(), events.Message{
			Type:   events.NetworkEventType,
			Action: events.ActionCreate,
			Actor:  events.Actor{ID: netID},
		})

		require.True(t, f.networks[netID], "managed network must populate networkSet")
		ev := recvEventByAction(t, sub.C, events.NetworkEventType, events.ActionCreate)
		require.Equal(t, netID, ev.Actor.ID)
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
// publishes DockerEvent{Type=network, Action=destroy} for previously-
// managed networks AND drops them from the managed-set.
func TestDispatch_NetworkDestroy_PublishesAndDropsManagedSet(t *testing.T) {
	f, bus := newTestFeeder(t, stubClient{})
	sub, ok := overseer.Subscribe[DockerEvent](bus, "test")
	require.True(t, ok)
	defer sub.Unsubscribe()

	netID := "n0000000000000000000000000000000000000000000000000000000000000aa"
	f.networks[netID] = true

	f.dispatch(context.Background(), events.Message{
		Type:   events.NetworkEventType,
		Action: events.ActionDestroy,
		Actor:  events.Actor{ID: netID},
	})

	ev := recvEventByAction(t, sub.C, events.NetworkEventType, events.ActionDestroy)
	require.Equal(t, netID, ev.Actor.ID)
	require.False(t, f.networks[netID], "destroy must drop the network from managed-set")
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
