package agentregistry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// liveBus constructs and starts an Overseer with deterministic options,
// returning a started instance plus cleanup. Reuses the production bus
// rather than mocking — the eviction contract is "what the bus
// publishes drives EvictByContainerID", and replacing the bus with a
// mock would replace the very integration this test exists to assert.
func liveBus(t *testing.T) *overseer.Overseer {
	t.Helper()
	bus := overseer.New(overseer.Options{Logger: logger.Nop()})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

func TestSubscribe_EvictsOnContainerRemoved(t *testing.T) {
	bus := liveBus(t)
	r := NewRegistry(nil)
	r.Add(Entry{AgentName: "x", ContainerID: "ctr-evict", Thumbprint: tp("cert"), RegisteredAt: time.Now()})

	cancel := Subscribe(context.Background(), r, bus, logger.Nop())
	t.Cleanup(cancel)

	overseer.Publish(bus, dockerevents.ContainerRemoved{ID: "ctr-evict", At: time.Now()})

	waitFor(t, func() bool {
		_, err := r.Lookup(tp("cert"), canonical("", "x"))
		return err == ErrUnknownAgent
	})
}

// TestSubscribe_DoesNotEvictOnStopped — a stopped container can be
// `docker start`-ed back into life and the same registry row should
// pick up where it left off. Only ContainerRemoved is the eviction
// trigger; ContainerStopped (die / stop / kill) must not touch the
// row. The CP startup reaper handles the case where a stopped
// container is removed while CP is down.
func TestSubscribe_DoesNotEvictOnStopped(t *testing.T) {
	bus := liveBus(t)
	r := NewRegistry(nil)
	r.Add(Entry{AgentName: "y", ContainerID: "ctr-stopped", Thumbprint: tp("cert-y"), RegisteredAt: time.Now()})

	cancel := Subscribe(context.Background(), r, bus, logger.Nop())
	t.Cleanup(cancel)

	overseer.Publish(bus, dockerevents.ContainerStarted{ID: "ctr-stopped", At: time.Now()})
	overseer.Publish(bus, dockerevents.ContainerStopped{ID: "ctr-stopped", At: time.Now()})

	// Proof-by-absence: poll for a stable window. A sleep too short on
	// a loaded runner could pass for the wrong reason (consumer hasn't
	// drained the event yet).
	const window = 100 * time.Millisecond
	const interval = 5 * time.Millisecond
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		got, err := r.Lookup(tp("cert-y"), canonical("", "y"))
		require.NoError(t, err, "stopped must not evict registered entry")
		assert.Equal(t, "y", got.AgentName)
		time.Sleep(interval)
	}
}

