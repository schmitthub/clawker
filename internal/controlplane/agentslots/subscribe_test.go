package agentslots

import (
	"bytes"
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/informer"
	"github.com/schmitthub/clawker/internal/logger"
)

// liveInformer constructs and starts an informer with deterministic
// options, returning a started instance plus a cleanup. Reuses the
// production informer rather than mocking it because the eviction
// contract is "what the informer publishes drives EvictByContainerID"
// — replacing the informer with a mock would replace the very
// integration this test exists to assert. Mirrors the helper in
// agentregistry/subscribe_test.go.
func liveInformer(t *testing.T) informer.Interface {
	t.Helper()
	inf := informer.New(informer.Options{})
	require.NoError(t, inf.Start(context.Background()))
	t.Cleanup(func() { _ = inf.Close() })
	return inf
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

func TestSubscribe_EvictsOnContainerRemoved(t *testing.T) {
	inf := liveInformer(t)
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	slot := mkSlot("", "x", "verifier-x")
	slot.ContainerID = "ctr-evict"
	require.NoError(t, r.Reserve(slot))

	cancel := Subscribe(context.Background(), r, inf, logger.Nop())
	t.Cleanup(cancel)

	now := time.Now()
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind: dockerevents.KindContainer,
		ID:   "ctr-evict",
	}, informer.Transition{Source: "test", At: now}))
	require.NoError(t, inf.Remove(context.Background(),
		informer.Key{Kind: dockerevents.KindContainer, ID: "ctr-evict"},
		informer.Transition{Source: "test", At: now}))

	waitFor(t, func() bool { return r.Len() == 0 })
}

func TestSubscribe_EvictsOnContainerStopped(t *testing.T) {
	inf := liveInformer(t)
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	slot := mkSlot("", "y", "verifier-y")
	slot.ContainerID = "ctr-stopped"
	require.NoError(t, r.Reserve(slot))

	cancel := Subscribe(context.Background(), r, inf, logger.Nop())
	t.Cleanup(cancel)

	now := time.Now()
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind:      dockerevents.KindContainer,
		ID:        "ctr-stopped",
		Lifecycle: "running",
	}, informer.Transition{Source: "test", At: now}))
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind:      dockerevents.KindContainer,
		ID:        "ctr-stopped",
		Lifecycle: "stopped",
	}, informer.Transition{Source: "test", At: now}))

	waitFor(t, func() bool { return r.Len() == 0 })
}

// TestSubscribe_DoesNotEvictOnPaused — paused agent's announcing
// container still exists and may yet call Connect. Slot must survive.
func TestSubscribe_DoesNotEvictOnPaused(t *testing.T) {
	inf := liveInformer(t)
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	slot := mkSlot("", "z", "verifier-z")
	slot.ContainerID = "ctr-paused"
	require.NoError(t, r.Reserve(slot))

	cancel := Subscribe(context.Background(), r, inf, logger.Nop())
	t.Cleanup(cancel)

	now := time.Now()
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind:      dockerevents.KindContainer,
		ID:        "ctr-paused",
		Lifecycle: "running",
	}, informer.Transition{Source: "test", At: now}))
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind:      dockerevents.KindContainer,
		ID:        "ctr-paused",
		Lifecycle: "paused",
	}, informer.Transition{Source: "test", At: now}))

	// Give the consumer goroutine time to process the delta — if it
	// was going to evict, it would have by now.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, r.Len(), "paused must not evict pending slot")
}

func TestSubscribe_CancelStopsConsumer(t *testing.T) {
	inf := liveInformer(t)
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	cancel := Subscribe(context.Background(), r, inf, logger.Nop())

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
// panics on its first call and then delegates to the underlying
// Registry for every subsequent call. Mirrors the agentregistry test
// helper — proves a panic in the eviction hook does not kill the
// consumer goroutine.
type panicOnceRegistry struct {
	calls    atomic.Int32
	panicked atomic.Bool
	delegate Registry
}

func (p *panicOnceRegistry) Reserve(s Slot) error { return p.delegate.Reserve(s) }
func (p *panicOnceRegistry) Consume(thumbprint [32]byte, agentName, project, verifier string) (*Slot, error) {
	return p.delegate.Consume(thumbprint, agentName, project, verifier)
}
func (p *panicOnceRegistry) Len() int { return p.delegate.Len() }
func (p *panicOnceRegistry) Stop()    { p.delegate.Stop() }
func (p *panicOnceRegistry) EvictByContainerID(id string) {
	p.calls.Add(1)
	if p.panicked.CompareAndSwap(false, true) {
		panic("synthetic eviction-hook panic")
	}
	p.delegate.EvictByContainerID(id)
}

func TestSubscribe_RecoversFromHookPanic(t *testing.T) {
	inf := liveInformer(t)

	var buf bytes.Buffer
	bufLog := logger.NewWriter(&buf)

	clock := &fakeClock{now: time.Unix(100, 0)}
	delegate := newRegistry(t, clock)

	first := mkSlot("", "first", "verifier-first")
	first.ContainerID = "ctr-first"
	require.NoError(t, delegate.Reserve(first))
	second := mkSlot("", "second", "verifier-second")
	second.ContainerID = "ctr-second"
	require.NoError(t, delegate.Reserve(second))

	reg := &panicOnceRegistry{delegate: delegate}

	cancel := Subscribe(context.Background(), reg, inf, bufLog)
	t.Cleanup(cancel)

	now := time.Now()
	// First delta — triggers the panic. The slot must still be in the
	// registry afterward (the panic prevented the eviction) and the
	// consumer must still be alive.
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind: dockerevents.KindContainer, ID: "ctr-first",
	}, informer.Transition{Source: "test", At: now}))
	require.NoError(t, inf.Remove(context.Background(),
		informer.Key{Kind: dockerevents.KindContainer, ID: "ctr-first"},
		informer.Transition{Source: "test", At: now}))

	// Wait for the panic before sending the second delta, otherwise we
	// race the consumer.
	waitFor(t, func() bool { return reg.panicked.Load() })

	// Second delta — must be processed by the resumed consumer.
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind: dockerevents.KindContainer, ID: "ctr-second",
	}, informer.Transition{Source: "test", At: now}))
	require.NoError(t, inf.Remove(context.Background(),
		informer.Key{Kind: dockerevents.KindContainer, ID: "ctr-second"},
		informer.Transition{Source: "test", At: now}))

	// First slot survived the panicked eviction; second was evicted by
	// the resumed consumer.
	waitFor(t, func() bool { return delegate.Len() == 1 })

	// Recover must have logged at error level so an operator can
	// notice the dropped delta. Parse JSON so we don't brittle-match
	// on prose.
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
