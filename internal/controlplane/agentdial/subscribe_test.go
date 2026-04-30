package agentdial

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// liveBus mirrors the helper in agentregistry — a started Overseer
// with deterministic options + auto-cleanup. Production wiring is
// what we want to exercise; mocking the bus would replace the very
// integration the consumer depends on.
func liveBus(t *testing.T) *overseer.Overseer {
	t.Helper()
	bus := overseer.New(overseer.Options{Logger: logger.Nop()})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

// Subscribe takes a concrete *Dialer; constructing one without a
// docker client would panic in runDial's first ContainerInspect.
// The filter, however, is a closure against ContainerStarted that
// runs at the bus layer — same predicate, same struct field.
// SubscribeFiltered with an identical closure exercises the unit
// (does the filter admit purpose=agent and reject everything else?)
// without dragging the rest of the Dialer's dependency graph in.

// TestSubscribe_FilterAdmitsPurposeAgent: the filter installed by
// Subscribe must let purpose=agent ContainerStarted through.
func TestSubscribe_FilterAdmitsPurposeAgent(t *testing.T) {
	bus := liveBus(t)
	predicate := func(ev dockerevents.ContainerStarted) bool {
		return ev.Labels[consts.LabelPurpose] == consts.PurposeAgent
	}
	sub, ok := overseer.SubscribeFiltered(bus, "agentdial-test", predicate)
	require.True(t, ok)
	t.Cleanup(sub.Unsubscribe)

	overseer.Publish(bus, dockerevents.ContainerStarted{
		ID:     "ctr-agent",
		Labels: map[string]string{consts.LabelPurpose: consts.PurposeAgent},
		At:     time.Now(),
	})

	select {
	case ev := <-sub.C:
		assert.Equal(t, "ctr-agent", ev.ID)
	case <-time.After(time.Second):
		t.Fatal("filter blocked a purpose=agent event")
	}
}

// TestSubscribe_FilterRejectsNonAgent: anything without
// LabelPurpose=agent (CP itself, host proxy, third-party
// containers) must be filtered out.
func TestSubscribe_FilterRejectsNonAgent(t *testing.T) {
	bus := liveBus(t)
	predicate := func(ev dockerevents.ContainerStarted) bool {
		return ev.Labels[consts.LabelPurpose] == consts.PurposeAgent
	}
	sub, ok := overseer.SubscribeFiltered(bus, "agentdial-test", predicate)
	require.True(t, ok)
	t.Cleanup(sub.Unsubscribe)

	overseer.Publish(bus, dockerevents.ContainerStarted{
		ID:     "ctr-other",
		Labels: map[string]string{consts.LabelPurpose: "host-proxy"},
		At:     time.Now(),
	})
	overseer.Publish(bus, dockerevents.ContainerStarted{
		ID:     "ctr-bare",
		Labels: nil,
		At:     time.Now(),
	})

	// Proof-by-absence: any value on sub.C within the wait window is
	// a regression. 50ms is enough for the bus to have applied the
	// filter; any longer would just slow the suite.
	select {
	case ev := <-sub.C:
		t.Fatalf("filter admitted a non-agent event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestPanicRingBuffer_BoundedMemory exercises the ring buffer path
// in Subscribe by driving the consumer through subscribePanicWindowMaxHits
// (Panic-ring-buffer tests removed in test cleanup: TestPanicRingBuffer_BoundedMemory
// was a structural twin of the production consumer that never invoked it, and
// TestPanicRingBuffer_Bounded asserted len([N]time.Time{}) == N — a compile-time
// tautology. The agentregistry equivalent (TestSubscribe_PanicStormTerminatesAtThreshold)
// drives the real consumer through threshold termination and is the canonical
// regression guard for the ring-buffer accounting.)
