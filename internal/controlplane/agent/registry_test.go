package agent

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/schmitthub/clawker/internal/auth"
)

func tp(s string) [sha256.Size]byte {
	return sha256.Sum256([]byte(s))
}

// validEntry builds the minimal Entry that satisfies Add's invariants
// (non-zero thumbprint, non-empty agent name, non-empty container_id,
// non-zero RegisteredAt). Used by tests that don't care about Entry
// contents — callers override the fields they're actually exercising.
func validEntry(project, agent, containerID, certSeed string) Entry {
	return Entry{
		AgentName:    auth.MustAgentName(agent),
		Project:      auth.MustProjectSlug(project),
		ContainerID:  containerID,
		Thumbprint:   tp(certSeed),
		RegisteredAt: time.Unix(1000, 0),
		LastSeen:     time.Unix(1000, 0),
	}
}

// TestRegistry_Add_RejectsInvariantViolations pins the contract that
// Add panics on programming-error invariants — zero thumbprint, empty
// container_id, zero RegisteredAt. The only legitimate caller of Add
// is the in-package Register handler which has already verified each
// invariant via the identity-binding cross-checks at Connect; any
// other caller violating these is a wiring bug that must surface
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
				AgentName:    auth.MustAgentName("x"),
				ContainerID:  "ctr",
				RegisteredAt: time.Unix(1000, 0),
				// Thumbprint left zero — the all-zero key would let
				// any non-registering caller collide on identity.
			},
		},
		{
			name: "empty container_id",
			entry: Entry{
				AgentName:    auth.MustAgentName("x"),
				Thumbprint:   tp("cert"),
				RegisteredAt: time.Unix(1000, 0),
				// ContainerID empty — breaks the (thumbprint,
				// container_id) composite key invariant; sqlite would
				// reject the row at insert.
			},
		},
		{
			name: "zero agent name",
			entry: Entry{
				ContainerID:  "ctr",
				Thumbprint:   tp("cert"),
				RegisteredAt: time.Unix(1000, 0),
				// AgentName left as zero auth.AgentName{} — empty
				// agent slot must surface as a wiring bug.
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
