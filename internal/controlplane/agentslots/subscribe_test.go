package agentslots

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
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

func newSubscribeRegistry(t *testing.T) Registry {
	t.Helper()
	r := NewRegistry(time.Now, time.Hour, logger.Nop())
	t.Cleanup(r.Stop)
	return r
}

func TestSubscribe_EvictsOnContainerRemoved(t *testing.T) {
	inf := liveInformer(t)
	r := newSubscribeRegistry(t)

	require.NoError(t, r.Reserve(Slot{ContainerID: "ctr-evict"}))

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
	r := newSubscribeRegistry(t)

	require.NoError(t, r.Reserve(Slot{ContainerID: "ctr-stopped"}))

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

// TestSubscribe_DoesNotEvictOnPaused — paused container still exists
// and may yet be dialed. Slot must survive.
func TestSubscribe_DoesNotEvictOnPaused(t *testing.T) {
	inf := liveInformer(t)
	r := newSubscribeRegistry(t)

	require.NoError(t, r.Reserve(Slot{ContainerID: "ctr-paused"}))

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

	const window = 100 * time.Millisecond
	const interval = 5 * time.Millisecond
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		assert.Equal(t, 1, r.Len(), "paused must not evict pending slot")
		time.Sleep(interval)
	}
}

// TestSubscribe_RaceWithConcurrentReserve exercises the dockerevents
// die/remove ↔ AnnounceAgent race: while the dockerevents consumer
// processes eviction deltas for one set of containers, another goroutine
// concurrently reserves + consumes slots for a disjoint set. The race
// detector + correctness invariant (only the matching ContainerID is
// evicted) must both hold.
func TestSubscribe_RaceWithConcurrentReserve(t *testing.T) {
	inf := liveInformer(t)
	r := newSubscribeRegistry(t)

	cancel := Subscribe(context.Background(), r, inf, logger.Nop())
	t.Cleanup(cancel)

	const iterations = 64
	now := time.Now()

	var resWG, evictWG sync.WaitGroup
	start := make(chan struct{})

	resWG.Add(iterations)
	for i := range iterations {
		go func(i int) {
			defer resWG.Done()
			<-start
			id := "race-keep-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
			if err := r.Reserve(Slot{ContainerID: id}); err != nil {
				t.Errorf("Reserve %q: %v", id, err)
				return
			}
			if _, err := r.Consume(id); err != nil {
				t.Errorf("Consume %q: %v", id, err)
			}
		}(i)
	}

	evictWG.Add(iterations)
	for i := range iterations {
		go func(i int) {
			defer evictWG.Done()
			<-start
			ctrID := "race-evict-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
			_ = inf.Upsert(context.Background(), informer.ResourceUpdate{
				Kind: dockerevents.KindContainer, ID: ctrID,
			}, informer.Transition{Source: "test", At: now})
			_ = inf.Remove(context.Background(),
				informer.Key{Kind: dockerevents.KindContainer, ID: ctrID},
				informer.Transition{Source: "test", At: now})
		}(i)
	}

	close(start)
	resWG.Wait()
	evictWG.Wait()

	assert.Equal(t, 0, r.Len())
}

func TestSubscribe_CancelStopsConsumer(t *testing.T) {
	inf := liveInformer(t)
	r := newSubscribeRegistry(t)

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

// panicOnceRegistry's EvictByContainerID panics on first call then
// delegates. Proves a panic in the eviction hook does not kill the
// consumer goroutine.
type panicOnceRegistry struct {
	calls    atomic.Int32
	panicked atomic.Bool
	delegate Registry
}

func (p *panicOnceRegistry) Reserve(s Slot) error { return p.delegate.Reserve(s) }
func (p *panicOnceRegistry) Consume(id string) (*Slot, error) {
	return p.delegate.Consume(id)
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

	delegate := NewRegistry(time.Now, time.Hour, logger.Nop())
	t.Cleanup(delegate.Stop)

	require.NoError(t, delegate.Reserve(Slot{ContainerID: "ctr-first"}))
	require.NoError(t, delegate.Reserve(Slot{ContainerID: "ctr-second"}))

	reg := &panicOnceRegistry{delegate: delegate}

	cancel := Subscribe(context.Background(), reg, inf, bufLog)
	t.Cleanup(cancel)

	now := time.Now()
	// First delta — triggers panic. Slot must survive (eviction was
	// interrupted) and the consumer must still be alive.
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind: dockerevents.KindContainer, ID: "ctr-first",
	}, informer.Transition{Source: "test", At: now}))
	require.NoError(t, inf.Remove(context.Background(),
		informer.Key{Kind: dockerevents.KindContainer, ID: "ctr-first"},
		informer.Transition{Source: "test", At: now}))

	waitFor(t, func() bool { return reg.panicked.Load() })

	// Second delta — must be processed by the resumed consumer.
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind: dockerevents.KindContainer, ID: "ctr-second",
	}, informer.Transition{Source: "test", At: now}))
	require.NoError(t, inf.Remove(context.Background(),
		informer.Key{Kind: dockerevents.KindContainer, ID: "ctr-second"},
		informer.Transition{Source: "test", At: now}))

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
