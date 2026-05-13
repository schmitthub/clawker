// Package agent owns the CP-side agent surface: the persisted
// identity registry, the AgentService.Register handler, the per-RPC
// IdentityInterceptor, the CP→clawkerd Session dialer, and the
// AgentRegistered/AgentUntrusted event types.
//
// Registry rows record the (mTLS cert thumbprint, container_id,
// project, agent_name) tuple. Reads are issued by:
//
//   - the Register handler (idempotency / replay-protection at row
//     write time, keyed by container_id)
//   - the dialer at Hello time (LookupByContainerID → drives
//     AgentRegistered / AgentUntrusted publication via thumbprint
//     compare against the live peer cert)
//   - AdminService.ListAgents (Snapshot)
//
// Writes are CP-only: the Register handler captures the live mTLS
// peer's thumbprint and writes the row. Eviction: startup orphan-row
// reap (in agent.Start), plus dockerevents container/destroy
// (subscribed in agent.Start). Stop/die/kill do NOT evict because a
// stopped container can be `docker start`-ed back into life.
//
// Identity is channel-bound: the registry key is `(thumbprint,
// container_id)` (both UNIQUE in sqlite). Agent-full-name composition
// from `(project, agent_name)` is no longer persisted — the
// IdentityInterceptor establishes the trust anchor via the
// kernel-attested peer IP and cross-checks the cert SAN against the
// label-derived AgentFullName at the gRPC boundary, so the registry
// holds only the per-row identity tuple.
package agent

import (
	"crypto/sha256"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/logger"
)

// Entry is one registered agent. Created CP-side at Register handler
// entry carrying the ContainerID, AgentName, Project, and the SHA-256
// over the peer cert DER (Thumbprint, captured live from the mTLS
// handshake). LastSeen currently equals RegisteredAt; future per-agent
// RPCs will refresh LastSeen at their own boundary.
type Entry struct {
	// AgentName is the user-typed short name (typed so the registry
	// cannot hold a string that failed auth.NewAgentName validation —
	// e.g. a slug containing a dot, the "clawker." prefix, or chars
	// outside the allowed charset). Constructed upstream by the
	// Register handler via auth.NewAgentName at the wire boundary;
	// reconstructed via auth.NewAgentName during sqlite Snapshot reads
	// (malformed rows are skipped, never panicked on).
	AgentName auth.AgentName
	// Project is the clawker project slug under which the agent
	// registered. The zero value (auth.ProjectSlug{}) is the unscoped
	// 2-segment naming case (matches docker.ContainerName when no
	// project is set). Typed for the same reason as AgentName.
	Project      auth.ProjectSlug
	ContainerID  string
	Thumbprint   [sha256.Size]byte
	RegisteredAt time.Time
	LastSeen     time.Time
}

// ErrUnknownAgent is returned by LookupByContainerID when no entry
// matches. Thumbprint compare against the live peer cert is the
// dialer's job, not the registry's.
var ErrUnknownAgent = errors.New("agentregistry: unknown agent")

// Registry is the consumer-facing contract.
//
//go:generate moq -rm -pkg agent -out registry_mock_test.go . Registry
type Registry interface {
	// Add inserts an entry keyed by (Entry.Thumbprint, Entry.ContainerID).
	// Container restart produces a new cert and a new thumbprint, so
	// re-registration creates a new entry; the dockerevents subscription
	// is responsible for evicting the stale one by container ID.
	//
	// Returns an error when the persistence layer (sqlite) rejects the
	// write — disk full, schema corruption, UNIQUE collision against a
	// stale row that hasn't been evicted yet. Callers translate the
	// error into the appropriate gRPC status.
	//
	// Add panics on programming-error invariants (zero thumbprint,
	// empty ContainerID, zero RegisteredAt). Identity-string validity
	// (project slug / agent name format) is enforced upstream by the
	// Register handler at the wire boundary.
	Add(entry Entry) error
	// LookupByContainerID returns the entry whose ContainerID matches.
	// Used by the Register handler (idempotency / replay-protection)
	// and by the dialer at Hello time to drive registry classification
	// (Match / Miss / ThumbprintMismatch) and decide whether to send
	// RegisterRequired or publish AgentUntrusted. The dialer performs
	// the thumbprint comparison against the returned entry itself;
	// this read intentionally does not gate on thumbprint so a
	// mismatch surfaces as a typed local outcome rather than as
	// ErrUnknownAgent.
	//
	// Returns (nil, ErrUnknownAgent) when no entry matches.
	LookupByContainerID(containerID string) (*Entry, error)
	// EvictByContainerID removes any entry whose ContainerID matches.
	// Returns the underlying persistence error so callers can decide
	// whether to retry, log, or abort. The dockerevents-driven and
	// reaper-driven callers log-and-proceed because a transient sqlite
	// failure must not stall the eviction pipeline; CLI-side `clawker
	// container remove` similarly logs at debug since registry hiccups
	// must not surface as remove failures (the row gets pruned later
	// by the dockerevents subscription).
	EvictByContainerID(containerID string) error
	// Snapshot returns a copy of every live entry, sorted by
	// (Project, AgentName) for deterministic output. Project is the
	// primary sort key because the same short AgentName can be reused
	// across different projects (the composite identity is
	// (project, agent)). Used by AdminService.ListAgents and the
	// `clawker controlplane agents` CLI; both rely on stable ordering
	// for diffability.
	Snapshot() []Entry
}

