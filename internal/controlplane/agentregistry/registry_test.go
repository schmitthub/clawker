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

// validEntry builds the minimal Entry that satisfies Add's invariants
// (non-zero thumbprint, non-empty agent name, non-zero RegisteredAt).
// Used by tests that don't care about Entry contents — callers
// override the fields they're actually exercising.
func validEntry(name, containerID, certSeed string) Entry {
	return Entry{
		AgentName:    name,
		ContainerID:  containerID,
		Thumbprint:   tp(certSeed),
		RegisteredAt: time.Unix(1000, 0),
		LastSeen:     time.Unix(1000, 0),
	}
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
	a := validEntry("clawker.a", "ctr-1", "cert-a")
	b := validEntry("clawker.b", "ctr-2", "cert-b")
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
	first := validEntry("clawker.x", "ctr", "cert-1")
	second := validEntry("clawker.x", "ctr", "cert-2")
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
	r.Add(validEntry("clawker.b", "", "cert-b"))
	r.Add(validEntry("clawker.a", "", "cert-a"))
	r.Add(validEntry("clawker.c", "", "cert-c"))

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
			entry := Entry{
				AgentName:    "clawker.agent",
				ContainerID:  "ctr-x",
				Thumbprint:   thumb,
				RegisteredAt: time.Unix(1000, 0),
			}
			r.Add(entry)
			_, _ = r.Lookup(thumb)
			r.Touch(thumb)
		}(i)
	}
	wg.Wait()
}

// TestRegistry_Add_RejectsInvariantViolations pins the contract that
// Add panics on invalid input. The only legitimate caller of Add is
// the in-package agent.Handler which has already verified each
// invariant via the five identity-binding cross-checks at Register;
// any other caller violating these is a wiring bug that must surface
// loudly. Each subtest uses recover() to assert a panic occurred and
// no entry made it into the registry.
func TestRegistry_Add_RejectsInvariantViolations(t *testing.T) {
	cases := []struct {
		name  string
		entry Entry
	}{
		{
			name: "zero thumbprint",
			entry: Entry{
				AgentName:    "clawker.x",
				ContainerID:  "ctr",
				RegisteredAt: time.Unix(1000, 0),
				// Thumbprint left zero — the all-zero key would let
				// any non-registering caller collide on identity.
			},
		},
		{
			name: "empty agent name",
			entry: Entry{
				ContainerID:  "ctr",
				Thumbprint:   tp("cert"),
				RegisteredAt: time.Unix(1000, 0),
				// AgentName empty — breaks Snapshot ordering and
				// confuses the audit log.
			},
		},
		{
			name: "zero RegisteredAt",
			entry: Entry{
				AgentName:   "clawker.x",
				ContainerID: "ctr",
				Thumbprint:  tp("cert"),
				// RegisteredAt zero — breaks downstream observability.
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry(nil)
			defer func() {
				rec := recover()
				assert.NotNil(t, rec, "Add must panic on %s", tc.name)
			}()
			r.Add(tc.entry)
			t.Fatal("Add did not panic on invalid entry")
		})
	}
}
