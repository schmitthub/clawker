package agentregistry

import (
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/auth"
)

func tp(s string) [sha256.Size]byte {
	return sha256.Sum256([]byte(s))
}

// canonical is the canonical "clawker.<project>.<agent>" CN for the
// (project, agent) tuple — what the CLI's MintAgentCert puts in the
// peer cert and what Lookup cross-checks against. Defined here so test
// assertions don't drift from the production composer in
// auth.CanonicalAgentCN.
func canonical(project, agent string) string {
	return auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agent))
}

// mustAdd inserts an entry and t.Fatals on persistence error. The
// in-memory Registry never returns an error from Add today; the helper
// keeps test sites symmetric with the sqlite-backed Registry where Add
// can fail under UNIQUE collisions.
func mustAdd(t *testing.T, r Registry, e Entry) {
	t.Helper()
	if err := r.Add(e); err != nil {
		t.Fatalf("Add(%q): %v", e.AgentName, err)
	}
}

// validEntry builds the minimal Entry that satisfies Add's invariants
// (non-zero thumbprint, non-empty agent name, non-empty container_id,
// non-zero RegisteredAt). Used by tests that don't care about Entry
// contents — callers override the fields they're actually exercising.
func validEntry(project, agent, containerID, certSeed string) Entry {
	return Entry{
		AgentName:    agent,
		Project:      project,
		ContainerID:  containerID,
		Thumbprint:   tp(certSeed),
		RegisteredAt: time.Unix(1000, 0),
		LastSeen:     time.Unix(1000, 0),
	}
}

func TestRegistry_AddLookup(t *testing.T) {
	r := NewRegistry(nil)
	now := time.Unix(1000, 0)
	entry := Entry{
		AgentName:    "y",
		Project:      "x",
		ContainerID:  "ctr-x",
		Thumbprint:   tp("cert-x"),
		RegisteredAt: now,
		LastSeen:     now,
	}
	mustAdd(t, r, entry)

	got, err := r.Lookup(entry.Thumbprint, canonical("x", "y"))
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, entry.AgentName, got.AgentName)
	assert.Equal(t, entry.Project, got.Project)
	assert.Equal(t, entry.ContainerID, got.ContainerID)
	assert.Equal(t, entry.Thumbprint, got.Thumbprint)
}

func TestRegistry_Lookup_Unknown(t *testing.T) {
	r := NewRegistry(nil)
	_, err := r.Lookup(tp("nope"), "clawker.x.y")
	assert.ErrorIs(t, err, ErrUnknownAgent)
}

// TestRegistry_Lookup_CNMismatch pins the second half of the composite
// identity check: thumbprint hits an entry but the supplied peer cert
// CN does NOT match the entry's stored canonical (Project, AgentName).
// Must collapse to ErrUnknownAgent — same sentinel as "unknown
// thumbprint" so handler-side error mapping cannot leak which half
// failed.
func TestRegistry_Lookup_CNMismatch(t *testing.T) {
	r := NewRegistry(nil)
	mustAdd(t, r, validEntry("alpha", "dev", "ctr", "cert"))

	// Right thumbprint, wrong CN (different project) — must fail.
	_, err := r.Lookup(tp("cert"), canonical("beta", "dev"))
	assert.ErrorIs(t, err, ErrUnknownAgent, "CN mismatch must be indistinguishable from unknown")

	// Right thumbprint, wrong CN (different agent) — must fail.
	_, err = r.Lookup(tp("cert"), canonical("alpha", "other"))
	assert.ErrorIs(t, err, ErrUnknownAgent)

	// Right thumbprint + right CN — must succeed.
	got, err := r.Lookup(tp("cert"), canonical("alpha", "dev"))
	require.NoError(t, err)
	assert.Equal(t, "dev", got.AgentName)
	assert.Equal(t, "alpha", got.Project)
}

// TestRegistry_Lookup_EmptyProject covers the 2-segment naming case.
// Empty project is a legitimate value (matches docker.ContainerName)
// and the canonical CN drops the project segment.
func TestRegistry_Lookup_EmptyProject(t *testing.T) {
	r := NewRegistry(nil)
	mustAdd(t, r, validEntry("", "solo", "ctr", "cert"))

	got, err := r.Lookup(tp("cert"), "clawker.solo")
	require.NoError(t, err)
	assert.Equal(t, "solo", got.AgentName)
	assert.Equal(t, "", got.Project)

	// 3-segment CN against a 2-segment entry must fail.
	_, err = r.Lookup(tp("cert"), "clawker.something.solo")
	assert.ErrorIs(t, err, ErrUnknownAgent)
}

func TestRegistry_EvictByContainerID(t *testing.T) {
	r := NewRegistry(nil)
	a := validEntry("", "a", "ctr-1", "cert-a")
	b := validEntry("", "b", "ctr-2", "cert-b")
	mustAdd(t, r, a)
	mustAdd(t, r, b)

	r.EvictByContainerID("ctr-1")

	_, err := r.Lookup(a.Thumbprint, canonical("", "a"))
	assert.ErrorIs(t, err, ErrUnknownAgent)

	got, err := r.Lookup(b.Thumbprint, canonical("", "b"))
	require.NoError(t, err)
	assert.Equal(t, "ctr-2", got.ContainerID)
}

