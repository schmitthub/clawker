package agent_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mobyevents "github.com/moby/moby/api/types/events"
	"github.com/schmitthub/clawker/controlplane/agent"
	agentmocks "github.com/schmitthub/clawker/controlplane/agent/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/dockerevents"
	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// noopPeerLookup satisfies ContainerByPeerIP for tests that don't
// exercise the resolver path. Current agent.Start() doesn't invoke
// LookupByIP; the dep is held for downstream consumers.
type noopPeerLookup struct{}

func (noopPeerLookup) LookupByIP(_ context.Context, _ netip.Addr) (agent.ResolvedContainer, error) {
	return agent.ResolvedContainer{}, agent.ErrNoContainerForPeerIP
}

// publishDocker offers a DockerEvent onto the topic.
func publishDocker(topic *pubsub.Topic[dockerevents.DockerEvent], msg mobyevents.Message) {
	topic.Publish(pubsub.Event[dockerevents.DockerEvent]{Payload: dockerevents.DockerEvent{Message: msg}})
}

// --- reapOrphans ---------------------------------------------------------

func TestReapOrphans_DropsRowsForGoneContainers(t *testing.T) {
	reg := agent.NewRegistry(nil)
	mustAddTestEntry(t, reg, "ctr-live", "live")
	mustAddTestEntry(t, reg, "ctr-orphan-1", "ophan1")
	mustAddTestEntry(t, reg, "ctr-orphan-2", "orphan2")

	lister := func(context.Context) ([]string, error) {
		return []string{"ctr-live"}, nil
	}

	evicted, err := agent.ReapOrphans(context.Background(), reg, lister, logger.Nop())
	require.NoError(t, err)
	assert.Equal(t, 2, evicted)

	got, snapErr := reg.Snapshot()
	require.NoError(t, snapErr)
	require.Len(t, got, 1)
	assert.Equal(t, "ctr-live", got[0].ContainerID)
}

func TestReapOrphans_EmptyDockerListEvictsAll(t *testing.T) {
	reg := agent.NewRegistry(nil)
	mustAddTestEntry(t, reg, "ctr-1", "a")
	mustAddTestEntry(t, reg, "ctr-2", "b")

	lister := func(context.Context) ([]string, error) { return nil, nil }
	evicted, err := agent.ReapOrphans(context.Background(), reg, lister, logger.Nop())
	require.NoError(t, err)
	assert.Equal(t, 2, evicted)
	snap, snapErr := reg.Snapshot()
	require.NoError(t, snapErr)
	assert.Empty(t, snap)
}

func TestReapOrphans_RetriesTransientListerFailure(t *testing.T) {
	reg := agent.NewRegistry(nil)
	mustAddTestEntry(t, reg, "ctr-orphan", "a")

	var attempts int32
	lister := func(context.Context) ([]string, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 2 {
			return nil, errors.New("transient")
		}
		return nil, nil // recovered → no live containers
	}
	evicted, err := agent.ReapOrphans(context.Background(), reg, lister, logger.Nop())
	require.NoError(t, err)
	assert.Equal(t, 1, evicted)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&attempts), int32(2))
}

func TestReapOrphans_ReportsListerExhaustion(t *testing.T) {
	reg := agent.NewRegistry(nil)
	mustAddTestEntry(t, reg, "ctr-1", "a")

	lister := func(context.Context) ([]string, error) {
		return nil, errors.New("daemon down")
	}
	_, err := agent.ReapOrphans(context.Background(), reg, lister, logger.Nop())
	require.Error(t, err)
	// agent.Registry must NOT be wiped on lister failure — orphans persist
	// until a future successful reap (or destroy event).
	snap, snapErr := reg.Snapshot()
	require.NoError(t, snapErr)
	assert.Len(t, snap, 1)
}

// --- subscribeEvict: filter contract -------------------------------------

