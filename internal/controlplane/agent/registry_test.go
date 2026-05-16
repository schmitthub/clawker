package agent

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
// Add returns an error on programming-error invariants — zero
// thumbprint, empty container_id, zero RegisteredAt. The legitimate
// caller (the in-package Register handler) has already verified each
// invariant via the identity-binding cross-checks; any other caller
// violating these gets a typed reject rather than a panic, because Add
// runs on the gRPC handler goroutine and a panic on the CP serve path
// strands eBPF programs (see root CLAUDE.md).
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
		{
			name: "zero RegisteredAt",
			entry: Entry{
				AgentName:   auth.MustAgentName("x"),
				ContainerID: "ctr",
				Thumbprint:  tp("cert"),
				// RegisteredAt left zero — Snapshot ordering and
				// LastSeen derivation both rely on a real timestamp.
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry(nil)
			err := r.Add(tc.entry)
			require.Error(t, err, "Add must reject %s", tc.name)
			snap, snapErr := r.Snapshot()
			require.NoError(t, snapErr)
			assert.Empty(t, snap, "rejected entry must not land in the registry")
		})
	}
}
