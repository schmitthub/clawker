package agentslots

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock returns a controllable clock so tests don't sleep.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newRegistry(t *testing.T, clock *fakeClock) Registry {
	t.Helper()
	r := NewRegistry(clock.Now, time.Hour, nil)
	t.Cleanup(r.Stop)
	return r
}

func mkSlot(clock *fakeClock, name, verifier string, ttl time.Duration) Slot {
	now := clock.Now()
	return Slot{
		AgentName:              name,
		ContainerID:            "ctr-" + name,
		ExpectedCertThumbprint: "thumbprint-" + name,
		Challenge:              pkceChallenge(verifier),
		ChallengeMethod:        "S256",
		ReservedAt:             now,
		ExpiresAt:              now.Add(ttl),
	}
}

func TestRegistry_ReserveConsumeHappyPath(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	const verifier = "verifier"
	slot := mkSlot(clock, "clawker.x.y", verifier, time.Minute)
	require.NoError(t, r.Reserve(slot))
	assert.Equal(t, 1, r.Len())

	got, err := r.Consume("clawker.x.y", verifier)
	require.NoError(t, err)
	require.NotNil(t, got)

	// Returned slot must carry the CLI-asserted attributes the handler
	// will use for cert/IP/label cross-checks at Register.
	assert.Equal(t, slot.ContainerID, got.ContainerID)
	assert.Equal(t, slot.ExpectedCertThumbprint, got.ExpectedCertThumbprint)

	// Slot consumed — empty registry.
	assert.Equal(t, 0, r.Len())
}

func TestRegistry_Consume_WrongVerifier_LeavesSlot(t *testing.T) {
	// Mismatched verifier must not consume the slot — that would let a
	// hostile retry burn a legitimate registration. Instead the slot
	// stays for a benign retry; the TTL janitor handles eviction.
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	const verifier = "verifier"
	require.NoError(t, r.Reserve(mkSlot(clock, "clawker.x.y", verifier, time.Minute)))

	_, err := r.Consume("clawker.x.y", "wrong-verifier")
	assert.ErrorIs(t, err, ErrSlotInvalid)
	assert.Equal(t, 1, r.Len(), "wrong verifier must leave the slot intact")

	got, err := r.Consume("clawker.x.y", verifier)
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestRegistry_Consume_Replay(t *testing.T) {
	// Single-use: a successful Consume must reject the same verifier on
	// replay. This is what makes slot consumption the nonce — no
	// separate replay-defense field needed.
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	const verifier = "verifier"
	require.NoError(t, r.Reserve(mkSlot(clock, "clawker.x.y", verifier, time.Minute)))

	_, err := r.Consume("clawker.x.y", verifier)
	require.NoError(t, err)

	_, err = r.Consume("clawker.x.y", verifier)
	assert.ErrorIs(t, err, ErrSlotInvalid)
}

func TestRegistry_Consume_Expired(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	const verifier = "verifier"
	require.NoError(t, r.Reserve(mkSlot(clock, "clawker.x.y", verifier, time.Minute)))

	clock.Advance(2 * time.Minute)

	_, err := r.Consume("clawker.x.y", verifier)
	assert.ErrorIs(t, err, ErrSlotInvalid)
	assert.Equal(t, 0, r.Len(), "expired slot must be deleted at consume time")
}

func TestRegistry_Reserve_Duplicate(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	require.NoError(t, r.Reserve(mkSlot(clock, "clawker.x.y", "v1", time.Minute)))
	err := r.Reserve(mkSlot(clock, "clawker.x.y", "v2", time.Minute))
	assert.ErrorIs(t, err, ErrSlotExists)
	assert.Equal(t, 1, r.Len())
}

func TestRegistry_Janitor_SweepsExpired(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}

	// Short sweep period for fast deterministic test. Janitor uses real
	// time for the ticker but the clock-injected `now()` decides
	// expiry, so we advance the fake clock past TTL and wait one tick.
	r := NewRegistry(clock.Now, 5*time.Millisecond, nil)
	t.Cleanup(r.Stop)

	for _, name := range []string{"clawker.a", "clawker.b"} {
		require.NoError(t, r.Reserve(mkSlot(clock, name, "verifier-"+name, time.Second)))
	}
	require.Equal(t, 2, r.Len())

	clock.Advance(2 * time.Second)

	// Wait for at least one sweep tick + a little slack.
	deadline := time.Now().Add(time.Second)
	for r.Len() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("janitor never swept expired slots: Len()=%d", r.Len())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestRegistry_Concurrent_ReserveConsume(t *testing.T) {
	// Race-detector contract: many goroutines reserving + consuming
	// without lock-order bugs. Each goroutine works on its own
	// agent_name so success counts can be checked deterministically.
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	const goroutines = 32
	const verifier = "verifier"

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			name := "clawker.agent." + string(rune('a'+i%26)) + string(rune('0'+i/26))
			require.NoError(t, r.Reserve(mkSlot(clock, name, verifier, time.Minute)))
			_, err := r.Consume(name, verifier)
			require.NoError(t, err)
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 0, r.Len())
}

func TestRegistry_Reserve_Validation(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	t.Run("empty agent name", func(t *testing.T) {
		s := mkSlot(clock, "", "v", time.Minute)
		require.Error(t, r.Reserve(s))
	})

	t.Run("non-S256 method", func(t *testing.T) {
		s := mkSlot(clock, "clawker.a.b", "v", time.Minute)
		s.ChallengeMethod = "plain"
		require.Error(t, r.Reserve(s))
	})

	t.Run("expired-at-reserve", func(t *testing.T) {
		s := mkSlot(clock, "clawker.a.c", "v", -time.Second)
		require.Error(t, r.Reserve(s))
	})
}

func TestRegistry_Stop_Idempotent(t *testing.T) {
	// Stop is called from multiple shutdown paths (drain-to-zero, test
	// cleanup, /controlplane down) — must not panic on a second call.
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := NewRegistry(clock.Now, 5*time.Millisecond, nil)
	r.Stop()
	r.Stop() // would close(nil) panic without sync.Once
}