func TestSubscribe_CancelStopsConsumer(t *testing.T) {
	bus := liveBus(t)
	r := NewRegistry(nil)
	cancel := Subscribe(context.Background(), r, bus, logger.Nop())

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

// panicOnceRegistry is a Registry test double whose EvictByContainerID
// panics on its first call and then delegates to a real Registry for
// every subsequent call.
type panicOnceRegistry struct {
	calls    atomic.Int32
	panicked atomic.Bool
	delegate Registry
}

func (p *panicOnceRegistry) Add(e Entry) error { return p.delegate.Add(e) }
func (p *panicOnceRegistry) Lookup(t [sha256.Size]byte, cn string) (*Entry, error) {
	return p.delegate.Lookup(t, cn)
}
func (p *panicOnceRegistry) LookupByContainerID(id string) (*Entry, error) {
	return p.delegate.LookupByContainerID(id)
}
func (p *panicOnceRegistry) LookupByThumbprint(t [sha256.Size]byte) (*Entry, error) {
	return p.delegate.LookupByThumbprint(t)
}
func (p *panicOnceRegistry) Snapshot() []Entry { return p.delegate.Snapshot() }
func (p *panicOnceRegistry) EvictByContainerID(id string) error {
	p.calls.Add(1)
	if p.panicked.CompareAndSwap(false, true) {
		panic("synthetic eviction-hook panic")
	}
	return p.delegate.EvictByContainerID(id)
}

func TestSubscribe_RecoversFromHookPanic(t *testing.T) {
	// A panic in EvictByContainerID must not kill the consumer
	// goroutine — otherwise registered agents' Thumbprint entries
	// would keep authorizing per-agent RPCs after their containers
	// are gone.
	bus := liveBus(t)

	var buf bytes.Buffer
	bufLog := logger.NewWriter(&buf)

	delegate := NewRegistry(nil)
	delegate.Add(Entry{AgentName: "first", ContainerID: "ctr-first", Thumbprint: tp("cert-first"), RegisteredAt: time.Now()})
	delegate.Add(Entry{AgentName: "second", ContainerID: "ctr-second", Thumbprint: tp("cert-second"), RegisteredAt: time.Now()})

	reg := &panicOnceRegistry{delegate: delegate}

	cancel := Subscribe(context.Background(), reg, bus, bufLog)
	t.Cleanup(cancel)

	// First event — triggers the panic. The entry must still be in the
	// registry afterward (the panic prevented the eviction) and the
	// consumer must still be alive.
	overseer.Publish(bus, dockerevents.ContainerRemoved{ID: "ctr-first", At: time.Now()})

	// Wait for the panic to actually fire before sending the second
	// event. Otherwise we race the consumer and the second event can
	// arrive before EvictByContainerID has been entered the first time.
	waitFor(t, func() bool { return reg.panicked.Load() })

	// Second event — must be processed by the resumed consumer,
	// proving subsequent events still drain after a recovered panic.
	overseer.Publish(bus, dockerevents.ContainerRemoved{ID: "ctr-second", At: time.Now()})

	waitFor(t, func() bool {
		_, err := delegate.Lookup(tp("cert-second"), canonical("", "second"))
		return err == ErrUnknownAgent
	})

	// First entry was never evicted because the panic prevented it.
	got, err := delegate.Lookup(tp("cert-first"), canonical("", "first"))
	require.NoError(t, err, "first entry must survive the panicked eviction call")
	assert.Equal(t, "first", got.AgentName)

	// Recover must have logged at error level so an operator can
	// notice the dropped event.
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	var sawPanicLog bool
	for {
		var line map[string]any
		if err := dec.Decode(&line); err != nil {
			break
		}
		if line["level"] == "error" && line["panic"] == "synthetic eviction-hook panic" {
			sawPanicLog = true
			break
		}
	}
	assert.True(t, sawPanicLog, "expected an error-level log entry capturing the recovered panic; got: %s", buf.String())
}

// alwaysPanicRegistry is a Registry test double whose
// EvictByContainerID always panics. Used by the panic-storm test to
// drive the consumer into its termination path.
type alwaysPanicRegistry struct {
	calls    atomic.Int32
	delegate Registry
}

func (p *alwaysPanicRegistry) Add(e Entry) error { return p.delegate.Add(e) }
func (p *alwaysPanicRegistry) Lookup(t [sha256.Size]byte, cn string) (*Entry, error) {
	return p.delegate.Lookup(t, cn)
}
func (p *alwaysPanicRegistry) LookupByContainerID(id string) (*Entry, error) {
	return p.delegate.LookupByContainerID(id)
}
func (p *alwaysPanicRegistry) LookupByThumbprint(t [sha256.Size]byte) (*Entry, error) {
	return p.delegate.LookupByThumbprint(t)
}
func (p *alwaysPanicRegistry) Snapshot() []Entry { return p.delegate.Snapshot() }
func (p *alwaysPanicRegistry) EvictByContainerID(id string) error {
	p.calls.Add(1)
	panic("synthetic storm panic")
}

// TestSubscribe_PanicStormTerminatesAtThreshold proves the panic-time
// ring buffer drives termination after subscribePanicWindowMaxHits
// recoveries within subscribePanicWindow. Pacing knobs are shrunk so
// the test runs in test-time; the array size is const and unaffected.
func TestSubscribe_PanicStormTerminatesAtThreshold(t *testing.T) {
	oldMin, oldMax, oldWindow := subscribePanicBackoffMin, subscribePanicBackoffMax, subscribePanicWindow
	subscribePanicBackoffMin = time.Microsecond
	subscribePanicBackoffMax = time.Microsecond
	subscribePanicWindow = time.Hour
	t.Cleanup(func() {
		subscribePanicBackoffMin = oldMin
		subscribePanicBackoffMax = oldMax
		subscribePanicWindow = oldWindow
	})

	bus := liveBus(t)
	var buf bytes.Buffer
	bufLog := logger.NewWriter(&buf)
	reg := &alwaysPanicRegistry{delegate: NewRegistry(nil)}

	cancel := Subscribe(context.Background(), reg, bus, bufLog)
	t.Cleanup(cancel)

	// Publish exactly subscribePanicWindowMaxHits events — each one
	// triggers a panic. The window-check on the final panic fires
	// the termination log.
	for range subscribePanicWindowMaxHits {
		for !overseer.Publish(bus, dockerevents.ContainerRemoved{ID: "ctr-storm", At: time.Now()}) {
			time.Sleep(time.Microsecond)
		}
	}

	// First wait for the consumer to process the full storm (calls
	// is incremented before each panic). Without this, cancel may
	// arrive while events are still buffered in the subscriber
	// channel, dropping them and never reaching the threshold.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && reg.calls.Load() < int32(subscribePanicWindowMaxHits) {
		time.Sleep(time.Millisecond)
	}
	require.GreaterOrEqual(t, int(reg.calls.Load()), subscribePanicWindowMaxHits, "consumer did not process every storm event before deadline")

	// Now wait for the consumer goroutine to actually exit. cancel()
	// blocks on the consumer's done channel; after it returns the
	// goroutine is gone and buf is owned by the test. Reading buf
	// concurrently with active zerolog writes would race the
	// detector regardless of content stability.
	canceled := make(chan struct{})
	go func() { cancel(); close(canceled) }()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not terminate within deadline after panic storm")
	}

	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	const wantMsg = "agentregistry subscribe consumer: panic rate exceeded ceiling; terminating consumer"
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
	assert.GreaterOrEqual(t, int(reg.calls.Load()), subscribePanicWindowMaxHits, "consumer must have processed at least the threshold panics")
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached within deadline")
		}
		time.Sleep(2 * time.Millisecond)
	}
}
