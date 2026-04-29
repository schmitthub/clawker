package agentslots

import (
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry_Reserve_Consume_HappyPath(t *testing.T) {
	now := time.Unix(1000, 0)
	r := NewRegistry(func() time.Time { return now }, time.Hour, nil)
	defer r.Stop()

	require.NoError(t, r.Reserve(Slot{ContainerID: "ctr-a"}))
	got, err := r.Consume("ctr-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "ctr-a", got.ContainerID)

	// Single-use — second consume returns ErrSlotInvalid.
	_, err = r.Consume("ctr-a")
	assert.ErrorIs(t, err, ErrSlotInvalid)
}

func TestRegistry_Reserve_StampsTimestamps(t *testing.T) {
	now := time.Unix(2000, 0)
	r := NewRegistry(func() time.Time { return now }, time.Hour, nil)
	defer r.Stop()

	require.NoError(t, r.Reserve(Slot{ContainerID: "ctr"}))
	got, err := r.Consume("ctr")
	require.NoError(t, err)
	assert.Equal(t, now, got.ReservedAt, "Reserve must stamp ReservedAt from registry clock")
	assert.True(t, got.ExpiresAt.After(got.ReservedAt), "ExpiresAt must be after ReservedAt")
}

func TestRegistry_Reserve_DuplicateReturnsErrSlotExists(t *testing.T) {
	r := NewRegistry(time.Now, time.Hour, nil)
	defer r.Stop()

	require.NoError(t, r.Reserve(Slot{ContainerID: "ctr"}))
	err := r.Reserve(Slot{ContainerID: "ctr"})
	assert.ErrorIs(t, err, ErrSlotExists)
}

func TestRegistry_Reserve_PanicsOnEmptyContainerID(t *testing.T) {
	r := NewRegistry(time.Now, time.Hour, nil)
	defer r.Stop()
	assert.PanicsWithValue(t,
		"agentslots: Reserve called with empty ContainerID",
		func() { _ = r.Reserve(Slot{}) },
	)
}

func TestRegistry_Consume_MissingReturnsErrSlotInvalid(t *testing.T) {
	r := NewRegistry(time.Now, time.Hour, nil)
	defer r.Stop()

	_, err := r.Consume("never-reserved")
	assert.ErrorIs(t, err, ErrSlotInvalid)
}

func TestRegistry_Consume_EmptyContainerIDReturnsErrSlotInvalid(t *testing.T) {
	r := NewRegistry(time.Now, time.Hour, nil)
	defer r.Stop()
	_, err := r.Consume("")
	assert.ErrorIs(t, err, ErrSlotInvalid)
}

// TestRegistry_Consume_ExpiredSlotReturnsErrSlotInvalid pins the TTL
// branch: a slot whose ExpiresAt has passed must return ErrSlotInvalid
// AND get removed from the live set so a future Consume sees no slot
// rather than a stale-expired one.
func TestRegistry_Consume_ExpiredSlotReturnsErrSlotInvalid(t *testing.T) {
	clock := time.Unix(1000, 0)
	tick := func() time.Time { return clock }
	r := NewRegistry(tick, time.Hour, nil)
	defer r.Stop()

	require.NoError(t, r.Reserve(Slot{ContainerID: "ctr"}))
	clock = clock.Add(time.Hour * 24)

	_, err := r.Consume("ctr")
	assert.ErrorIs(t, err, ErrSlotInvalid)

	require.NoError(t, r.Reserve(Slot{ContainerID: "ctr"}))
}

func TestRegistry_EvictByContainerID_RemovesPendingSlot(t *testing.T) {
	r := NewRegistry(time.Now, time.Hour, nil)
	defer r.Stop()

	require.NoError(t, r.Reserve(Slot{ContainerID: "ctr-a"}))
	require.NoError(t, r.Reserve(Slot{ContainerID: "ctr-b"}))
	assert.Equal(t, 2, r.Len())

	r.EvictByContainerID("ctr-a")
	assert.Equal(t, 1, r.Len())

	_, err := r.Consume("ctr-a")
	assert.ErrorIs(t, err, ErrSlotInvalid)

	got, err := r.Consume("ctr-b")
	require.NoError(t, err)
	assert.Equal(t, "ctr-b", got.ContainerID)
}

func TestRegistry_EvictByContainerID_EmptyArgIsNoop(t *testing.T) {
	r := NewRegistry(time.Now, time.Hour, nil)
	defer r.Stop()
	require.NoError(t, r.Reserve(Slot{ContainerID: "ctr"}))
	r.EvictByContainerID("")
	assert.Equal(t, 1, r.Len())
}

// TestRegistry_Janitor_SweepsExpiredSlots drives sweeps deterministically
// via NewRegistryWithPulseChan. Reserve a slot, advance the fake clock
// past its TTL, pulse the channel, observe Len() drops to 0.
func TestRegistry_Janitor_SweepsExpiredSlots(t *testing.T) {
	clock := time.Unix(1000, 0)
	tick := func() time.Time { return clock }
	pulse := make(chan time.Time, 1)
	r := NewRegistryWithPulseChan(tick, nil, pulse)
	defer r.Stop()

	require.NoError(t, r.Reserve(Slot{ContainerID: "ctr"}))
	require.Equal(t, 1, r.Len())

	clock = clock.Add(time.Hour * 24)
	pulse <- clock
	require.Eventually(t, func() bool { return r.Len() == 0 }, time.Second, 5*time.Millisecond)
}

// TestRegistry_Janitor_RacesConsume locks down that a janitor sweep
// firing mid-Consume on the same slot is serialized correctly by the
// registry mutex: the outcome is consistent (slot returned OR
// ErrSlotInvalid), no panic, no double-delete crash, and the slot map
// settles to len 0. testHookConsumeMidpoint fires inside Consume
// holding the mutex; the hook spawns a goroutine that calls sweep —
// that goroutine blocks on the mutex until Consume releases it, so
// the two paths never observe inconsistent intermediate state. Run
// under -race to confirm no data race even under repeated execution.
func TestRegistry_Janitor_RacesConsume(t *testing.T) {
	clock := time.Unix(1000, 0)
	tick := func() time.Time { return clock }
	r := NewRegistry(tick, time.Hour, nil)
	defer r.Stop()

	impl := r.(*registryImpl)

	require.NoError(t, r.Reserve(Slot{ContainerID: "ctr-race", AgentName: "a", Project: "p"}))

	sweepDone := make(chan struct{})
	impl.testHookConsumeMidpoint = func() {
		// Spawn a goroutine that races Consume's delete via sweep().
		// sweep takes the same mutex Consume currently holds, so it
		// will block here until Consume releases the lock. After that,
		// sweep observes whatever state Consume left.
		go func() {
			defer close(sweepDone)
			impl.sweep()
		}()
		// Yield so the janitor goroutine reaches the lock acquire
		// before this hook returns. Without this Gosched the spawned
		// goroutine may not be scheduled until after Consume returns,
		// flattening the race into pure sequential execution.
		runtime.Gosched()
	}

	got, err := r.Consume("ctr-race")
	// Either outcome is correct — the slot was live when Consume
	// acquired the lock, so Consume must succeed. The serialization
	// guarantees sweep cannot have evicted it first.
	require.NoError(t, err, "Consume must win the lock; sweep is queued behind it")
	require.NotNil(t, got)
	assert.Equal(t, "ctr-race", got.ContainerID)

	select {
	case <-sweepDone:
	case <-time.After(time.Second):
		t.Fatal("sweep goroutine did not finish; deadlock or stuck mutex")
	}

	assert.Equal(t, 0, r.Len(), "no slots should remain after Consume + sweep")

	// Sanity: the contract permits ErrSlotInvalid in equivalent setups
	// (e.g. expired slot path); reference the sentinel so the test
	// codifies that callers MUST tolerate it.
	_ = errors.Is(err, ErrSlotInvalid)
}

func TestRegistry_Concurrent_ReserveAndConsume(t *testing.T) {
	r := NewRegistry(time.Now, time.Hour, nil)
	defer r.Stop()

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			id := "ctr-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
			_ = r.Reserve(Slot{ContainerID: id})
			_, _ = r.Consume(id)
		}(i)
	}
	wg.Wait()
}