// TestSubscribeEvict_OnlyDestroyEvicts pins the load-bearing filter:
// container/destroy is the ONLY action that evicts. die/stop/kill MUST
// NOT evict — a stopped container can be docker start-ed back into
// life and the registry row should survive. A regression that added
// die to the filter would silently break agent restart resilience.
func TestSubscribeEvict_OnlyDestroyEvicts(t *testing.T) {
	cases := []struct {
		name        string
		action      mobyevents.Action
		wantEvicted bool
	}{
		{"destroy evicts", mobyevents.ActionDestroy, true},
		{"stop does not evict", mobyevents.ActionStop, false},
		{"die does not evict", mobyevents.ActionDie, false},
		{"kill does not evict", mobyevents.ActionKill, false},
		{"oom does not evict", mobyevents.ActionOOM, false},
		{"start does not evict", mobyevents.ActionStart, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := agent.NewRegistry(nil)
			mustAddTestEntry(t, reg, "ctr-target", "a")
			topic := agentmocks.NewDockerTopic(t)

			agent.SubscribeEvict(t.Context(), topic, reg, logger.Nop())

			publishDocker(topic, mobyevents.Message{
				Type:   mobyevents.ContainerEventType,
				Action: tc.action,
				Actor:  mobyevents.Actor{ID: "ctr-target"},
			})

			require.Eventually(t, func() bool {
				snap, err := reg.Snapshot()
				if err != nil {
					return false
				}
				if tc.wantEvicted {
					return len(snap) == 0
				}
				return len(snap) == 1
			}, 500*time.Millisecond, 10*time.Millisecond)
		})
	}
}

// fakeCanceller records every CancelDial call so tests can pin the
// session-cancel subscriber's behavior without a real *Dialer.
type fakeCanceller struct {
	mu  sync.Mutex
	ids []string
}

func (f *fakeCanceller) CancelDial(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids = append(f.ids, id)
}

func (f *fakeCanceller) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.ids))
	copy(out, f.ids)
	return out
}

// TestSubscribeSessionCancel_CancelsOnExitTransitions pins the docker
// events that trigger CancelDial. The trust anchor is the docker
// event, NOT registry state — die/stop/kill/oom/destroy all mean
// clawkerd is gone (or about to be), so the Session should tear down
// regardless of whether the registry row gets evicted (only destroy
// evicts).
func TestSubscribeSessionCancel_CancelsOnExitTransitions(t *testing.T) {
	cases := []struct {
		name       string
		action     mobyevents.Action
		wantCancel bool
	}{
		{"die cancels", mobyevents.ActionDie, true},
		{"stop cancels", mobyevents.ActionStop, true},
		{"kill cancels", mobyevents.ActionKill, true},
		{"oom cancels", mobyevents.ActionOOM, true},
		{"destroy cancels", mobyevents.ActionDestroy, true},
		{"start does not cancel", mobyevents.ActionStart, false},
		{"restart does not cancel", mobyevents.ActionRestart, false},
		{"unpause does not cancel", mobyevents.ActionUnPause, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			topic := agentmocks.NewDockerTopic(t)
			canc := &fakeCanceller{}

			agent.SubscribeSessionCancel(t.Context(), topic, canc, logger.Nop())

			publishDocker(topic, mobyevents.Message{
				Type:   mobyevents.ContainerEventType,
				Action: tc.action,
				Actor:  mobyevents.Actor{ID: "ctr-target"},
			})

			if tc.wantCancel {
				require.Eventually(t, func() bool {
					calls := canc.calls()
					return len(calls) == 1 && calls[0] == "ctr-target"
				}, 500*time.Millisecond, 10*time.Millisecond)
			} else {
				time.Sleep(100 * time.Millisecond)
				assert.Empty(t, canc.calls(), "non-exit transition must not cancel")
			}
		})
	}
}

