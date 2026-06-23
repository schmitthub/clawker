package agent_test

import (
	"crypto/sha256"
	"testing"
	"time"

	mobyevents "github.com/moby/moby/api/types/events"
	"github.com/schmitthub/clawker/controlplane/agent"
	agentmocks "github.com/schmitthub/clawker/controlplane/agent/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/pubsub"
)

// publishAndAwait publishes an agent.AgentEvent through the production
// producer-side seam (Publish) onto the real topic and blocks until the
// store has projected an entry for the agent's container whose
// UpdatedAt matches the event's timestamp. Matching on UpdatedAt (set
// from the envelope Timestamp, which Publish stamps from
// agent.Message.TimeNano) makes the wait observe THIS
// event's projection rather than racing a prior one — the topic drains
// on its own goroutine, so a bare store.Get could read a stale entry.
func publishAndAwait(t *testing.T, topic *pubsub.Topic[agent.AgentEvent], store *agent.AgentStore, ev agent.AgentEvent) {
	t.Helper()
	require.True(t, agent.Publish(topic, ev), "publish must be accepted by the topic")
	want := time.Unix(0, ev.Message.TimeNano)
	require.Eventually(t, func() bool {
		v, ok := store.Get(ev.Agent.ContainerID)
		return ok && v.UpdatedAt.Equal(want)
	}, 2*time.Second, 5*time.Millisecond, "store did not project event for %s", ev.Agent.ContainerID)
}

// thumb returns a deterministic non-zero sha256 thumbprint for tests.
func thumb(seed byte) [sha256.Size]byte {
	var t [sha256.Size]byte
	for i := range t {
		t[i] = seed
	}
	return t
}

