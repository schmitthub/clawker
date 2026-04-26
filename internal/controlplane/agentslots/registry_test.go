package agentslots

import (
	"crypto/sha256"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
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

// mkThumb derives a deterministic SHA-256 thumbprint for a given
// (project, agent) tuple. Used by both mkSlot and the Consume call
// sites so a test can pass the same thumbprint Reserve stored without
// reconstructing it.
func mkThumb(project, agent string) [sha256.Size]byte {
	var thumb [sha256.Size]byte
	copy(thumb[:], sha256.New().Sum([]byte("thumb-"+project+":"+agent)))
	return thumb
}

// mkSlot builds the input shape callers actually pass to Reserve. The
// ReservedAt/ExpiresAt fields are intentionally omitted: Reserve stamps
// them from its own clock and any value supplied here would be
// overwritten.
func mkSlot(project, agent, verifier string) Slot {
	return Slot{
		AgentName:              agent,
		Project:                project,
		ContainerID:            "ctr-" + project + "-" + agent,
		ExpectedCertThumbprint: mkThumb(project, agent),
		Challenge:              pkceChallenge(verifier),
		ChallengeMethod:        consts.ChallengeMethodS256,
	}
}

func TestRegistry_ReserveConsumeHappyPath(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	const verifier = "verifier"
	in := mkSlot("x", "y", verifier)
	require.NoError(t, r.Reserve(in))
	assert.Equal(t, 1, r.Len())

	got, err := r.Consume(mkThumb("x", "y"), "y", "x", verifier)
	require.NoError(t, err)
	require.NotNil(t, got)

	// Returned slot must carry the CLI-asserted attributes the handler
	// will use for cert/IP/label cross-checks at Connect.
	assert.Equal(t, in.ContainerID, got.ContainerID)
	assert.Equal(t, in.ExpectedCertThumbprint, got.ExpectedCertThumbprint)
	assert.Equal(t, "x", got.Project)
	assert.Equal(t, "y", got.AgentName)

	// Reserve must stamp the clock-derived TTL fields, not the zero
	// values that the caller passed in.
	assert.Equal(t, clock.Now(), got.ReservedAt, "Reserve must stamp ReservedAt from its clock")
	assert.Equal(t, clock.Now().Add(consts.AgentSlotTTL), got.ExpiresAt, "Reserve must stamp ExpiresAt = now + AgentSlotTTL")

	// Slot consumed — empty registry.
	assert.Equal(t, 0, r.Len())
}

// TestRegistry_Reserve_IgnoresCallerStamps documents the contract:
// caller-supplied ReservedAt/ExpiresAt are silently overwritten. A
// previous version of this code trusted caller input and would let a
// buggy CLI reserve a slot already past expiry, or with an absurd
// far-future expiry that bypassed the TTL janitor.
func TestRegistry_Reserve_IgnoresCallerStamps(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	const verifier = "verifier"
	in := mkSlot("x", "y", verifier)
	in.ReservedAt = time.Unix(0, 0)    // adversarial: pre-epoch
	in.ExpiresAt = time.Unix(1<<40, 0) // adversarial: far future
	require.NoError(t, r.Reserve(in))

	got, err := r.Consume(mkThumb("x", "y"), "y", "x", verifier)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, clock.Now(), got.ReservedAt, "caller's ReservedAt must be ignored")
	assert.Equal(t, clock.Now().Add(consts.AgentSlotTTL), got.ExpiresAt, "caller's ExpiresAt must be ignored")
}

func TestRegistry_Consume_WrongVerifier_LeavesSlot(t *testing.T) {
	// Mismatched verifier must not consume the slot — that would let a
	// hostile retry burn a legitimate registration. Instead the slot
	// stays for a benign retry; the TTL janitor handles eviction.
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	const verifier = "verifier"
	require.NoError(t, r.Reserve(mkSlot("x", "y", verifier)))

	_, err := r.Consume(mkThumb("x", "y"), "y", "x", "wrong-verifier")
	assert.ErrorIs(t, err, ErrSlotInvalid)
	assert.Equal(t, 1, r.Len(), "wrong verifier must leave the slot intact")

	got, err := r.Consume(mkThumb("x", "y"), "y", "x", verifier)
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
	require.NoError(t, r.Reserve(mkSlot("x", "y", verifier)))

	_, err := r.Consume(mkThumb("x", "y"), "y", "x", verifier)
	require.NoError(t, err)

	_, err = r.Consume(mkThumb("x", "y"), "y", "x", verifier)
	assert.ErrorIs(t, err, ErrSlotInvalid)
}

func TestRegistry_Consume_Expired(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	const verifier = "verifier"
	require.NoError(t, r.Reserve(mkSlot("x", "y", verifier)))

	// Advance well past AgentSlotTTL so the slot is unambiguously expired.
	clock.Advance(2 * consts.AgentSlotTTL)

	_, err := r.Consume(mkThumb("x", "y"), "y", "x", verifier)
	assert.ErrorIs(t, err, ErrSlotInvalid)
	assert.Equal(t, 0, r.Len(), "expired slot must be deleted at consume time")
}

