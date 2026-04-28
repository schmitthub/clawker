// Package agentregistry tracks live agents that have completed the
// AgentService.Connect handshake. It is populated by the Connect
// handler and evicted by an informer subscription that watches
// container die/destroy events.
//
// Identity is channel-bound: the registry key is the SHA-256 thumbprint
// of the mTLS peer cert from the TLS handshake. Lookup is the only path
// for per-agent gRPC handlers to resolve the caller — there is no path
// where an agent claims an identity other than what its TLS cert proves.
// Container restart yields a new cert and a new thumbprint; the old
// entry remains briefly until the dockerevents subscription evicts it
// by container ID.
package agentregistry

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/logger"
)

// Entry is one registered agent. Created by the Register handler with
// data taken from the slot (ContainerID, AgentName, Project) plus the
// SHA-256 over the peer cert DER (Thumbprint). LastSeen currently
// equals RegisteredAt because the per-agent RPCs that bump it have not
// shipped yet; future per-agent RPCs will refresh LastSeen at their
// own boundary.
type Entry struct {
	// AgentName is the user-typed short name (e.g. "dev"); composed with
	// Project at Lookup time to verify against the peer cert's CN.
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
// peer cert's CN not to match the entry's stored (Project, AgentName).
// All three failure modes collapse into one sentinel — the handler maps
// it to a generic codes.PermissionDenied (matching every other Connect
// rejection) so callers can't probe which half of the composite identity
// failed.
var ErrUnknownAgent = errors.New("agentregistry: unknown agent")

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
	// error into the appropriate gRPC status; nothing reaches the
	// in-memory cache when persistence rejects the write so the on-disk
	// state and the cache stay in sync.
	//
	// Add panics on invalid input (zero thumbprint, empty AgentName,
	// empty Attestation, or zero RegisteredAt). The only callers are
	// in-package handlers that construct entries from validated cross-
	// checks at Register time — invalid input there is a programming
	// error that must surface loudly rather than corrupt the registry.
	Add(entry Entry) error
	// Lookup retrieves an entry by cert thumbprint and verifies that the
	// supplied peer cert CN matches the entry's stored canonical
	// (Project, AgentName). The thumbprint resolves to at most one
	// entry; the CN cross-check defends against the case where a
	// thumbprint is somehow shared (impossible under SHA-256 collision
	// resistance, but the cross-check costs nothing and forces the
	// handler to thread the cert subject through the call). Mismatch on
	// thumbprint OR CN returns ErrUnknownAgent.
	Lookup(thumbprint [sha256.Size]byte, cn string) (*Entry, error)
	// LookupByThumbprint returns the entry whose Thumbprint matches,
	// without any CN cross-check. Used by the Register handler to
	// REJECT clawkerd that calls Register while it already has a row —
	// the verifier-wipe contract means a Register call against a known
	// thumbprint indicates a stale or replayed bootstrap, not a legit
	// flow. Distinct from Lookup (CN-gated identity resolution used by
	// AgentPort RPCs) so a future regression that swaps the two stays
	// loud at the call site.
	//
	// Returns (nil, ErrUnknownAgent) when no entry matches.
	LookupByThumbprint(thumbprint [sha256.Size]byte) (*Entry, error)
	// LookupByContainerID returns the entry whose ContainerID matches,
	// without any CN cross-check. Used by AdminService.AnnounceAgent to
	// short-circuit when the CLI announces a container that already has
	// a registry row (clawkerd skips Register on the next boot, CP skips
	// slot reservation + verifier delivery). The peer authentication for
	// AnnounceAgent itself is mTLS + JWT scope on the AdminPort, so no
	// additional cross-check is needed at this read.
	//
	// Returns (nil, ErrUnknownAgent) when no entry matches.
	LookupByContainerID(containerID string) (*Entry, error)
	// EvictByContainerID removes any entry whose ContainerID matches.
	// Linear in the number of registered agents; that's fine for
	// realistic clawker host scales (single-digit agents).
	EvictByContainerID(containerID string)
	// Snapshot returns a copy of every live entry, sorted by
	// (Project, AgentName) for deterministic output. Project is the
	// primary key because the same short AgentName can be reused across
	// different projects (the composite identity is (project, agent) —
	// see internal/controlplane/agentslots/CLAUDE.md). Used by
	// AdminService.ListAgents and the `clawker controlplane agents`
	// CLI; both rely on stable ordering for diffability.
	Snapshot() []Entry
}

