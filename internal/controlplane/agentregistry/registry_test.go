package agentregistry

import (
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tp(s string) [sha256.Size]byte {
	return sha256.Sum256([]byte(s))
}

func TestRegistry_AddLookupTouch(t *testing.T) {
	r := NewRegistry(nil)
	now := time.Unix(1000, 0)
	entry := Entry{
		AgentName:    "clawker.x.y",
		ContainerID:  "ctr-x",
		Thumbprint:   tp("cert-x"),
		RegisteredAt: now,
		LastSeen:     now,
	}
	r.Add(entry)

	got, err := r.Lookup(entry.Thumbprint)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, entry.AgentName, got.AgentName)
	assert.Equal(t, entry.ContainerID, got.ContainerID)
	assert.Equal(t, entry.Thumbprint, got.Thumbprint)

	// Touch must monotonically advance LastSeen.
	preTouch := got.LastSeen
	time.Sleep(time.Millisecond)
	r.Touch(entry.Thumbprint)
	got2, err := r.Lookup(entry.Thumbprint)
	require.NoError(t, err)
	assert.True(t, got2.LastSeen.After(preTouch), "Touch must advance LastSeen")
}

func TestRegistry_Lookup_Unknown(t *testing.T) {
	r := NewRegistry(nil)
	_, err := r.Lookup(tp("nope"))
	assert.ErrorIs(t, err, ErrUnknownAgent)
}

func TestRegistry_EvictByContainerID(t *testing.T) {
	r := NewRegistry(nil)
	a := Entry{AgentName: "clawker.a", ContainerID: "ctr-1", Thumbprint: tp("cert-a")}
	b := Entry{AgentName: "clawker.b", ContainerID: "ctr-2", Thumbprint: tp("cert-b")}
	r.Add(a)
	r.Add(b)

	r.EvictByContainerID("ctr-1")

	_, err := r.Lookup(a.Thumbprint)
	assert.ErrorIs(t, err, ErrUnknownAgent)

	got, err := r.Lookup(b.Thumbprint)
	require.NoError(t, err)
	assert.Equal(t, "ctr-2", got.ContainerID)
}

func TestRegistry_ReRegisterAfterEvict(t *testing.T) {
	// Container restart yields a new cert + new thumbprint. Old entry
	// remains briefly until the dockerevents subscription evicts it,
	// then the new Add lands cleanly.
	r := NewRegistry(nil)
	first := Entry{AgentName: "clawker.x", ContainerID: "ctr", Thumbprint: tp("cert-1")}
	second := Entry{AgentName: "clawker.x", ContainerID: "ctr", Thumbprint: tp("cert-2")}
	r.Add(first)
	r.EvictByContainerID("ctr")
	r.Add(second)

	_, err := r.Lookup(first.Thumbprint)
	assert.ErrorIs(t, err, ErrUnknownAgent)

	got, err := r.Lookup(second.Thumbprint)
	require.NoError(t, err)
	assert.Equal(t, "clawker.x", got.AgentName)
}

func TestRegistry_Snapshot_Sorted(t *testing.T) {
	r := NewRegistry(nil)
	r.Add(Entry{AgentName: "clawker.b", Thumbprint: tp("cert-b")})
	r.Add(Entry{AgentName: "clawker.a", Thumbprint: tp("cert-a")})
	r.Add(Entry{AgentName: "clawker.c", Thumbprint: tp("cert-c")})

	snap := r.Snapshot()
	require.Len(t, snap, 3)
	for i, want := range []string{"clawker.a", "clawker.b", "clawker.c"} {
		assert.Equal(t, want, snap[i].AgentName, "snapshot must be sorted by agent name")
	}
}

func TestRegistry_Concurrent(t *testing.T) {
	// Race-detector contract: many goroutines adding/looking up/evicting
	// without lock-order bugs. Each goroutine works on its own
	// thumbprint so eviction outcomes are deterministic.
	r := NewRegistry(nil)
	const n = 64

	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			thumb := tp("cert-" + string(rune('a'+i%26)) + string(rune('0'+i/26)))
			r.Add(Entry{AgentName: "clawker.agent", ContainerID: "ctr-x", Thumbprint: thumb})
			_, _ = r.Lookup(thumb)
			r.Touch(thumb)
		}(i)
	}
	wg.Wait()
}
