// Package agent owns the CP-side agent surface: the persisted
// identity registry, the AgentService.Register handler, the per-RPC
// IdentityInterceptor, the CP→clawkerd Session dialer, and the
// AgentRegistered/AgentUntrusted event types.
//
// Registry rows record the (mTLS cert thumbprint, container_id,
// project, agent_name, canonical_cn) tuple. Reads are issued by:
//
//   - IdentityInterceptor on every per-agent gRPC RPC
//     (cert thumbprint → registry entry; CN cross-check inside Lookup)
//   - the dialer at Hello time (classifyRegistry → drives
//     AgentRegistered / AgentUntrusted publication)
//   - AdminService.ListAgents
//
// Writes are CP-only: the Register handler captures the live mTLS
// peer's thumbprint and writes the row. Eviction: startup orphan-row
// reap (in agent.Start), plus dockerevents container/destroy
// (subscribed in agent.Start). Stop/die/kill do NOT evict because a
// stopped container can be `docker start`-ed back into life.
//
// Identity is channel-bound: the registry key is `(thumbprint,
// container_id)` (both UNIQUE in sqlite). The canonical CN composed
// from `(project, agent_name)` is stored as a column at Add time and
// compared via `subtle.ConstantTimeCompare` inside Lookup — no
// reconstruction at read time.
package agent

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
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
	// AgentName is the user-typed short name (e.g. "dev"); composed with
	// Project at Add time into the canonical CN that Lookup compares
	// against the peer cert's Subject.CommonName.
	AgentName string
	// Project is the clawker project slug under which the agent
	// registered. Empty string is allowed and matches the unscoped
	// 2-segment naming case (docker.ContainerName).
	Project      string
	ContainerID  string
	Thumbprint   [sha256.Size]byte
	RegisteredAt time.Time
	LastSeen     time.Time
}

// ErrUnknownAgent is returned by Lookup when no entry matches the
// thumbprint+CN pair. Distinguishable from "agent disconnected" because
// the thumbprint is channel-bound: the only way to fail a Lookup is for
// the cert to have never registered, to have been evicted, or for the
// peer cert's CN not to match the entry's stored canonical CN.
// All three failure modes collapse into one sentinel — the handler maps
// it to a generic codes.PermissionDenied (matching every other Connect
// rejection) so callers can't probe which half of the composite identity
// failed.
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
	// Add computes the canonical agent CN from Entry.Project +
	// Entry.AgentName via auth.NewProjectSlug / auth.NewAgentName (the
	// err-returning typed constructors). Malformed identity strings
	// surface as a returned error, NOT as a panic — historical
	// MustProjectSlug / MustAgentName at Lookup time was a CP-wide
	// crash vector. Callers must validate at the wire boundary.
	//
	// Returns an error when the canonical-CN composition rejects the
	// inputs OR when the persistence layer (sqlite) rejects the write —
	// disk full, schema corruption, UNIQUE collision against a stale
	// row that hasn't been evicted yet. Callers translate the error
	// into the appropriate gRPC status.
	//
	// Add panics on programming-error invariants (zero thumbprint,
	// empty ContainerID, zero RegisteredAt). Empty AgentName is treated
	// as user-input violation and surfaces as an error from
	// auth.NewAgentName.
	Add(entry Entry) error
	// Lookup retrieves an entry by cert thumbprint and verifies that the
	// supplied peer cert CN matches the entry's pre-computed canonical
	// CN with subtle.ConstantTimeCompare. Mismatch on thumbprint OR CN
	// returns ErrUnknownAgent.
	Lookup(thumbprint [sha256.Size]byte, cn string) (*Entry, error)
	// LookupByContainerID returns the entry whose ContainerID matches,
	// without any CN cross-check. Used by the dialer at Hello time to
	// drive registry classification (Match / Miss / ThumbprintMismatch
	// / CNMismatch) and decide whether to send RegisterRequired or
	// publish AgentUntrusted. The dialer performs the thumbprint + CN
	// comparisons against the returned entry itself; this read
	// intentionally does not gate on either so a mismatch surfaces as
	// a typed local outcome rather than as ErrUnknownAgent.
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