type registryImpl struct {
	mu      sync.RWMutex
	entries map[[sha256.Size]byte]Entry
	log     *logger.Logger
}

// NewRegistry constructs an empty registry. Logger is required (use
// logger.Nop() in tests) so audit-trail messages on Add and Evict are
// captured even when production logging is otherwise disabled.
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
	r.mu.Lock()
	r.entries[entry.Thumbprint] = entry
	r.mu.Unlock()
	r.log.Info().
		Str("agent", entry.AgentName).
		Str("container_id", entry.ContainerID).
		Msg("agentregistry: agent registered")
	return nil
}

// validateEntry runs the programming-error invariants shared by every
// Registry.Add path. Failure is a wiring bug — panic so it surfaces
// during development rather than corrupting the registry. Centralised
// so the in-memory and sqlite impls stay in lockstep.
func validateEntry(entry Entry) {
	if entry.Thumbprint == ([sha256.Size]byte{}) {
		panic("agentregistry: Add called with zero thumbprint")
	}
	if entry.AgentName == "" {
		panic("agentregistry: Add called with empty AgentName")
	}
	if entry.ContainerID == "" {
		panic("agentregistry: Add called with empty ContainerID")
	}
	if entry.RegisteredAt.IsZero() {
		panic("agentregistry: Add called with zero RegisteredAt")
	}
}

func (r *registryImpl) LookupByThumbprint(thumbprint [sha256.Size]byte) (*Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[thumbprint]
	if !ok {
		return nil, ErrUnknownAgent
	}
	e := entry
	return &e, nil
}

func (r *registryImpl) LookupByContainerID(containerID string) (*Entry, error) {
	if containerID == "" {
		return nil, ErrUnknownAgent
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.entries {
		if entry.ContainerID == containerID {
			e := entry
			return &e, nil
		}
	}
	return nil, ErrUnknownAgent
}

func (r *registryImpl) Lookup(thumbprint [sha256.Size]byte, cn string) (*Entry, error) {
	r.mu.RLock()
	entry, ok := r.entries[thumbprint]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownAgent
	}
	// Cross-check the supplied peer cert CN against the entry's stored
	// canonical (Project, AgentName). Composed via auth.CanonicalAgentCN
	// so the rule lives in exactly one place — same source of truth the
	// CLI used at MintAgentCert time. ConstantTimeCompare matches the
	// timing-discipline of the Connect handler's own CN check (peer cert
	// CN vs req-derived canonical) so a future regression that caches a
	// thumbprint without invalidating it on rename can't be probed via
	// per-byte CN compare latency.
	// entry.Project / entry.AgentName were validated by the Connect
	// handler before reaching Add() — wrap via MustProjectSlug /
	// MustAgentName so a future regression that bypasses the wire-side
	// validation surfaces as a startup-or-first-call panic instead of
	// silent identity drift.
	want := auth.CanonicalAgentCN(auth.MustProjectSlug(entry.Project), auth.MustAgentName(entry.AgentName))
	if subtle.ConstantTimeCompare([]byte(cn), []byte(want)) != 1 {
		return nil, ErrUnknownAgent
	}
	return &entry, nil
}

func (r *registryImpl) EvictByContainerID(containerID string) {
	r.mu.Lock()
	var evicted []Entry
	for tp, entry := range r.entries {
		if entry.ContainerID == containerID {
			delete(r.entries, tp)
			evicted = append(evicted, entry)
		}
	}
	r.mu.Unlock()
	for _, entry := range evicted {
		r.log.Info().
			Str("agent", entry.AgentName).
			Str("container_id", entry.ContainerID).
			Msg("agentregistry: agent evicted")
	}
}

func (r *registryImpl) Snapshot() []Entry {
	r.mu.RLock()
	out := make([]Entry, 0, len(r.entries))
	for _, entry := range r.entries {
		out = append(out, entry)
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