func TestRegistry_Reserve_Duplicate(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	require.NoError(t, r.Reserve(mkSlot("x", "y", "v1")))
	err := r.Reserve(mkSlot("x", "y", "v2"))
	assert.ErrorIs(t, err, ErrSlotExists)
	assert.Equal(t, 1, r.Len())
}

// TestRegistry_Reserve_SameAgentDifferentProjects pins the project-as-
// part-of-the-key invariant: the same short agent name (the user's
// favorite "dev") in two different projects keys two disjoint slots.
// Reserve does not collide; both Consumes succeed independently. This
// is the headline reason Project entered the slot key — without it,
// running two clawker projects with the same agent name would force
// users to rename or clobber the second slot.
func TestRegistry_Reserve_SameAgentDifferentProjects(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	const verifier = "verifier"
	require.NoError(t, r.Reserve(mkSlot("alpha", "dev", verifier)))
	require.NoError(t, r.Reserve(mkSlot("beta", "dev", verifier)),
		"same agent name in a different project must NOT collide")
	assert.Equal(t, 2, r.Len())

	gotA, err := r.Consume(mkThumb("alpha", "dev"), "dev", "alpha", verifier)
	require.NoError(t, err)
	assert.Equal(t, "alpha", gotA.Project)

	gotB, err := r.Consume(mkThumb("beta", "dev"), "dev", "beta", verifier)
	require.NoError(t, err)
	assert.Equal(t, "beta", gotB.Project)
}

// TestRegistry_EvictByContainerID exercises the dockerevents-driven
// eviction path. Multiple slots share a registry; evicting by one
// container_id must drop only the matching slot(s) and leave the rest.
// Mirrors agentregistry's eviction semantics so dockerevents.Subscribe
// can drive both registries identically.
func TestRegistry_EvictByContainerID(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	a := mkSlot("", "a", "verifier-a")
	a.ContainerID = "ctr-a"
	b := mkSlot("", "b", "verifier-b")
	b.ContainerID = "ctr-b"
	require.NoError(t, r.Reserve(a))
	require.NoError(t, r.Reserve(b))
	require.Equal(t, 2, r.Len())

	r.EvictByContainerID("ctr-a")
	assert.Equal(t, 1, r.Len(), "only ctr-a's slot must be evicted")

	// Surviving slot is consumable; evicted slot is not.
	_, err := r.Consume(mkThumb("", "a"), "a", "", "verifier-a")
	assert.ErrorIs(t, err, ErrSlotInvalid, "evicted slot must be unreachable")

	got, err := r.Consume(mkThumb("", "b"), "b", "", "verifier-b")
	require.NoError(t, err)
	assert.Equal(t, "ctr-b", got.ContainerID)
}

// TestRegistry_EvictByContainerID_Unknown is a no-op safety check:
// evicting a container_id with no matching slot must not panic, must
// not affect other slots, and must leave Len unchanged.
func TestRegistry_EvictByContainerID_Unknown(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	require.NoError(t, r.Reserve(mkSlot("", "x", "verifier-x")))
	r.EvictByContainerID("ctr-does-not-exist")
	assert.Equal(t, 1, r.Len())
}

func TestRegistry_Janitor_SweepsExpired(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}

	// Pulse channel makes the janitor deterministic — the test fires
	// one sweep on demand instead of wall-clock polling for the
	// 5ms-ticker test to land. Eliminates the race-detector flake
	// surface where a loaded CI runner misses the ticker by enough
	// jitter to bust the 1-second deadline.
	pulse := make(chan time.Time, 1)
	r := NewRegistryWithPulseChan(clock.Now, nil, pulse)
	t.Cleanup(r.Stop)

	for _, name := range []string{"a", "b"} {
		require.NoError(t, r.Reserve(mkSlot("", name, "verifier-"+name)))
	}
	require.Equal(t, 2, r.Len())

	clock.Advance(2 * consts.AgentSlotTTL)

	// Fire one pulse and wait for the sweep to drain. Len() reaching 0
	// proves the janitor read the pulse, called sweep, and saw both
	// expired slots in a single pass.
	pulse <- time.Now()
	deadline := time.Now().Add(time.Second)
	for r.Len() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("janitor never swept expired slots after pulse: Len()=%d", r.Len())
		}
		time.Sleep(time.Millisecond)
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
			name := "agent-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
			require.NoError(t, r.Reserve(mkSlot("", name, verifier)))
			_, err := r.Consume(mkThumb("", name), name, "", verifier)
			require.NoError(t, err)
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 0, r.Len())
}

