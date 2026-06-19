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

	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
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

// newTestFeeder constructs a feeder publishing onto a real
// *pubsub.Topic[DockerEvent] with a recording subscriber. The topic is closed
// via t.Cleanup so drains complete before the test exits.
func newTestFeeder(t *testing.T, cli EventsClient) (*Feeder, *recorder) {
	t.Helper()
	rec := newRecorder(t)
	f, err := New(cli, rec.topic, Options{
		ManagedLabelKey:   testManagedKey,
		ManagedLabelValue: testManagedValue,
	})
	require.NoError(t, err)
	return f, rec
}

// recvEventByAction drains the recorder channel until it observes an envelope
// whose payload Type+Action match, or the deadline elapses. Other actions are
// skipped — the test seeds them deliberately.
func recvEventByAction(t *testing.T, ch <-chan pubsub.Event[DockerEvent], wantType events.Type, wantAction events.Action) pubsub.Event[DockerEvent] {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Payload.Type == wantType && ev.Payload.Action == wantAction {
				return ev
			}
		case <-deadline:
			t.Fatalf("did not receive %s/%s within deadline", wantType, wantAction)
			return pubsub.Event[DockerEvent]{}
		}
	}
}

// assertNoEvent fails if any envelope arrives within the grace window.
func assertNoEvent(t *testing.T, ch <-chan pubsub.Event[DockerEvent], grace time.Duration) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event published: %+v", ev.Payload)
	case <-time.After(grace):
	}
}

// TestDispatch_ContainerLifecycle_OneToOne pins the 1:1 moby-action →
// envelope mapping. Every actionable container event publishes exactly one
// DockerEvent envelope wrapping the moby message verbatim — create, start, die
// (with exitCode) and destroy each ride through unchanged. The feeder projects
// no state; the published envelope is the entire contract.
func TestDispatch_ContainerLifecycle_OneToOne(t *testing.T) {
	f, rec := newTestFeeder(t, stubClient{})
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

	// ActionCreate → exactly one DockerEvent published, payload verbatim.
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionCreate, Actor: managed, TimeNano: time.Now().UnixNano()})
	ev := rec.recvByAction(t, events.ContainerEventType, events.ActionCreate)
	require.Equal(t, id, ev.Payload.Actor.ID)
	require.Equal(t, "myctr", ev.Payload.Actor.Attributes["name"])
	require.Equal(t, sourceName, ev.Source, "envelope must carry the dockerevents source")
	require.NotEmpty(t, ev.ID, "envelope must carry a non-empty ID")
	require.Equal(t, ev.Payload.TimeNano, ev.Timestamp, "envelope Timestamp must be the event's UnixNano")

	// ActionStart → published verbatim, user label intact, engine keys intact
	// on the payload (the feeder strips nothing — that's a subscriber concern).
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionStart, Actor: managed, TimeNano: time.Now().UnixNano()})
	ev = rec.recvByAction(t, events.ContainerEventType, events.ActionStart)
	require.Equal(t, id, ev.Payload.Actor.ID)
	require.Equal(t, "my-svc", ev.Payload.Actor.Attributes["app"])
	require.Equal(t, "alpine:3", ev.Payload.Actor.Attributes["image"], "feeder publishes Actor.Attributes verbatim")

	// ActionDie → published with exitCode riding on Actor.Attributes.
	dieActor := managed
	dieActor.Attributes = map[string]string{
		testManagedKey: testManagedValue,
		"image":        "alpine:3",
		"name":         "myctr",
		"exitCode":     "137",
	}
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionDie, Actor: dieActor, TimeNano: time.Now().UnixNano()})
	ev = rec.recvByAction(t, events.ContainerEventType, events.ActionDie)
	require.Equal(t, id, ev.Payload.Actor.ID)
	require.Equal(t, "137", ev.Payload.Actor.Attributes["exitCode"], "exitCode must ride through on Actor.Attributes")

	// ActionDestroy → published + dropped from managed-set.
	f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionDestroy, Actor: dieActor, TimeNano: time.Now().UnixNano()})
	ev = rec.recvByAction(t, events.ContainerEventType, events.ActionDestroy)
	require.Equal(t, id, ev.Payload.Actor.ID)
	require.False(t, f.containers[id], "destroy must drop the container from managed-set")
}

