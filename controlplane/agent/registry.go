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
	// AgentName is the user-typed short name. Typed (auth.AgentName)
	// purely for compile-time discipline so the registry can't hold a
	// raw string where an AgentName is expected. auth.NewAgentName
	// rejects only the empty case today; charset/length/form
	// constraints are enforced downstream (Docker container/volume
	// create, x509 URI SAN encoding, IdentityInterceptor's symmetric
	// SAN-vs-label compare). User-typed input is normalized upstream
	// by cmdutil.ProjectSlugify before it crosses into auth.
	// Constructed by the Register handler at the wire boundary via
	// auth.NewAgentName; re-validated via auth.NewAgentName during
	// sqlite Snapshot reads — rows with an empty agent_name column
	// are skipped, never panicked on.
	AgentName auth.AgentName
	// Project is the clawker project slug under which the agent
	// registered. The zero value (auth.ProjectSlug{}) signals a
	// global-scope agent (no project namespace), matching the
	// 2-segment docker.ContainerName shape. Typed for the same reason
	// as AgentName; auth.NewProjectSlug accepts any input including
	// empty.
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

// ErrMalformedEntry is returned by sqlite reads when a row's
// agent_name / project / thumbprint fails re-validation (a value that
// landed pre-typed-boundary, a hand-edited DB, or a corrupted column).
// The Register handler treats this sentinel as "evict + re-write":
// the row's identity is unusable so it gets purged and replaced by
// the typed identity the middleware just resolved off the live peer
// IP + cert. Wrapped via fmt.Errorf so errors.Is on the wrapped error
// works for callers that don't unwrap themselves.
var ErrMalformedEntry = errors.New("agentregistry: malformed registry row")

// Registry is the consumer-facing contract.
//
//go:generate moq -rm -pkg mocks -out mocks/registry_mock.go . Registry
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
	// Add returns an error on programming-error invariants (zero
	// thumbprint, empty ContainerID, zero AgentName, zero
	// RegisteredAt). Both Registry implementations (in-memory and
	// sqlite) propagate the error from `validateEntry` rather than
	// panicking — Add lives on a gRPC handler goroutine reachable
	// post-SetReady from Register, and a panic on that path would
	// strand eBPF programs with no supervisor (see root CLAUDE.md).
	// Register maps the error to codes.Internal: every field
	// validateEntry checks is server-derived (thumbprint from the
	// live peer cert, ContainerID/AgentName/Project from
	// IdentityInterceptor's ResolvedContainer, RegisteredAt from
	// h.clock()), so a failure here is a CP wiring bug, not bad
	// client input. User-controlled identity strings are validated
	// upstream at the wire boundary (auth.NewProjectSlug /
	// auth.NewAgentName → codes.InvalidArgument).
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
	//
	// Returns a non-nil error on persistence failure (sqlite db.Query
	// or rows.Err non-nil). Callers must NOT treat an empty result as
	// authoritative when err != nil — reapOrphans would otherwise
	// evict every registered agent on a transient query failure;
	// ListAgents would surface "no agents" to operators while the
	// registry is intact but unreadable.
	Snapshot() ([]Entry, error)
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
	if err := validateEntry(entry); err != nil {
		return err
	}
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
// Registry.Add path. AgentName and Project string-validity is enforced
// by the typed Entry fields (auth.AgentName / auth.ProjectSlug). The
// checks below cover the invariants the type system cannot express:
// non-zero thumbprint, non-empty ContainerID, non-zero RegisteredAt,
// and a non-zero AgentName (a struct-literal Entry{} could otherwise
// omit it and land an empty agent slot in the registry).
//
// Returns an error rather than panicking — Add lives on the gRPC
// handler goroutine reachable post-SetReady from Register, and a
// panic on that path would strand eBPF programs with no supervisor
// (see root CLAUDE.md). Both Add implementations propagate the
// error; Register maps it to codes.Internal because every field
// checked here is server-derived (see Add's doc comment above).
func validateEntry(entry Entry) error {
	if entry.Thumbprint == ([sha256.Size]byte{}) {
		return errors.New("agentregistry: Add called with zero thumbprint")
	}
	if entry.ContainerID == "" {
		return errors.New("agentregistry: Add called with empty ContainerID")
	}
	if entry.AgentName.IsZero() {
		return errors.New("agentregistry: Add called with zero AgentName")
	}
	if entry.RegisteredAt.IsZero() {
		return errors.New("agentregistry: Add called with zero RegisteredAt")
	}
	return nil
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

func (r *registryImpl) Snapshot() ([]Entry, error) {
	r.mu.RLock()
	out := make([]Entry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e)
	}
	r.mu.RUnlock()
	sortEntries(out)
	return out, nil
}

// sortEntries orders entries by (Project, AgentName) — the composite
// identity. Two projects can register the same short AgentName, so
// AgentName alone is not a unique key and sorting by it would leave
// the inter-project order undefined (Go map iteration order). Shared
// between the in-memory and sqlite Snapshot implementations so the
// ordering rule has one home; Less methods on the typed identity
// values keep the comparison free of `.String() < .String()`.
func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Project != entries[j].Project {
			return entries[i].Project.Less(entries[j].Project)
		}
		return entries[i].AgentName.Less(entries[j].AgentName)
	})
}