// TestAgentStore_Projection drives the agent.AgentStore subscribe-and-project
// repository through the REAL *pubsub.Topic[agent.AgentEvent] across the
// session and registry/trust axes — the worldview-projection contract.
// The exec axis is covered by TestExecutor_Run_StateProjection. Every
// case publishes via the production publish seam and asserts the
// resulting store.Get(cid) worldview. Steps run in order against one
// store so the connected-clears-LastError and register-after-untrust
// transitions are exercised against real prior state.
func TestAgentStore_Projection(t *testing.T) {
	const cid = "c-proj-axis-1234567890ab"
	tp := thumb(0x5a)

	type step struct {
		msg    agent.Message
		assert func(t *testing.T, v agent.AgentEventState)
	}

	now := time.Now().UnixNano()
	mono := func(off int) int64 { return now + int64(off) }

	// Each step's agent.Message.TimeNano is monotonically distinct so
	// publishAndAwait observes that exact step's projection.
	steps := []step{
		{
			// Session connecting: status transitions, address/attempts captured.
			msg: agent.Message{
				Type: agent.DialerEventType, Action: agent.ActionConnecting, TimeNano: mono(1),
				Address: "10.0.0.7:7700", Attempts: 1,
			},
			assert: func(t *testing.T, v agent.AgentEventState) {
				assert.Equal(t, agent.StatusConnecting, v.SessionStatus)
				assert.Equal(t, "10.0.0.7:7700", v.Address)
				assert.Equal(t, 1, v.Attempts)
			},
		},
		{
			// Session failed: status + LastError populated from Detail.
			msg: agent.Message{
				Type: agent.DialerEventType, Action: agent.ActionFailed, TimeNano: mono(2),
				Reason: agent.ReasonFailed, Detail: "dial timeout",
			},
			assert: func(t *testing.T, v agent.AgentEventState) {
				assert.Equal(t, agent.StatusFailed, v.SessionStatus)
				assert.Equal(t, "dial timeout", v.LastError)
			},
		},
		{
			// Session connected: status + peer identity + thumbprint
			// captured AND the stale LastError from the failed attempt
			// cleared. A regression that forgets either ships silently.
			msg: agent.Message{
				Type: agent.DialerEventType, Action: agent.ActionConnected, TimeNano: mono(3),
				PeerAgentFullName: "clawker.myapp.dev", PeerThumbprint: tp,
			},
			assert: func(t *testing.T, v agent.AgentEventState) {
				assert.Equal(t, agent.StatusConnected, v.SessionStatus)
				assert.Equal(t, "clawker.myapp.dev", v.PeerAgentFullName)
				assert.Equal(t, tp, v.Thumbprint)
				assert.Empty(t, v.LastError, "Connected must clear stale session LastError")
			},
		},
		{
			// Session broken: status + LastError from Detail.
			msg: agent.Message{
				Type: agent.DialerEventType, Action: agent.ActionBroken, TimeNano: mono(4),
				Reason: agent.ReasonTransportError, Detail: "stream reset",
			},
			assert: func(t *testing.T, v agent.AgentEventState) {
				assert.Equal(t, agent.StatusBroken, v.SessionStatus)
				assert.Equal(t, "stream reset", v.LastError)
			},
		},
		{
			// Untrusted (thumbprint mismatch): Trust flips, Reason captured.
			msg: agent.Message{
				Type: agent.RegistryEventType, Action: agent.ActionUntrusted, TimeNano: mono(5),
				Reason: agent.ReasonThumbprintMismatch, Detail: "thumbprint mismatch",
			},
			assert: func(t *testing.T, v agent.AgentEventState) {
				assert.False(t, v.Trust.IsTrusted(), "Untrusted must flip Trust")
				assert.Equal(t, agent.ReasonThumbprintMismatch, v.Trust.Reason())
				assert.Equal(t, "thumbprint mismatch", v.LastError)
			},
		},
		{
			// Registered{Ok}: Registered set AND Trust reset to zero so a
			// previously-untrusted agent is trusted again after a clean
			// re-register (the re-register-after-untrust ordering case).
			msg: agent.Message{
				Type: agent.RegistryEventType, Action: agent.ActionRegistered, TimeNano: mono(6),
				RegisterOk: true,
			},
			assert: func(t *testing.T, v agent.AgentEventState) {
				assert.True(t, v.Registered, "Registered{Ok} must set Registered")
				assert.True(t, v.Trust.IsTrusted(),
					"Registered{Ok} after Untrusted must restore trust")
				assert.Equal(t, agent.ReasonNone, v.Trust.Reason())
			},
		},
		{
			// Reap: degraded status.
			msg: agent.Message{
				Type: agent.RegistryEventType, Action: agent.ActionReap, TimeNano: mono(7),
				Detail: "drain timeout exceeded",
			},
			assert: func(t *testing.T, v agent.AgentEventState) {
				assert.Equal(t, agent.StatusDegraded, v.SessionStatus)
				assert.Equal(t, "drain timeout exceeded", v.LastError)
			},
		},
	}

	topic := agentmocks.NewAgentTopic(t)
	store := agent.NewAgentStore()
	store.Subscribe(topic)

	for _, s := range steps {
		ev := agent.AgentEvent{
			Agent:   agent.Agent{ContainerID: cid, AgentName: "dev", Project: "myapp"},
			Message: s.msg,
		}
		publishAndAwait(t, topic, store, ev)
		v, ok := store.Get(cid)
		require.True(t, ok)
		// Identity always refreshed from the event.
		assert.Equal(t, "dev", v.AgentName)
		assert.Equal(t, "myapp", v.Project)
		s.assert(t, v)
	}
}

// Testagent.AgentStore_RegisterFailedDoesNotMarkRegistered proves the
// RegisterOk gate: a Registered event with Ok=false must NOT set
// Registered (the register handshake failed). A regression that flips
// Registered unconditionally would report a failed agent as registered.
func TestAgentStore_RegisterFailedDoesNotMarkRegistered(t *testing.T) {
	const cid = "c-regfail-1234567890ab"
	topic := agentmocks.NewAgentTopic(t)
	store := agent.NewAgentStore()
	store.Subscribe(topic)

	publishAndAwait(t, topic, store, agent.AgentEvent{
		Agent: agent.Agent{ContainerID: cid, AgentName: "dev", Project: "myapp"},
		Message: agent.Message{
			Type: agent.RegistryEventType, Action: agent.ActionRegistered, TimeNano: time.Now().UnixNano(),
			RegisterOk: false,
		},
	})

	v, ok := store.Get(cid)
	require.True(t, ok)
	assert.False(t, v.Registered, "Registered{Ok:false} must not mark the agent registered")
}