type registryImpl struct {
	mu      sync.RWMutex
	entries map[[sha256.Size]byte]Entry
	log     *logger.Logger
}

// NewRegistry constructs an empty in-memory registry. Logger is required
// (use logger.Nop() in tests) so audit-trail messages on Add and Evict
// are captured even when production logging is otherwise disabled.
func NewRegistry(log *logger.Logger) Registry {
	if log == nil {
		log = logger.Nop()
	}
	return &registryImpl{
		entries: make(map[[sha256.Size]byte]Entry),
		log:     log,
	}
}

func (r *registryImpl) Add(entry Entry) error {
	validateEntry(entry)
	if entry.LastSeen.IsZero() {
		entry.LastSeen = entry.RegisteredAt
	}
	r.mu.Lock()
	r.entries[entry.Thumbprint] = entry
	r.mu.Unlock()
	r.log.Info().
		Str("agent", entry.AgentName.String()).
		Str("project", entry.Project.String()).
		Str("container_id", entry.ContainerID).
		Msg("agentregistry: agent registered")
	return nil
}

// validateEntry runs the programming-error invariants shared by every
// Registry.Add path. Failure is a wiring bug — panic so it surfaces
// during development rather than corrupting the registry. AgentName
// and Project string-validity is enforced by the typed Entry fields
// (auth.AgentName / auth.ProjectSlug). The checks below cover the
// invariants the type system cannot express: non-zero thumbprint,
// non-empty ContainerID, non-zero RegisteredAt, and a non-zero
// AgentName (a struct-literal Entry{} could otherwise omit it and
// land an empty agent slot in the registry).
//
// FIXME(cp-serve-path): These panics are on the gRPC handler goroutine
// reachable post-SetReady from Register. Per root CLAUDE.md, a CP
// panic strands eBPF — a future cleanup should convert to error
// returns. Today the production caller (register_handler.go::Register)
// constructs Entry from middleware-resolved typed values that cannot
// trip any of these gates, but the safety belongs in the API, not in
// the call-site discipline.
func validateEntry(entry Entry) {
	if entry.Thumbprint == ([sha256.Size]byte{}) {
		panic("agentregistry: Add called with zero thumbprint")
	}
	if entry.ContainerID == "" {
		panic("agentregistry: Add called with empty ContainerID")
	}
	if entry.AgentName.IsZero() {
		panic("agentregistry: Add called with zero AgentName")
	}
	if entry.RegisteredAt.IsZero() {
		panic("agentregistry: Add called with zero RegisteredAt")
	}
}

func (r *registryImpl) LookupByContainerID(containerID string) (*Entry, error) {
	if containerID == "" {
		return nil, ErrUnknownAgent
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.entries {
		if e.ContainerID == containerID {
			out := e
			return &out, nil
		}
	}
	return nil, ErrUnknownAgent
}

func (r *registryImpl) EvictByContainerID(containerID string) error {
	r.mu.Lock()
	var evicted []Entry
	for tp, e := range r.entries {
		if e.ContainerID == containerID {
			delete(r.entries, tp)
			evicted = append(evicted, e)
		}
	}
	r.mu.Unlock()
	for _, entry := range evicted {
		r.log.Info().
			Str("agent", entry.AgentName.String()).
			Str("container_id", entry.ContainerID).
			Msg("agentregistry: agent evicted")
	}
	return nil
}

func (r *registryImpl) Snapshot() []Entry {
	r.mu.RLock()
	out := make([]Entry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e)
	}
	r.mu.RUnlock()
	// Sort by (Project, AgentName) — the composite identity. Two
	// projects can register the same short AgentName, so AgentName
	// alone is not a unique key and sorting by it leaves the
	// inter-project order undefined (Go map iteration order).
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project.String() != out[j].Project.String() {
			return out[i].Project.String() < out[j].Project.String()
		}
		return out[i].AgentName.String() < out[j].AgentName.String()
	})
	return out
}