// TestDispatch_RestartPublishesRestartAction — moby fires a single
// `restart` action atomically (not separate stop+start); the
// dispatcher publishes DockerEvent{Action=restart}, distinct from
// ActionStart.
func TestDispatch_RestartPublishesRestartAction(t *testing.T) {
	f, rec := newTestFeeder(t, stubClient{})

	id := "rstrtaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	a := events.Actor{ID: id, Attributes: map[string]string{
		testManagedKey: testManagedValue, "image": "alpine", "name": "rstr",
	}}
	f.dispatch(context.Background(), events.Message{
		Type: events.ContainerEventType, Action: events.ActionRestart, Actor: a, TimeNano: time.Now().UnixNano(),
	})

	ev := rec.recvByAction(t, events.ContainerEventType, events.ActionRestart)
	require.Equal(t, id, ev.Payload.Actor.ID)
	require.Equal(t, events.ActionRestart, ev.Payload.Action, "must remain Action=restart, not collapse to Action=start")
}

// TestDispatch_UnmanagedContainerDropped — events without the managed
// label and unknown to the in-feeder containerSet must not be published.
func TestDispatch_UnmanagedContainerDropped(t *testing.T) {
	f, rec := newTestFeeder(t, stubClient{})
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

	assertNoEvent(t, rec.ch, 50*time.Millisecond)
}

// TestDispatch_OOMPublishesOOMAction — moby's `oom` action stays
// distinct on the wire; subscribers that need a single "container
// terminated" notification dedup at the consumer layer.
func TestDispatch_OOMPublishesOOMAction(t *testing.T) {
	f, rec := newTestFeeder(t, stubClient{})

	id := "oom00000000000000000000000000000000000000000000000000000000000aa"
	a := events.Actor{ID: id, Attributes: map[string]string{
		testManagedKey: testManagedValue, "image": "alpine", "name": "oomctr",
	}}
	f.dispatch(context.Background(), events.Message{
		Type: events.ContainerEventType, Action: events.ActionOOM, Actor: a, TimeNano: time.Now().UnixNano(),
	})

	ev := rec.recvByAction(t, events.ContainerEventType, events.ActionOOM)
	require.Equal(t, id, ev.Payload.Actor.ID)
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
	f, rec := newTestFeeder(t, stubClient{})

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
	ev := rec.recvByAction(t, events.NetworkEventType, events.ActionConnect)
	require.Equal(t, ctrID, ev.Payload.Actor.Attributes["container"])
	require.Equal(t, netID, ev.Payload.Actor.ID)

	f.dispatch(context.Background(), events.Message{
		Type:     events.NetworkEventType,
		Action:   events.ActionDisconnect,
		Actor:    events.Actor{ID: netID, Attributes: map[string]string{"container": ctrID}},
		TimeNano: time.Now().UnixNano(),
	})
	ev = rec.recvByAction(t, events.NetworkEventType, events.ActionDisconnect)
	require.Equal(t, ctrID, ev.Payload.Actor.Attributes["container"])
	require.Equal(t, netID, ev.Payload.Actor.ID)
}

// TestDispatch_NetworkConnect_UnknownNetwork — connect with the
// network not in managed-set must not publish anything.
func TestDispatch_NetworkConnect_UnknownNetwork(t *testing.T) {
	f, rec := newTestFeeder(t, stubClient{})

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

	assertNoEvent(t, rec.ch, 50*time.Millisecond)
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
		f, rec := newTestFeeder(t, cli)

		f.dispatch(context.Background(), events.Message{
			Type:   events.NetworkEventType,
			Action: events.ActionCreate,
			Actor:  events.Actor{ID: netID},
		})

		require.True(t, f.networks[netID], "managed network must populate networkSet")
		ev := rec.recvByAction(t, events.NetworkEventType, events.ActionCreate)
		require.Equal(t, netID, ev.Payload.Actor.ID)
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
	f, rec := newTestFeeder(t, stubClient{})

	netID := "n0000000000000000000000000000000000000000000000000000000000000aa"
	f.networks[netID] = true

	f.dispatch(context.Background(), events.Message{
		Type:   events.NetworkEventType,
		Action: events.ActionDestroy,
		Actor:  events.Actor{ID: netID},
	})

	ev := rec.recvByAction(t, events.NetworkEventType, events.ActionDestroy)
	require.Equal(t, netID, ev.Payload.Actor.ID)
	require.False(t, f.networks[netID], "destroy must drop the network from managed-set")
}

// TestLogEventReceived_ActorAttributesSchema pins the structured-log
// contract for Actor.Attributes. Operators rely on these field names.
func TestLogEventReceived_ActorAttributesSchema(t *testing.T) {
	var buf bytes.Buffer
	topic, err := pubsub.NewTopic[DockerEvent](logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = topic.Close() })

	f, err := New(stubClient{}, topic, Options{
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
	topic, err := pubsub.NewTopic[DockerEvent](logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = topic.Close() })

	f, err := New(stubClient{}, topic, Options{
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
