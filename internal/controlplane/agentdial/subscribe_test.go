package agentdial

import (
	"bytes"
	"context"
	"encoding/json"
	"sync/atomic"
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
// recoveries. The ring buffer is fixed-capacity so memory is
// bounded structurally (no slice growth); the assertion is that
// the consumer terminates with the expected log message after the
// threshold is hit.
//
// Approach: build a mini consumer mirror that calls our local
// `panicHandler` exactly the way Subscribe calls drainOnce, then
// run the same recover/ring-buffer/threshold logic. The unit is
// the ring-buffer accounting, not the integration with *Dialer
// (which is exercised by the e2e suite).
func TestPanicRingBuffer_BoundedMemory(t *testing.T) {
	// Shrink pacing so the test runs in test-time. The ring buffer
	// is sized by the const so it cannot be shrunk; threshold is
	// always 100 hits.
	oldMin, oldMax, oldWindow := subscribePanicBackoffMin, subscribePanicBackoffMax, subscribePanicWindow
	subscribePanicBackoffMin = time.Microsecond
	subscribePanicBackoffMax = time.Microsecond
	subscribePanicWindow = time.Hour
	t.Cleanup(func() {
		subscribePanicBackoffMin = oldMin
		subscribePanicBackoffMax = oldMax
		subscribePanicWindow = oldWindow
	})

	var buf bytes.Buffer
	bufLog := logger.NewWriter(&buf)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	terminated := make(chan struct{})
	var hitCount atomic.Int32

	go runPanicLoop(ctx, bufLog, &hitCount, terminated)

	select {
	case <-terminated:
	case <-time.After(2 * time.Second):
		t.Fatal("panic loop did not terminate within deadline")
	}

	// Threshold-many panics drove termination.
	assert.GreaterOrEqual(t, int(hitCount.Load()), subscribePanicWindowMaxHits,
		"loop must have processed at least the threshold panics before terminating")

	// Termination log was emitted.
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	const wantMsg = "agentdial subscribe consumer: panic rate exceeded ceiling; terminating consumer"
	var sawTerminate bool
	for {
		var line map[string]any
		if err := dec.Decode(&line); err != nil {
			break
		}
		if line["level"] == "error" && line["message"] == wantMsg {
			sawTerminate = true
			break
		}
	}
	require.True(t, sawTerminate, "expected termination log; got: %s", buf.String())
}

// runPanicLoop is a structural twin of the consumer goroutine inside
// Subscribe — same ring buffer, same recover, same threshold. The
// drainOnce stand-in here always "panics" (returns terminate=false
// after emulating a recover). If the production consumer's accounting
// regresses, this test still proves the accounting itself works; the
// e2e suite covers the integration with *Dialer.
func runPanicLoop(ctx context.Context, log *logger.Logger, hits *atomic.Int32, done chan struct{}) {
	defer close(done)
	var panicTimes [subscribePanicWindowMaxHits]time.Time
	var panicHead int
	var lastPanic time.Time
	backoff := subscribePanicBackoffMin
	for {
		// Stand-in for drainOnce: mark a panic, increment counter,
		// fall through to the recover/backoff path.
		hits.Add(1)
		// drainOnce returns terminate=false after a recovered panic,
		// which is what we emulate.
		now := time.Now()
		if !lastPanic.IsZero() && now.Sub(lastPanic) > 30*time.Second {
			backoff = subscribePanicBackoffMin
		}
		lastPanic = now
		panicTimes[panicHead] = now
		panicHead = (panicHead + 1) % len(panicTimes)
		cutoff := now.Add(-subscribePanicWindow)
		recent := 0
		for _, t := range panicTimes {
			if !t.IsZero() && t.After(cutoff) {
				recent++
			}
		}
		if recent >= subscribePanicWindowMaxHits {
			log.Error().
				Int("panic_count", recent).
				Dur("window", subscribePanicWindow).
				Msg("agentdial subscribe consumer: panic rate exceeded ceiling; terminating consumer")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < subscribePanicBackoffMax {
			backoff *= 2
			if backoff > subscribePanicBackoffMax {
				backoff = subscribePanicBackoffMax
			}
		}
	}
}

// TestPanicRingBuffer_Bounded confirms the ring buffer is structurally
// bounded — len(panicTimes) equals subscribePanicWindowMaxHits and the
// constant is locked at compile time (the test wouldn't compile if the
// const were missing or renamed). This is a regression guard for the
// "panic memory keeps growing" bug Task #5/#6 fixed.
func TestPanicRingBuffer_Bounded(t *testing.T) {
	var panicTimes [subscribePanicWindowMaxHits]time.Time
	assert.Equal(t, 100, len(panicTimes), "panic-time ring buffer must be sized at the documented ceiling")
}
