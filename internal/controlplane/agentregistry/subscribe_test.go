package agentregistry

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/informer"
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
	r.Add(Entry{AgentName: "clawker.x", ContainerID: "ctr-evict", Thumbprint: tp("cert")})

	cancel := Subscribe(context.Background(), r, inf)
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
		_, err := r.Lookup(tp("cert"))
		return err == ErrUnknownAgent
	})
}

func TestSubscribe_EvictsOnContainerStopped(t *testing.T) {
	inf := liveInformer(t)
	r := NewRegistry(nil)
	r.Add(Entry{AgentName: "clawker.y", ContainerID: "ctr-stopped", Thumbprint: tp("cert-y")})

	cancel := Subscribe(context.Background(), r, inf)
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
		_, err := r.Lookup(tp("cert-y"))
		return err == ErrUnknownAgent
	})
}

func TestSubscribe_DoesNotEvictOnPaused(t *testing.T) {
	// Paused agent's mTLS connection is intact — the kernel hasn't
	// torn down the socket, the process is just frozen. The registry
	// must NOT evict on paused.
	inf := liveInformer(t)
	r := NewRegistry(nil)
	r.Add(Entry{AgentName: "clawker.z", ContainerID: "ctr-paused", Thumbprint: tp("cert-z")})

	cancel := Subscribe(context.Background(), r, inf)
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

	got, err := r.Lookup(tp("cert-z"))
	require.NoError(t, err)
	assert.Equal(t, "clawker.z", got.AgentName)
}

func TestSubscribe_CancelStopsConsumer(t *testing.T) {
	inf := liveInformer(t)
	r := NewRegistry(nil)
	cancel := Subscribe(context.Background(), r, inf)

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