// TestSubscribeSessionCancel_NonContainerEventsIgnored: network/volume
// events with matching action names must not trigger CancelDial.
func TestSubscribeSessionCancel_NonContainerEventsIgnored(t *testing.T) {
	topic := agentmocks.NewDockerTopic(t)
	canc := &fakeCanceller{}

	agent.SubscribeSessionCancel(t.Context(), topic, canc, logger.Nop())

	publishDocker(topic, mobyevents.Message{
		Type:   mobyevents.NetworkEventType,
		Action: mobyevents.ActionDie,
		Actor:  mobyevents.Actor{ID: "ctr-target"},
	})

	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, canc.calls())
}

// TestSubscribeEvict_NonContainerEventsIgnored: network/volume events
// should never be dispatched to the evict consumer.
func TestSubscribeEvict_NonContainerEventsIgnored(t *testing.T) {
	reg := agent.NewRegistry(nil)
	mustAddTestEntry(t, reg, "ctr-1", "a")
	topic := agentmocks.NewDockerTopic(t)

	agent.SubscribeEvict(t.Context(), topic, reg, logger.Nop())

	// Non-container/destroy event with the same Actor.ID — must not
	// evict (network events use container-id-shaped IDs in some moby
	// versions; the type filter is the gate).
	publishDocker(topic, mobyevents.Message{
		Type:   mobyevents.NetworkEventType,
		Action: mobyevents.ActionDestroy,
		Actor:  mobyevents.Actor{ID: "ctr-1"},
	})

	// Sleep briefly for the drain goroutine to run; row must persist.
	time.Sleep(100 * time.Millisecond)
	snap, snapErr := reg.Snapshot()
	require.NoError(t, snapErr)
	assert.Len(t, snap, 1)
}

// --- subscribeDial: filter contract --------------------------------------

// TestDialEvent_FiltersOnPurposeAgent pins that ONLY events with the
// dev.clawker.purpose=agent label pass the dial predicate. CP itself,
// host proxy, and any other clawker-managed container must not be
// dialed. Drives the production dialEvent predicate directly so the test
// and the subscribeDial wiring cannot drift.
func TestDialEvent_FiltersOnPurposeAgent(t *testing.T) {
	cases := []struct {
		name     string
		action   mobyevents.Action
		labels   map[string]string
		wantDial bool
	}{
		{
			name:     "agent start dials",
			action:   mobyevents.ActionStart,
			labels:   map[string]string{consts.LabelPurpose: consts.PurposeAgent},
			wantDial: true,
		},
		{
			name:     "agent restart dials",
			action:   mobyevents.ActionRestart,
			labels:   map[string]string{consts.LabelPurpose: consts.PurposeAgent},
			wantDial: true,
		},
		{
			name:     "agent unpause dials",
			action:   mobyevents.ActionUnPause,
			labels:   map[string]string{consts.LabelPurpose: consts.PurposeAgent},
			wantDial: true,
		},
		{
			name:     "non-agent start does NOT dial",
			action:   mobyevents.ActionStart,
			labels:   map[string]string{consts.LabelPurpose: "host-proxy"},
			wantDial: false,
		},
		{
			name:     "agent create does NOT dial",
			action:   mobyevents.ActionCreate,
			labels:   map[string]string{consts.LabelPurpose: consts.PurposeAgent},
			wantDial: false,
		},
		{
			name:     "agent stop does NOT dial",
			action:   mobyevents.ActionStop,
			labels:   map[string]string{consts.LabelPurpose: consts.PurposeAgent},
			wantDial: false,
		},
		{
			name:     "missing purpose label does NOT dial",
			action:   mobyevents.ActionStart,
			labels:   map[string]string{},
			wantDial: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := dockerevents.DockerEvent{Message: mobyevents.Message{
				Type:   mobyevents.ContainerEventType,
				Action: tc.action,
				Actor: mobyevents.Actor{
					ID:         "ctr-1",
					Attributes: tc.labels,
				},
			}}
			assert.Equal(t, tc.wantDial, agent.DialEvent(ev))
		})
	}
}

// --- agent.Start: nil-deps validation -----------------------------------