func TestRegistry_ReRegisterAfterEvict(t *testing.T) {
	// Container restart yields a new cert + new thumbprint. Old entry
	// remains briefly until the dockerevents subscription evicts it,
	// then the new Add lands cleanly.
	r := NewRegistry(nil)
	first := validEntry("", "x", "ctr", "cert-1")
	second := validEntry("", "x", "ctr", "cert-2")
	mustAdd(t, r, first)
	r.EvictByContainerID("ctr")
	mustAdd(t, r, second)

	_, err := r.Lookup(first.Thumbprint, canonical("", "x"))
	assert.ErrorIs(t, err, ErrUnknownAgent)

	got, err := r.Lookup(second.Thumbprint, canonical("", "x"))
	require.NoError(t, err)
	assert.Equal(t, "x", got.AgentName)
}

func TestRegistry_Snapshot_Sorted(t *testing.T) {
	r := NewRegistry(nil)
	mustAdd(t, r, validEntry("", "b", "ctr-b", "cert-b"))
	mustAdd(t, r, validEntry("", "a", "ctr-a", "cert-a"))
	mustAdd(t, r, validEntry("", "c", "ctr-c", "cert-c"))

	snap := r.Snapshot()
	require.Len(t, snap, 3)
	for i, want := range []string{"a", "b", "c"} {
		assert.Equal(t, want, snap[i].AgentName, "snapshot must be sorted by agent name")
	}
}

// TestRegistry_Snapshot_SortedAcrossProjects pins the (Project,
// AgentName) ordering contract. Same short AgentName ("dev") can be
// registered under different projects — the composite identity is
// (project, agent). Without a Project tie-breaker, Go map iteration
// would leave the inter-project order undefined and ListAgents /
// `clawker controlplane agents` output would jitter between calls.
func TestRegistry_Snapshot_SortedAcrossProjects(t *testing.T) {
	r := NewRegistry(nil)
	// Insert in scrambled order to defeat any incidental sort that
	// happens to match insertion order.
	mustAdd(t, r, validEntry("zproj", "dev", "ctr-zproj-dev", "cert-zproj-dev"))
	mustAdd(t, r, validEntry("aproj", "dev", "ctr-aproj-dev", "cert-aproj-dev"))
	mustAdd(t, r, validEntry("aproj", "bot", "ctr-aproj-bot", "cert-aproj-bot"))
	mustAdd(t, r, validEntry("mproj", "dev", "ctr-mproj-dev", "cert-mproj-dev"))

	snap := r.Snapshot()
	require.Len(t, snap, 4)
	got := make([][2]string, len(snap))
	for i, e := range snap {
		got[i] = [2]string{e.Project, e.AgentName}
	}
	want := [][2]string{
		{"aproj", "bot"},
		{"aproj", "dev"},
		{"mproj", "dev"},
		{"zproj", "dev"},
	}
	assert.Equal(t, want, got, "snapshot must be sorted by (Project, AgentName)")
}

func TestRegistry_Concurrent(t *testing.T) {
	// Race-detector contract: many goroutines adding/looking up/evicting
	// without lock-order bugs. Each goroutine works on its own
	// thumbprint AND its own container_id so eviction outcomes are
	// deterministic and the registry's UNIQUE-on-container_id contract
	// is honored (the in-memory impl tolerates collisions today, but
	// the sqlite-backed impl rejects them).
	r := NewRegistry(nil)
	const n = 64

	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			suffix := string(rune('a'+i%26)) + string(rune('0'+i/26))
			thumb := tp("cert-" + suffix)
			entry := Entry{
				AgentName:    "agent",
				Project:      "p",
				ContainerID:  "ctr-" + suffix,
				Thumbprint:   thumb,
				RegisteredAt: time.Unix(1000, 0),
			}
			_ = r.Add(entry)
			_, _ = r.Lookup(thumb, canonical("p", "agent"))
		}(i)
	}
	wg.Wait()
}

// TestRegistry_Add_RejectsInvariantViolations pins the contract that
// Add panics on invalid input. The only legitimate caller of Add is
// the in-package agent.Handler which has already verified each
// invariant via the five identity-binding cross-checks at Connect;
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
				AgentName:    "x",
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
			name: "empty container_id",
			entry: Entry{
				AgentName:    "x",
				Thumbprint:   tp("cert"),
				RegisteredAt: time.Unix(1000, 0),
				// ContainerID empty — breaks the (thumbprint,
				// container_id) composite key invariant; sqlite would
				// reject the row at insert.
			},
		},
		{
			name: "zero RegisteredAt",
			entry: Entry{
				AgentName:   "x",
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
			_ = r.Add(tc.entry)
			t.Fatal("Add did not panic on invalid entry")
		})
	}
}