// TestRegistry_Consume_RaceWrongVerifier exercises the contract under
// adversarial concurrency: many goroutines hammering Consume with a
// known-wrong verifier against one goroutine attempting the correct
// verifier. Exactly one Consume must succeed (the correct one), every
// wrong call must return ErrSlotInvalid, the slot must be removed
// after the correct call (and only after), and the map must not be
// corrupted (Len reads cleanly). Run under -race to catch any
// lock-order or visibility bug in the wrong-verifier branch (which
// previously left the slot in place but had to do so under the same
// mutex as the correct branch's delete).
func TestRegistry_Consume_RaceWrongVerifier(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	const (
		project   = "race"
		agentName = "target"
		correct   = "correct-verifier"
		wrong     = "wrong-verifier"
		attackers = 64
	)
	require.NoError(t, r.Reserve(mkSlot(project, agentName, correct)))

	var (
		wg            sync.WaitGroup
		wrongFailures atomic.Int64
		correctWins   atomic.Int64
		start         = make(chan struct{})
	)

	wg.Add(attackers)
	for range attackers {
		go func() {
			defer wg.Done()
			<-start
			if _, err := r.Consume(mkThumb(project, agentName), agentName, project, wrong); err == ErrSlotInvalid {
				wrongFailures.Add(1)
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		if _, err := r.Consume(mkThumb(project, agentName), agentName, project, correct); err == nil {
			correctWins.Add(1)
		}
	}()

	close(start)
	wg.Wait()

	assert.Equal(t, int64(attackers), wrongFailures.Load(), "every wrong-verifier consume must fail")
	assert.Equal(t, int64(1), correctWins.Load(), "exactly one correct-verifier consume must win")
	assert.Equal(t, 0, r.Len(), "slot must be removed after the correct consume")

	// Repeat the correct verifier — single-use contract still holds
	// after the race. Catches the regression where a wrong-verifier
	// branch accidentally leaks a delete.
	_, err := r.Consume(mkThumb(project, agentName), agentName, project, correct)
	assert.ErrorIs(t, err, ErrSlotInvalid)
}

func TestRegistry_Reserve_Validation(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := newRegistry(t, clock)

	t.Run("empty agent name", func(t *testing.T) {
		s := mkSlot("p", "", "v")
		require.Error(t, r.Reserve(s))
	})

	t.Run("non-S256 method", func(t *testing.T) {
		s := mkSlot("a", "b", "v")
		s.ChallengeMethod = "plain"
		require.Error(t, r.Reserve(s))
	})

	t.Run("zero method", func(t *testing.T) {
		// Empty/zero-value method must be rejected too — defends
		// against a caller that builds Slot{} and forgets the field.
		s := mkSlot("a", "c", "v")
		s.ChallengeMethod = ""
		require.Error(t, r.Reserve(s))
	})
}

// TestRegistry_Reserve_RejectsInvariantViolations pins the panic
// contract that mirrors agentregistry.Add: the only legitimate caller
// is AdminService.AnnounceAgent, which derives the load-bearing fields
// from the CLI's signed claim. Zero ExpectedCertThumbprint or empty
// Challenge is a wiring bug that must surface loudly — silently
// keying a slot under all-zero bytes would break the "fresh cert per
// retry" composite-collision argument; an empty Challenge would let
// subtle.ConstantTimeCompare("", "") trivially pass against an empty
// verifier. Without these tests a future refactor that drops a
// guard would silently regress identity binding.
func TestRegistry_Reserve_RejectsInvariantViolations(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Slot)
		wantSub string
	}{
		{
			name: "zero thumbprint",
			mutate: func(s *Slot) {
				s.ExpectedCertThumbprint = [sha256.Size]byte{}
			},
			wantSub: "zero ExpectedCertThumbprint",
		},
		{
			name: "empty challenge",
			mutate: func(s *Slot) {
				s.Challenge = ""
			},
			wantSub: "empty Challenge",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &fakeClock{now: time.Unix(100, 0)}
			r := newRegistry(t, clock)
			s := mkSlot("p", "a", "verifier")
			tc.mutate(&s)

			defer func() {
				rec := recover()
				require.NotNil(t, rec, "Reserve must panic on %s", tc.name)
				msg, _ := rec.(string)
				assert.Contains(t, msg, tc.wantSub,
					"panic message must identify the violated invariant")
			}()
			_ = r.Reserve(s)
			t.Fatal("Reserve did not panic on invalid slot")
		})
	}
}

func TestRegistry_Stop_Idempotent(t *testing.T) {
	// Stop is called from multiple shutdown paths (drain-to-zero, test
	// cleanup, /controlplane down) — must not panic on a second call.
	clock := &fakeClock{now: time.Unix(100, 0)}
	r := NewRegistry(clock.Now, 5*time.Millisecond, nil)
	r.Stop()
	r.Stop() // would panic on closing already-closed channel without sync.Once
}