// canonicalCNFromEntry composes the canonical agent CN from the string
// fields on Entry. Returns an error rather than panicking when Project
// or AgentName is malformed — historical Must* paths crashed CP on a
// single bad row. Callers Add returns this error directly so the wire
// boundary surfaces it as codes.Internal / codes.InvalidArgument.
func canonicalCNFromEntry(e Entry) (string, error) {
	proj, err := auth.NewProjectSlug(e.Project)
	if err != nil {
		return "", fmt.Errorf("agentregistry: invalid project: %w", err)
	}
	agent, err := auth.NewAgentName(e.AgentName)
	if err != nil {
		return "", fmt.Errorf("agentregistry: invalid agent name: %w", err)
	}
	return auth.CanonicalAgentCN(proj, agent), nil
}

// memEntry pairs an Entry with its pre-computed canonical CN so Lookup
// can compare against the peer cert CN without recomposing on the hot
// path. Pre-compute also means a single bad row in the in-memory impl
// fails at Add (typed-constructor error) rather than at every Lookup.
type memEntry struct {
	e  Entry
	cn string
}

type registryImpl struct {
	mu      sync.RWMutex
	entries map[[sha256.Size]byte]memEntry
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
		entries: make(map[[sha256.Size]byte]memEntry),
		log:     log,
	}
}

func (r *registryImpl) Add(entry Entry) error {
	validateEntry(entry)
	cn, err := canonicalCNFromEntry(entry)
	if err != nil {
		return err
	}
	if entry.LastSeen.IsZero() {
		entry.LastSeen = entry.RegisteredAt
	}
	r.mu.Lock()
	r.entries[entry.Thumbprint] = memEntry{e: entry, cn: cn}
	r.mu.Unlock()
	r.log.Info().
		Str("agent", entry.AgentName).
		Str("project", entry.Project).
		Str("container_id", entry.ContainerID).
		Msg("agentregistry: agent registered")
	return nil
}

// validateEntry runs the programming-error invariants shared by every
// Registry.Add path. Failure is a wiring bug — panic so it surfaces
// during development rather than corrupting the registry. AgentName
// and Project string-validity are NOT checked here; they flow through
// canonicalCNFromEntry which returns errors on malformed input.
func validateEntry(entry Entry) {
	if entry.Thumbprint == ([sha256.Size]byte{}) {
		panic("agentregistry: Add called with zero thumbprint")
	}
	if entry.ContainerID == "" {
		panic("agentregistry: Add called with empty ContainerID")
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
	for _, me := range r.entries {
		if me.e.ContainerID == containerID {
			e := me.e
			return &e, nil
		}
	}
	return nil, ErrUnknownAgent
}

func (r *registryImpl) Lookup(thumbprint [sha256.Size]byte, cn string) (*Entry, error) {
	r.mu.RLock()
	me, ok := r.entries[thumbprint]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownAgent
	}
	// Pre-computed canonical CN; ConstantTimeCompare so a future
	// regression that caches a thumbprint without invalidating it on
	// rename can't be probed via per-byte CN compare latency.
	if subtle.ConstantTimeCompare([]byte(cn), []byte(me.cn)) != 1 {
		return nil, ErrUnknownAgent
	}
	e := me.e
	return &e, nil
}

func (r *registryImpl) EvictByContainerID(containerID string) error {
	r.mu.Lock()
	var evicted []Entry
	for tp, me := range r.entries {
		if me.e.ContainerID == containerID {
			delete(r.entries, tp)
			evicted = append(evicted, me.e)
		}
	}
	r.mu.Unlock()
	for _, entry := range evicted {
		r.log.Info().
			Str("agent", entry.AgentName).
			Str("container_id", entry.ContainerID).
			Msg("agentregistry: agent evicted")
	}
	return nil
}

func (r *registryImpl) Snapshot() []Entry {
	r.mu.RLock()
	out := make([]Entry, 0, len(r.entries))
	for _, me := range r.entries {
		out = append(out, me.e)
	}
	r.mu.RUnlock()
	// Sort by (Project, AgentName) — the composite identity. Two
	// projects can register the same short AgentName, so AgentName
	// alone is not a unique key and sorting by it leaves the
	// inter-project order undefined (Go map iteration order).
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].AgentName < out[j].AgentName
	})
	return out
}
