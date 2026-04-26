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

// Entry is one registered agent. Created by the Connect handler with
// data taken from the slot (ContainerID, AgentName, Project) plus the
// SHA-256 over the peer cert DER (Thumbprint). LastSeen currently
// equals RegisteredAt because Connect is the only per-agent RPC that
// has shipped; future per-agent RPCs will refresh LastSeen at their
// own boundary (tracked in cp-restart-resilience).
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
	// Add inserts (or replaces) an entry keyed by Entry.Thumbprint.
	// Container restart produces a new cert and a new thumbprint, so
	// re-registration creates a new entry; the dockerevents
	// subscription is responsible for evicting the stale one by
	// container ID.
	//
	// Add panics on invalid input (zero thumbprint, empty AgentName,
	// or zero RegisteredAt). The only caller is the in-package
	// agent.Handler which constructs entries from validated cross-
	// checks at Connect time — invalid input there is a programming
	// error that must surface loudly rather than corrupt the registry.
	Add(entry Entry)
	// Lookup retrieves an entry by cert thumbprint and verifies that the
	// supplied peer cert CN matches the entry's stored canonical
	// (Project, AgentName). The thumbprint resolves to at most one
	// entry; the CN cross-check defends against the case where a
	// thumbprint is somehow shared (impossible under SHA-256 collision
	// resistance, but the cross-check costs nothing and forces the
	// handler to thread the cert subject through the call). Mismatch on
	// thumbprint OR CN returns ErrUnknownAgent.
	Lookup(thumbprint [sha256.Size]byte, cn string) (*Entry, error)
	// EvictByContainerID removes any entry whose ContainerID matches.
	// Linear in the number of registered agents; that's fine for
	// realistic clawker host scales (single-digit agents).
	EvictByContainerID(containerID string)
	// Snapshot returns a copy of every live entry, sorted by
	// AgentName for deterministic output (used by AdminService.ListAgents
	// and the `clawker controlplane agents` CLI).
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

func (r *registryImpl) Add(entry Entry) {
	// Programming-error invariants — the only caller is the in-package
	// agent.Handler which has already verified each of these via the
	// five identity-binding cross-checks at Connect. A zero
	// Thumbprint here would key the registry to all-zero-byte
	// "identity" that any caller could trivially collide; an empty
	// AgentName breaks the Snapshot ordering contract; a zero
	// RegisteredAt breaks downstream observability. Panic loudly so
	// the wiring bug surfaces during development rather than turning
	// into a silent identity-binding gap.
	if entry.Thumbprint == ([sha256.Size]byte{}) {
		panic("agentregistry: Add called with zero thumbprint")
	}
	if entry.AgentName == "" {
		panic("agentregistry: Add called with empty AgentName")
	}
	if entry.RegisteredAt.IsZero() {
		panic("agentregistry: Add called with zero RegisteredAt")
	}
	r.mu.Lock()
	r.entries[entry.Thumbprint] = entry
	r.mu.Unlock()
	r.log.Info().
		Str("agent", entry.AgentName).
		Str("container_id", entry.ContainerID).
		Msg("agentregistry: agent registered")
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
	sort.Slice(out, func(i, j int) bool { return out[i].AgentName < out[j].AgentName })
	return out
}