// Testagent.AgentStore_EvictsOnContainerDestroy proves the worldview store
// drops a container's projected agent.AgentEventState when that container is
// destroyed (docker rm). The store subscribes to BOTH the agent topic
// (to populate its worldview) and the dockerevents topic (to evict on
// destroy) — the DDD eviction wiring that keeps the observed-now
// projection bounded instead of growing without limit. Drives REAL
// pubsub topics end to end (no mocks): publish an agent.AgentEvent to
// populate, then a container/destroy DockerEvent to evict, and assert
// the entry is gone and Len decremented.
//
// This test goes red if SubscribeDockerEvents is removed (no eviction):
// the destroy event is never consumed, Get stays true, and Len never
// returns to zero.
func TestAgentStore_EvictsOnContainerDestroy(t *testing.T) {
	const cid = "c-evict-1234567890abcdef"

	agentTopic := agentmocks.NewAgentTopic(t)
	dockerTopic := agentmocks.NewDockerTopic(t)
	store := agent.NewAgentStore()
	store.Subscribe(agentTopic)
	store.SubscribeDockerEvents(dockerTopic)

	// Populate the worldview with one agent.
	publishAndAwait(t, agentTopic, store, agent.AgentEvent{
		Agent: agent.Agent{ContainerID: cid, AgentName: "dev", Project: "myapp"},
		Message: agent.Message{
			Type: agent.DialerEventType, Action: agent.ActionConnected, TimeNano: time.Now().UnixNano(),
		},
	})
	require.Equal(t, 1, store.Len(), "store must hold the projected agent")
	_, ok := store.Get(cid)
	require.True(t, ok, "agent must be present before destroy")

	// A container/destroy event must evict the worldview entry.
	publishDocker(dockerTopic, mobyDestroy(cid))

	require.Eventually(t, func() bool {
		_, ok := store.Get(cid)
		return !ok && store.Len() == 0
	}, 2*time.Second, 5*time.Millisecond,
		"destroy DockerEvent must evict the worldview entry")
}

// TestAgentStore_DoesNotEvictOnContainerDie pins the eviction predicate:
// die/stop/kill do NOT evict the worldview entry. A stopped container can
// be docker start-ed back into life; only destroy (docker rm) means the
// container is genuinely gone. A regression that widened the predicate to
// die would silently drop worldview for a merely-stopped agent.
func TestAgentStore_DoesNotEvictOnContainerDie(t *testing.T) {
	const cid = "c-noevict-1234567890ab"

	agentTopic := agentmocks.NewAgentTopic(t)
	dockerTopic := agentmocks.NewDockerTopic(t)
	store := agent.NewAgentStore()
	store.Subscribe(agentTopic)
	store.SubscribeDockerEvents(dockerTopic)

	publishAndAwait(t, agentTopic, store, agent.AgentEvent{
		Agent: agent.Agent{ContainerID: cid, AgentName: "dev", Project: "myapp"},
		Message: agent.Message{
			Type: agent.DialerEventType, Action: agent.ActionConnected, TimeNano: time.Now().UnixNano(),
		},
	})
	require.Equal(t, 1, store.Len())

	publishDocker(dockerTopic, mobyevents.Message{
		Type:   mobyevents.ContainerEventType,
		Action: mobyevents.ActionDie,
		Actor:  mobyevents.Actor{ID: cid},
	})

	// Give the (no-op) handler time to run, then assert the entry survives.
	time.Sleep(100 * time.Millisecond)
	_, ok := store.Get(cid)
	assert.True(t, ok, "die must not evict; a stopped container can be restarted")
	assert.Equal(t, 1, store.Len())
}

// mobyDestroy builds a container/destroy moby event for the given id.
func mobyDestroy(cid string) mobyevents.Message {
	return mobyevents.Message{
		Type:   mobyevents.ContainerEventType,
		Action: mobyevents.ActionDestroy,
		Actor:  mobyevents.Actor{ID: cid},
	}
}
