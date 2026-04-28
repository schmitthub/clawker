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
	"github.com/schmitthub/clawker/internal/controlplane/informer"
	"github.com/schmitthub/clawker/internal/logger"
)

// liveInformer constructs and starts an informer with deterministic
// options, returning a started instance plus a cleanup. Reuses the
// production informer rather than mocking it because the eviction
// contract is "what the informer publishes drives EvictByContainerID"
// — replacing the informer with a mock would replace the very
// integration this test exists to assert.
func liveInformer(t *testing.T) informer.Interface {
	t.Helper()
	inf := informer.New(informer.Options{})
	require.NoError(t, inf.Start(context.Background()))
	t.Cleanup(func() { _ = inf.Close() })
	return inf
}

func TestSubscribe_EvictsOnContainerRemoved(t *testing.T) {
	inf := liveInformer(t)
	r := NewRegistry(nil)
	r.Add(Entry{AgentName: "x", ContainerID: "ctr-evict", Thumbprint: tp("cert"), RegisteredAt: time.Now()})

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

	waitFor(t, func() bool {
		_, err := r.Lookup(tp("cert"), canonical("", "x"))
		return err == ErrUnknownAgent
	})
}

func TestSubscribe_EvictsOnContainerStopped(t *testing.T) {
	inf := liveInformer(t)
	r := NewRegistry(nil)
	r.Add(Entry{AgentName: "y", ContainerID: "ctr-stopped", Thumbprint: tp("cert-y"), RegisteredAt: time.Now()})

	cancel := Subscribe(context.Background(), r, inf, logger.Nop())
	t.Cleanup(cancel)

	now := time.Now()
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind:      dockerevents.KindContainer,
		ID:        "ctr-stopped",
		Lifecycle: "running",
	}, informer.Transition{Source: "test", At: now}))
	// First update + first delta is DeltaAdded (the container appears).
	// Second is DeltaUpdated to "stopped" — the eviction trigger.
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind:      dockerevents.KindContainer,
		ID:        "ctr-stopped",
		Lifecycle: "stopped",
	}, informer.Transition{Source: "test", At: now}))

	waitFor(t, func() bool {
		_, err := r.Lookup(tp("cert-y"), canonical("", "y"))
		return err == ErrUnknownAgent
	})
}

func TestSubscribe_DoesNotEvictOnPaused(t *testing.T) {
	// Paused agent's mTLS connection is intact — the kernel hasn't
	// torn down the socket, the process is just frozen. The registry
	// must NOT evict on paused.
	inf := liveInformer(t)
	r := NewRegistry(nil)
	r.Add(Entry{AgentName: "z", ContainerID: "ctr-paused", Thumbprint: tp("cert-z"), RegisteredAt: time.Now()})

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

	// Poll for a stable window — proof of absence by repeated
	// observation is more deterministic than a single time.Sleep:
	// a sleep too short on a loaded CI runner can pass for the wrong
	// reason (the consumer simply hasn't drained the paused delta
	// yet). We poll every 5ms for 100ms; if the entry ever disappears
	// the eviction happened (test fails). If it survives every
	// observation, the consumer saw the paused delta and correctly
	// skipped eviction.
	const window = 100 * time.Millisecond
	const interval = 5 * time.Millisecond
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		got, err := r.Lookup(tp("cert-z"), canonical("", "z"))
		require.NoError(t, err, "paused must not evict registered entry")
		assert.Equal(t, "z", got.AgentName)
		time.Sleep(interval)
	}
}

func TestSubscribe_CancelStopsConsumer(t *testing.T) {
	inf := liveInformer(t)
	r := NewRegistry(nil)
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
// panics on its first call and then delegates to a real Registry for
// every subsequent call. It exists so TestSubscribe_RecoversFromHookPanic
// can prove (a) a panic in EvictByContainerID does not kill the consumer
// goroutine, (b) the recover is logged, and (c) the next delta still
// reaches a working Registry. We cannot register a panicking variant
// directly on registryImpl without touching registry.go (Agent A's
// lane), so we wrap the Registry interface here.
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
func (p *panicOnceRegistry) EvictByContainerID(id string) {
	p.calls.Add(1)
	if p.panicked.CompareAndSwap(false, true) {
		panic("synthetic eviction-hook panic")
	}
	p.delegate.EvictByContainerID(id)
}

func TestSubscribe_RecoversFromHookPanic(t *testing.T) {
	// A panic in EvictByContainerID must not kill the consumer
	// goroutine — otherwise registered agents' Thumbprint entries
	// would keep authorizing per-agent RPCs after their containers
	// are gone, the very leak this regression guards against.
	inf := liveInformer(t)

	var buf bytes.Buffer
	bufLog := logger.NewWriter(&buf)

	delegate := NewRegistry(nil)
	delegate.Add(Entry{AgentName: "first", ContainerID: "ctr-first", Thumbprint: tp("cert-first"), RegisteredAt: time.Now()})
	delegate.Add(Entry{AgentName: "second", ContainerID: "ctr-second", Thumbprint: tp("cert-second"), RegisteredAt: time.Now()})

	reg := &panicOnceRegistry{delegate: delegate}

	cancel := Subscribe(context.Background(), reg, inf, bufLog)
	t.Cleanup(cancel)

	now := time.Now()
	// First delta — triggers the panic. The Entry must still be in
	// the registry afterward (the panic prevented the eviction) and
	// the consumer must still be alive.
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind: dockerevents.KindContainer, ID: "ctr-first",
	}, informer.Transition{Source: "test", At: now}))
	require.NoError(t, inf.Remove(context.Background(),
		informer.Key{Kind: dockerevents.KindContainer, ID: "ctr-first"},
		informer.Transition{Source: "test", At: now}))

	// Wait for the panic to actually fire before sending the second
	// delta. Otherwise we race the consumer and the second delta can
	// arrive before EvictByContainerID has been entered the first
	// time.
	waitFor(t, func() bool { return reg.panicked.Load() })

	// Second delta — must be processed by the resumed consumer,
	// proving subsequent deltas still drain after a recovered panic.
	require.NoError(t, inf.Upsert(context.Background(), informer.ResourceUpdate{
		Kind: dockerevents.KindContainer, ID: "ctr-second",
	}, informer.Transition{Source: "test", At: now}))
	require.NoError(t, inf.Remove(context.Background(),
		informer.Key{Kind: dockerevents.KindContainer, ID: "ctr-second"},
		informer.Transition{Source: "test", At: now}))

	waitFor(t, func() bool {
		_, err := delegate.Lookup(tp("cert-second"), canonical("", "second"))
		return err == ErrUnknownAgent
	})

	// First entry was never evicted because the panic prevented it
	// — assert it's still present so we know the test exercised the
	// panic path rather than silently succeeding.
	got, err := delegate.Lookup(tp("cert-first"), canonical("", "first"))
	require.NoError(t, err, "first entry must survive the panicked eviction call")
	assert.Equal(t, "first", got.AgentName)

	// Recover must have logged at error level so an operator can
	// notice the dropped delta. Parse the JSON line(s) so we don't
	// brittle-match on prose.
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
