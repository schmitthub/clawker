package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	mobyevents "github.com/moby/moby/api/types/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// --- reapOrphans ---------------------------------------------------------

func TestReapOrphans_DropsRowsForGoneContainers(t *testing.T) {
	reg := NewRegistry(nil)
	mustAddTestEntry(t, reg, "ctr-live", "live", "p")
	mustAddTestEntry(t, reg, "ctr-orphan-1", "ophan1", "p")
	mustAddTestEntry(t, reg, "ctr-orphan-2", "orphan2", "p")

	lister := func(context.Context) ([]string, error) {
		return []string{"ctr-live"}, nil
	}

	evicted, err := reapOrphans(context.Background(), reg, lister, logger.Nop())
	require.NoError(t, err)
	assert.Equal(t, 2, evicted)

	got := reg.Snapshot()
	require.Len(t, got, 1)
	assert.Equal(t, "ctr-live", got[0].ContainerID)
}

func TestReapOrphans_EmptyDockerListEvictsAll(t *testing.T) {
	reg := NewRegistry(nil)
	mustAddTestEntry(t, reg, "ctr-1", "a", "p")
	mustAddTestEntry(t, reg, "ctr-2", "b", "p")

	lister := func(context.Context) ([]string, error) { return nil, nil }
	evicted, err := reapOrphans(context.Background(), reg, lister, logger.Nop())
	require.NoError(t, err)
	assert.Equal(t, 2, evicted)
	assert.Empty(t, reg.Snapshot())
}

func TestReapOrphans_RetriesTransientListerFailure(t *testing.T) {
	reg := NewRegistry(nil)
	mustAddTestEntry(t, reg, "ctr-orphan", "a", "p")

	var attempts int32
	lister := func(context.Context) ([]string, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 2 {
			return nil, errors.New("transient")
		}
		return nil, nil // recovered → no live containers
	}
	evicted, err := reapOrphans(context.Background(), reg, lister, logger.Nop())
	require.NoError(t, err)
	assert.Equal(t, 1, evicted)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&attempts), int32(2))
}

func TestReapOrphans_ReportsListerExhaustion(t *testing.T) {
	reg := NewRegistry(nil)
	mustAddTestEntry(t, reg, "ctr-1", "a", "p")

	lister := func(context.Context) ([]string, error) {
		return nil, errors.New("daemon down")
	}
	_, err := reapOrphans(context.Background(), reg, lister, logger.Nop())
	require.Error(t, err)
	// Registry must NOT be wiped on lister failure — orphans persist
	// until a future successful reap (or destroy event).
	assert.Len(t, reg.Snapshot(), 1)
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
			reg := NewRegistry(nil)
			mustAddTestEntry(t, reg, "ctr-target", "a", "p")
			bus, cleanup := startTestBus(t)
			defer cleanup()

			cancelSub, err := subscribeEvict(t.Context(), reg, bus, logger.Nop())
			require.NoError(t, err)
			defer cancelSub()

			overseer.Publish(bus, dockerevents.DockerEvent{Message: mobyevents.Message{
				Type:   mobyevents.ContainerEventType,
				Action: tc.action,
				Actor:  mobyevents.Actor{ID: "ctr-target"},
			}})

			require.Eventually(t, func() bool {
				snap := reg.Snapshot()
				if tc.wantEvicted {
					return len(snap) == 0
				}
				return len(snap) == 1
			}, 500*time.Millisecond, 10*time.Millisecond)
		})
	}
}

// TestSubscribeEvict_NonContainerEventsIgnored: network/volume events
// should never be dispatched to the evict consumer.
func TestSubscribeEvict_NonContainerEventsIgnored(t *testing.T) {
	reg := NewRegistry(nil)
	mustAddTestEntry(t, reg, "ctr-1", "a", "p")
	bus, cleanup := startTestBus(t)
	defer cleanup()

	cancelSub, err := subscribeEvict(t.Context(), reg, bus, logger.Nop())
	require.NoError(t, err)
	defer cancelSub()

	// Non-container/destroy event with the same Actor.ID — must not
	// evict (network events use container-id-shaped IDs in some moby
	// versions; the type filter is the gate).
	overseer.Publish(bus, dockerevents.DockerEvent{Message: mobyevents.Message{
		Type:   mobyevents.NetworkEventType,
		Action: mobyevents.ActionDestroy,
		Actor:  mobyevents.Actor{ID: "ctr-1"},
	}})

	// Sleep briefly for the bus loop to run; row must persist.
	time.Sleep(100 * time.Millisecond)
	assert.Len(t, reg.Snapshot(), 1)
}

// --- subscribeDial: filter contract --------------------------------------

