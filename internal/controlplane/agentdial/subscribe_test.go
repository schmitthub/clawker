package agentdial

import (
	"context"
	"testing"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	regmocks "github.com/schmitthub/clawker/internal/controlplane/agentregistry/mocks"
	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// mkContainerEvent builds a wire-equivalent DockerEvent envelope for
// tests. Mirrors the moby Message a real docker daemon would send;
// subscribers see the same shape they'd receive in production.
func mkContainerEvent(action events.Action, id string, labels map[string]string) dockerevents.DockerEvent {
	attrs := make(map[string]string, len(labels)+2)
	attrs["name"] = id
	attrs["image"] = "alpine"
	for k, v := range labels {
		attrs[k] = v
	}
	return dockerevents.DockerEvent{Message: events.Message{
		Type:     events.ContainerEventType,
		Action:   action,
		Actor:    events.Actor{ID: id, Attributes: attrs},
		TimeNano: time.Now().UnixNano(),
	}}
}

// liveBus mirrors the agentregistry test helper: real Overseer, Started,
// auto-closed via t.Cleanup. Subscribe is integration-shaped — replacing
// the bus with a mock would replace the very wiring the test pins.
func liveBus(t *testing.T) *overseer.Overseer {
	t.Helper()
	bus := overseer.New(overseer.Options{Logger: logger.Nop()})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

// dialerForSubscribe builds a real *Dialer with a fakeMobyForDialer that
// returns errContainerStopped on inspect — every DialAgent terminates
// quickly with outcomeContainerGone → SessionFailed{Reason="container_not_running"}.
// The SessionFailed event on the same bus is the assertion surface.
func dialerForSubscribe(t *testing.T, bus *overseer.Overseer) *Dialer {
	t.Helper()
	docker := &fakeMobyForDialer{}
	regMock := &regmocks.RegistryMock{
		LookupByContainerIDFunc: func(string) (*agentregistry.Entry, error) {
			return nil, agentregistry.ErrUnknownAgent
		},
	}
	d, _, _, cancel := newDialerForTest(t, docker, regMock)
	t.Cleanup(cancel)
	// Re-bind d to the test's bus — newDialerForTest creates its own,
	// but for filter tests we need the dialer's publishes to land on
	// the same bus we subscribe on.
	d.bus = bus
	return d
}

func TestSubscribe_DialsOnPurposeAgentContainerStarted(t *testing.T) {
	bus := liveBus(t)
	d := dialerForSubscribe(t, bus)

	sub, ok := overseer.Subscribe[SessionFailed](bus, "test")
	require.True(t, ok)

	cancel := Subscribe(context.Background(), d, bus, logger.Nop())
	t.Cleanup(cancel)

	overseer.Publish(bus, mkContainerEvent(events.ActionStart, "ctr-agent", map[string]string{consts.LabelPurpose: consts.PurposeAgent}))

	select {
	case ev := <-sub.C:
		assert.Equal(t, "ctr-agent", ev.ContainerID)
		assert.Equal(t, "container_not_running", ev.Reason)
	case <-time.After(2 * time.Second):
		t.Fatal("agent ContainerStarted did not trigger DialAgent within deadline")
	}
}

// TestSubscribe_DialsOnPurposeAgentContainerRestarted — moby's
// `restart` action publishes ContainerRestarted; Subscribe matches
// the running-transition union so a restart of an agent container
// drives a fresh dial cycle.
func TestSubscribe_DialsOnPurposeAgentContainerRestarted(t *testing.T) {
	bus := liveBus(t)
	d := dialerForSubscribe(t, bus)

	sub, ok := overseer.Subscribe[SessionFailed](bus, "test")
	require.True(t, ok)

	cancel := Subscribe(context.Background(), d, bus, logger.Nop())
	t.Cleanup(cancel)

	overseer.Publish(bus, mkContainerEvent(events.ActionRestart, "ctr-agent-r", map[string]string{consts.LabelPurpose: consts.PurposeAgent}))

	select {
	case ev := <-sub.C:
		assert.Equal(t, "ctr-agent-r", ev.ContainerID)
	case <-time.After(2 * time.Second):
		t.Fatal("agent ContainerRestarted did not trigger DialAgent within deadline")
	}
}

// TestSubscribe_DialsOnPurposeAgentContainerUnpaused — same union as
// Restarted; unpause re-establishes a session against a previously
// paused agent container.
func TestSubscribe_DialsOnPurposeAgentContainerUnpaused(t *testing.T) {
	bus := liveBus(t)
	d := dialerForSubscribe(t, bus)

	sub, ok := overseer.Subscribe[SessionFailed](bus, "test")
	require.True(t, ok)

	cancel := Subscribe(context.Background(), d, bus, logger.Nop())
	t.Cleanup(cancel)

	overseer.Publish(bus, mkContainerEvent(events.ActionUnPause, "ctr-agent-u", map[string]string{consts.LabelPurpose: consts.PurposeAgent}))

	select {
	case ev := <-sub.C:
		assert.Equal(t, "ctr-agent-u", ev.ContainerID)
	case <-time.After(2 * time.Second):
		t.Fatal("agent ContainerUnpaused did not trigger DialAgent within deadline")
	}
}

func TestSubscribe_IgnoresNonAgentContainerStarted(t *testing.T) {
	// Filter regression guard: a ContainerStarted with no purpose label
	// (or a non-agent purpose) must never reach DialAgent. Without the
	// filter, the CP would dial its own non-agent containers (CP itself,
	// host proxy, hostproxytest) every time they restart.
	bus := liveBus(t)
	d := dialerForSubscribe(t, bus)

	sub, ok := overseer.Subscribe[SessionFailed](bus, "test")
	require.True(t, ok)

	cancel := Subscribe(context.Background(), d, bus, logger.Nop())
	t.Cleanup(cancel)

	overseer.Publish(bus, mkContainerEvent(events.ActionStart, "ctr-non-agent", map[string]string{consts.LabelPurpose: "host-proxy"}))
	overseer.Publish(bus, mkContainerEvent(events.ActionStart, "ctr-no-label", nil))

	select {
	case ev := <-sub.C:
		t.Fatalf("filter violated — non-agent ContainerStarted triggered DialAgent: %+v", ev)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestSubscribe_CancelStopsConsumer(t *testing.T) {
	bus := liveBus(t)
	d := dialerForSubscribe(t, bus)

	cancel := Subscribe(context.Background(), d, bus, logger.Nop())

	done := make(chan struct{})
	go func() {
		cancel()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel did not return; consumer goroutine likely leaked")
	}
}

func TestSubscribe_NilLogTolerated(t *testing.T) {
	// Subscribe documents nil-log → logger.Nop() fallback. A regression
	// that dereferences nil before the fallback would panic at consumer
	// construction.
	bus := liveBus(t)
	d := dialerForSubscribe(t, bus)
	cancel := Subscribe(context.Background(), d, bus, nil)
	t.Cleanup(cancel)
}