func TestStart_RejectsNilDeps(t *testing.T) {
	dockerTopic := agentmocks.NewDockerTopic(t)
	agentTopic := agentmocks.NewAgentTopic(t)
	reg := agent.NewRegistry(nil)
	dialer := &agent.Dialer{Log: logger.Nop(), Dialing: make(map[string]context.CancelFunc)}
	lister := agent.ContainerListFunc(func(context.Context) ([]string, error) { return nil, nil })
	peerLookup := noopPeerLookup{}

	cases := []struct {
		name string
		deps agent.StartDeps
	}{
		{"nil registry", agent.StartDeps{DockerTopic: dockerTopic, AgentTopic: agentTopic, DockerLister: lister, Dialer: dialer, PeerLookup: peerLookup}},
		{"nil docker lister", agent.StartDeps{DockerTopic: dockerTopic, AgentTopic: agentTopic, Registry: reg, Dialer: dialer, PeerLookup: peerLookup}},
		{"nil docker topic", agent.StartDeps{AgentTopic: agentTopic, Registry: reg, DockerLister: lister, Dialer: dialer, PeerLookup: peerLookup}},
		{"nil agent topic", agent.StartDeps{DockerTopic: dockerTopic, Registry: reg, DockerLister: lister, Dialer: dialer, PeerLookup: peerLookup}},
		{"nil dialer", agent.StartDeps{DockerTopic: dockerTopic, AgentTopic: agentTopic, Registry: reg, DockerLister: lister, PeerLookup: peerLookup}},
		{"nil peer lookup", agent.StartDeps{DockerTopic: dockerTopic, AgentTopic: agentTopic, Registry: reg, DockerLister: lister, Dialer: dialer}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := agent.Start(t.Context(), tc.deps)
			require.Error(t, err)
		})
	}
}

// TestStart_PublishesReapDegradedOnListerFailure pins the new event
// surface: when reap fails (lister exhausts retries), Start publishes a
// agent.RegistryEventType/agent.ActionReap AgentEvent so worldview consumers know
// orphans may persist.
func TestStart_PublishesReapDegradedOnListerFailure(t *testing.T) {
	dockerTopic := agentmocks.NewDockerTopic(t)
	agentTopic := agentmocks.NewAgentTopic(t)
	rec := agentmocks.RecordAgent(agentTopic)

	reg := agent.NewRegistry(nil)
	mustAddTestEntry(t, reg, "ctr-orphan", "a")
	dialer := &agent.Dialer{Log: logger.Nop(), Dialing: make(map[string]context.CancelFunc)}
	lister := agent.ContainerListFunc(func(context.Context) ([]string, error) {
		return nil, errors.New("daemon down")
	})

	cleanupStart, err := agent.Start(t.Context(), agent.StartDeps{
		Registry:     reg,
		DockerLister: lister,
		PeerLookup:   noopPeerLookup{},
		Dialer:       dialer,
		DockerTopic:  dockerTopic,
		AgentTopic:   agentTopic,
		Log:          logger.Nop(),
	})
	require.NoError(t, err, "Start must NOT fail on reap failure (soft-fail)")
	defer cleanupStart()

	require.Eventually(t, func() bool {
		ev, ok := rec.FirstWith(agent.RegistryEventType, agent.ActionReap)
		return ok && strings.Contains(ev.Message.Detail, "daemon down")
	}, 2*time.Second, 10*time.Millisecond, "expected reap-degraded AgentEvent")
}

// --- helpers -------------------------------------------------------------

func mustAddTestEntry(t *testing.T, reg agent.Registry, containerID, agentName string) {
	t.Helper()
	entry := agent.Entry{
		AgentName:    auth.MustAgentName(agentName),
		Project:      auth.MustProjectSlug("p"),
		ContainerID:  containerID,
		Thumbprint:   testThumb(containerID),
		RegisteredAt: time.Unix(1000, 0),
	}
	require.NoError(t, reg.Add(entry))
}

func testThumb(s string) [32]byte {
	var t [32]byte
	copy(t[:], s)
	return t
}