// TestSubscribeDial_FiltersOnPurposeAgent pins that ONLY events with
// the dev.clawker.purpose=agent label trigger DialAgent. CP itself,
// host proxy, and any other clawker-managed container must not be
// dialed.
func TestSubscribeDial_FiltersOnPurposeAgent(t *testing.T) {
	cases := []struct {
		name      string
		action    mobyevents.Action
		labels    map[string]string
		wantDial  bool
		wantDialN int
	}{
		{
			name:      "agent start dials",
			action:    mobyevents.ActionStart,
			labels:    map[string]string{consts.LabelPurpose: consts.PurposeAgent},
			wantDial:  true,
			wantDialN: 1,
		},
		{
			name:      "agent restart dials",
			action:    mobyevents.ActionRestart,
			labels:    map[string]string{consts.LabelPurpose: consts.PurposeAgent},
			wantDial:  true,
			wantDialN: 1,
		},
		{
			name:      "agent unpause dials",
			action:    mobyevents.ActionUnPause,
			labels:    map[string]string{consts.LabelPurpose: consts.PurposeAgent},
			wantDial:  true,
			wantDialN: 1,
		},
		{
			name:      "non-agent start does NOT dial",
			action:    mobyevents.ActionStart,
			labels:    map[string]string{consts.LabelPurpose: "host-proxy"},
			wantDial:  false,
			wantDialN: 0,
		},
		{
			name:      "agent create does NOT dial",
			action:    mobyevents.ActionCreate,
			labels:    map[string]string{consts.LabelPurpose: consts.PurposeAgent},
			wantDial:  false,
			wantDialN: 0,
		},
		{
			name:      "agent stop does NOT dial",
			action:    mobyevents.ActionStop,
			labels:    map[string]string{consts.LabelPurpose: consts.PurposeAgent},
			wantDial:  false,
			wantDialN: 0,
		},
		{
			name:      "missing purpose label does NOT dial",
			action:    mobyevents.ActionStart,
			labels:    map[string]string{},
			wantDial:  false,
			wantDialN: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus, cleanup := startTestBus(t)
			defer cleanup()

			// Drive the production dialEventPredicate (the same closure
			// subscribeDial wires onto the bus) against the test
			// channel. Pinning the named predicate prevents drift
			// between the test and the production filter.
			sub, ok := overseer.SubscribeFiltered(bus, "test-dial", dialEventPredicate)
			require.True(t, ok)
			defer sub.Unsubscribe()

			overseer.Publish(bus, dockerevents.DockerEvent{Message: mobyevents.Message{
				Type:   mobyevents.ContainerEventType,
				Action: tc.action,
				Actor: mobyevents.Actor{
					ID:         "ctr-1",
					Attributes: tc.labels,
				},
			}})

			if tc.wantDial {
				select {
				case <-sub.C:
					// ok
				case <-time.After(200 * time.Millisecond):
					t.Fatal("expected event to pass filter, none arrived")
				}
			} else {
				select {
				case <-sub.C:
					t.Fatal("event arrived but should have been filtered out")
				case <-time.After(100 * time.Millisecond):
					// ok
				}
			}
		})
	}
}

// --- agent.Start: nil-deps validation -----------------------------------

func TestStart_RejectsNilDeps(t *testing.T) {
	bus, cleanup := startTestBus(t)
	defer cleanup()
	reg := NewRegistry(nil)
	dialer := &Dialer{log: logger.Nop(), dialing: make(map[string]struct{})}
	lister := ContainerLister(func(context.Context) ([]string, error) { return nil, nil })

	cases := []struct {
		name string
		deps StartDeps
	}{
		{"nil registry", StartDeps{Bus: bus, DockerLister: lister, Dialer: dialer}},
		{"nil docker lister", StartDeps{Bus: bus, Registry: reg, Dialer: dialer}},
		{"nil bus", StartDeps{Registry: reg, DockerLister: lister, Dialer: dialer}},
		{"nil dialer", StartDeps{Bus: bus, Registry: reg, DockerLister: lister}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Start(t.Context(), tc.deps)
			require.Error(t, err)
		})
	}
}

// TestStart_PublishesReapDegradedOnListerFailure pins the new event
// surface: when reap fails (lister exhausts retries), Start
// publishes ReapDegraded so worldview consumers know orphans may
// persist.
func TestStart_PublishesReapDegradedOnListerFailure(t *testing.T) {
	bus, cleanup := startTestBus(t)
	defer cleanup()

	sub, ok := overseer.Subscribe[ReapDegraded](bus, "test-reap")
	require.True(t, ok)
	defer sub.Unsubscribe()

	reg := NewRegistry(nil)
	mustAddTestEntry(t, reg, "ctr-orphan", "a", "p")
	dialer := &Dialer{log: logger.Nop(), dialing: make(map[string]struct{})}
	lister := ContainerLister(func(context.Context) ([]string, error) {
		return nil, errors.New("daemon down")
	})

	cleanupStart, err := Start(t.Context(), StartDeps{
		Registry:     reg,
		DockerLister: lister,
		Dialer:       dialer,
		Bus:          bus,
		Log:          logger.Nop(),
	})
	require.NoError(t, err, "Start must NOT fail on reap failure (soft-fail)")
	defer cleanupStart()

	select {
	case ev := <-sub.C:
		assert.Contains(t, ev.Reason, "daemon down")
	case <-time.After(2 * time.Second):
		t.Fatal("expected ReapDegraded event")
	}
}

// --- helpers -------------------------------------------------------------

func mustAddTestEntry(t *testing.T, reg Registry, containerID, agentName, project string) {
	t.Helper()
	entry := Entry{
		AgentName:    agentName,
		Project:      project,
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

func startTestBus(t *testing.T) (*overseer.Overseer, func()) {
	t.Helper()
	bus := overseer.New(overseer.Options{Logger: logger.Nop()})
	require.NoError(t, bus.Start(t.Context()))
	return bus, func() { _ = bus.Close() }
}
