// Package agentregistry tracks live agents that have completed the
// AgentService.Register handshake. It is populated by the Register
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
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

// Entry is one registered agent. Created by the Register handler with
// data taken from the slot (ContainerID, AgentName) plus the SHA-256
// over the peer cert DER (Thumbprint). LastSeen is updated on every
// successful per-agent RPC via Touch; registration is currently the
// only writer because Register is the only per-agent RPC that exists.
type Entry struct {
	AgentName    string
	ContainerID  string
	Thumbprint   [sha256.Size]byte
	RegisteredAt time.Time
	LastSeen     time.Time
}

// ErrUnknownAgent is returned by Lookup when no entry matches the
// thumbprint. Distinguishable from "agent disconnected" because the
// thumbprint is channel-bound: the only way to fail a Lookup is for
// the cert to have never registered, or to have been evicted.
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
	Add(entry Entry)
	// Lookup retrieves an entry by cert thumbprint. The hash equality
	// IS the identity check; no further matching is required.
	Lookup(thumbprint [sha256.Size]byte) (*Entry, error)
	// Touch refreshes LastSeen on a thumbprint. No-op for unknown
	// thumbprints — the caller has already verified identity if it
	// reaches this point.
	Touch(thumbprint [sha256.Size]byte)
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
	r.mu.Lock()
	r.entries[entry.Thumbprint] = entry
	r.mu.Unlock()
	r.log.Info().
		Str("agent", entry.AgentName).
		Str("container_id", entry.ContainerID).
		Msg("agentregistry: agent registered")
}

func (r *registryImpl) Lookup(thumbprint [sha256.Size]byte) (*Entry, error) {
	r.mu.RLock()
	entry, ok := r.entries[thumbprint]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownAgent
	}
	return &entry, nil
}

func (r *registryImpl) Touch(thumbprint [sha256.Size]byte) {
	r.mu.Lock()
	entry, ok := r.entries[thumbprint]
	if ok {
		entry.LastSeen = time.Now()
		r.entries[thumbprint] = entry
	}
	r.mu.Unlock()
	if !ok {
		// Surface unexpected misses at debug — the contract is that
		// callers have already authenticated, so a miss usually means
		// a race with eviction or a thumbprint-derivation bug.
		r.log.Debug().Msg("agentregistry: touch on unknown thumbprint")
	}
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
