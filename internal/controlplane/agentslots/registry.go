// Package agentslots holds short-lived registration slots reserved by
// AdminService.AnnounceAgent and consumed by AgentService.Connect.
//
// A slot stores the CLI-asserted attributes for one in-flight container
// startup: container_id, expected mTLS cert thumbprint, and a PKCE S256
// challenge. Consume is atomic and single-use — successful consumption
// deletes the slot, which is how replay defense works without a separate
// nonce field. Mismatched verifiers do NOT delete the slot; the TTL
// janitor handles eviction. This means a hostile probe with a wrong
// verifier cannot burn a slot reserved for a legitimate caller — the
// legitimate retry can still consume it within TTL.
//
// Slots are keyed by the composite (cert_thumbprint, project, agent_name).
// For an honest CLI each AnnounceAgent retry mints a fresh leaf cert,
// producing a fresh thumbprint and a fresh slot key — so concurrent
// pending slots for the same (project, agent) tuple never collide. A
// duplicate composite key indicates caller misuse (re-Reserve under the
// same cert) rather than a benign overlap; surface as codes.AlreadyExists.
// The composite key folds the (project, agent) cross-check into the slot
// lookup itself: Consume must receive thumbprint AND project AND agent
// to find a slot, so an attacker cannot reuse a slot reserved for a
// different agent even if they somehow obtained the verifier — and the
// same short agent name (e.g. "dev") in two different projects keys two
// disjoint slots. Project enters the key (rather than being a side
// attribute) so this collision-impossibility argument generalizes from
// agent_name alone to the full project-scoped identity.
//
// Identity binding at this layer ends with "the verifier hashes to the
// stored challenge under the matching (thumbprint, project, agent) key".
// Peer IP check, cert CN check, and container label cross-check live
// in the AgentService.Connect handler and consume the Slot returned
// by Consume.
package agentslots

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// Slot is the per-agent reservation record stored between AnnounceAgent
// and Connect. Fields are written by Reserve and read at Consume; the
// caller treats the returned slot as immutable.
//
// ReservedAt and ExpiresAt are stamped INSIDE Reserve from the
// registry's clock; callers MUST NOT set those two fields. Doing so was
// previously possible and let a buggy caller reserve a slot that was
// already past its expiry, or with a bogus expiry far in the future,
// silently bypassing the TTL contract. Reserve now ignores both fields
// on input and overwrites them.
type Slot struct {
	// AgentName is the user-typed short name (e.g. "dev"). Paired with
	// Project to form the composite identity key — the canonical
	// "clawker.project.agent" form is composed downstream by the agent
	// handler when it cross-checks the peer cert CN.
	AgentName string
	// Project is the clawker project slug. Empty string is allowed and
	// matches the unscoped/2-segment naming case (docker.ContainerName
	// behavior). Two announces for the same agent name across different
	// projects key into disjoint slots so they never collide on Reserve.
	Project                string
	ContainerID            string
	ExpectedCertThumbprint [sha256.Size]byte
	Challenge              string
	ChallengeMethod        consts.ChallengeMethod
	// ReservedAt and ExpiresAt are written by Reserve; ignored on input.
	ReservedAt time.Time
	ExpiresAt  time.Time
}

// ErrSlotInvalid covers every failure mode of Consume — missing slot,
// expired slot, wrong verifier — collapsed into one sentinel so the
// error type itself does not leak which check failed. The handler maps
// it directly to codes.PermissionDenied with a generic "registration
// rejected" message.
var ErrSlotInvalid = errors.New("agentslots: slot invalid or expired")

// ErrSlotExists is returned by Reserve when a slot keyed by
// (cert_thumbprint, agent_name) is already pending. For an honest CLI
// the bootstrap path mints a fresh cert per AnnounceAgent, so a
// duplicate composite key indicates caller misuse — re-Reserve under
// the same cert, or a buggy retry path. Treat as fatal misuse: surface
// as codes.AlreadyExists. Do NOT treat as a signal to retry under a
// fresh challenge; the existing slot is still consumable.
var ErrSlotExists = errors.New("agentslots: slot already reserved")

// Registry is the consumer-facing contract.
//
//go:generate moq -rm -pkg mocks -out mocks/registry_mock.go . Registry
type Registry interface {
	// Reserve stores the slot keyed by (ExpectedCertThumbprint,
	// AgentName, Project). Reserve stamps ReservedAt + ExpiresAt from the
	// registry's clock; any value supplied by the caller on those two
	// fields is ignored. Duplicate composite keys return ErrSlotExists.
	// Empty Project is allowed (matches docker.ContainerName 2-segment
	// naming); empty AgentName is rejected.
	Reserve(slot Slot) error
	// Consume locates the slot keyed by (thumbprint, agentName, project)
	// and verifies S256(verifier) == slot.Challenge in constant time,
	// atomically removing the slot on success. Mismatch / missing /
	// expired all map to ErrSlotInvalid; verifier mismatch leaves the
	// slot in place so a benign retry can succeed (TTL handles eviction).
	Consume(thumbprint [sha256.Size]byte, agentName, project, verifier string) (*Slot, error)
	// EvictByContainerID removes any pending slot whose ContainerID
	// matches. Linear scan over slots; fine for realistic clawker host
	// scales (single-digit pending registrations). Mirrors
	// agentregistry's eviction shape so dockerevents can drive both
	// registries identically.
	EvictByContainerID(containerID string)
	// Len reports the number of live slots — used for the CP /healthz
	// snapshot and for tests.
	Len() int
	// Stop terminates the background TTL janitor cleanly.
	Stop()
}

// slotKey is the composite map key for pending slots: cert thumbprint
// plus (project, agent) tuple. Every component comes from the CLI's
// AnnounceAgent payload; clawkerd later proves them with the mTLS cert
// (thumbprint) and the ConnectRequest body (agent_name + project). For
// an honest CLI each retry mints a fresh cert, so concurrent slot keys
// for the same (project, agent) tuple never collide. Project is part of
// the key (not just a side attribute) so the same short agent name in
// two different projects keys two disjoint slots — a hard isolation
// boundary at the registry level.
type slotKey struct {
	Thumbprint [sha256.Size]byte
	AgentName  string
	Project    string
}

type registryImpl struct {
	mu       sync.Mutex
	slots    map[slotKey]Slot
	now      func() time.Time
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	log      *logger.Logger
	period   time.Duration
	// tickC drives janitor sweeps. Default is a real time.Ticker; tests
	// inject a channel they can drive deterministically via
	// NewRegistryForTesting so a sweep test doesn't have to wall-clock-
	// poll the result.
	tickC <-chan time.Time
}

// NewRegistry creates a Registry and starts the background janitor.
// `now` is injectable for deterministic tests; pass `time.Now` in
// production. `sweepPeriod` controls how often the janitor wakes up;
// pass 0 to default to half of consts.AgentSlotTTL so a slot is at most
// half its TTL past expiry before eviction.
func NewRegistry(now func() time.Time, sweepPeriod time.Duration, log *logger.Logger) Registry {
	return constructRegistry(now, sweepPeriod, log, nil)
}

// NewRegistryWithPulseChan is a test-only constructor that lets the
// caller drive janitor sweeps deterministically via a channel of
// time.Time pulses. Production code MUST use NewRegistry — this
// constructor exists so the janitor sweep test doesn't have to wall-
// clock-poll for eviction completion.
func NewRegistryWithPulseChan(now func() time.Time, log *logger.Logger, pulse <-chan time.Time) Registry {
	return constructRegistry(now, time.Hour, log, pulse)
}

func constructRegistry(now func() time.Time, sweepPeriod time.Duration, log *logger.Logger, pulse <-chan time.Time) Registry {
	if log == nil {
		log = logger.Nop()
	}
	if now == nil {
		now = time.Now
	}
	if sweepPeriod <= 0 {
		sweepPeriod = consts.AgentSlotTTL / 2
	}
	r := &registryImpl{
		slots:  make(map[slotKey]Slot),
		now:    now,
		stop:   make(chan struct{}),
		log:    log,
		period: sweepPeriod,
		tickC:  pulse,
	}
	r.wg.Add(1)
	go r.janitor()
	return r
}

func (r *registryImpl) Reserve(slot Slot) error {
	if slot.AgentName == "" {
		return errors.New("agentslots: agent name required")
	}
	if slot.ChallengeMethod != consts.ChallengeMethodS256 {
		// S256-only at the type level. The plan is explicit that no
		// other PKCE method is supported; reject early so a buggy CLI
		// doesn't reserve an unenforceable slot.
		return errors.New("agentslots: challenge method must be S256")
	}
	// Programming-error invariants — the only caller is the
	// AdminService.AnnounceAgent handler, which derives both fields
	// from the CLI's signed claim. A zero thumbprint here would key
	// the slot under all-zeros, breaking the "fresh cert per retry"
	// composite-collision argument; an empty Challenge would let
	// subtle.ConstantTimeCompare("", "") trivially pass against an
	// empty verifier. Panic loudly so the wiring bug surfaces during
	// development rather than turning into a silent identity-binding
	// gap. Mirrors the same posture in agentregistry.Add.
	if slot.ExpectedCertThumbprint == ([sha256.Size]byte{}) {
		panic("agentslots: Reserve called with zero ExpectedCertThumbprint")
	}
	if slot.Challenge == "" {
		panic("agentslots: Reserve called with empty Challenge")
	}

	// Stamp ReservedAt/ExpiresAt from the registry's clock so callers
	// cannot supply a pre-expired or future-dated TTL. Any caller value
	// is overwritten.
	now := r.now()
	slot.ReservedAt = now
	slot.ExpiresAt = now.Add(consts.AgentSlotTTL)

	key := slotKey{Thumbprint: slot.ExpectedCertThumbprint, AgentName: slot.AgentName, Project: slot.Project}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.slots[key]; exists {
		return ErrSlotExists
	}
	r.slots[key] = slot
	return nil
}

func (r *registryImpl) Consume(thumbprint [sha256.Size]byte, agentName, project, verifier string) (*Slot, error) {
	// Hash the verifier unconditionally before any branching on slot
	// presence so an attacker probing for valid (thumbprint, project,
	// agent) tuples can't use SHA-256 wall-clock latency to distinguish
	// "key unknown" from "key known, wrong verifier".
	expected := pkceChallenge(verifier)

	key := slotKey{Thumbprint: thumbprint, AgentName: agentName, Project: project}
	r.mu.Lock()
	defer r.mu.Unlock()

	slot, ok := r.slots[key]
	if !ok {
		return nil, ErrSlotInvalid
	}
	if !r.now().Before(slot.ExpiresAt) {
		delete(r.slots, key)
		return nil, ErrSlotInvalid
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(slot.Challenge)) != 1 {
		// Mismatch leaves the slot for a benign retry; TTL handles eviction.
		return nil, ErrSlotInvalid
	}
	delete(r.slots, key)
	return &slot, nil
}

func (r *registryImpl) EvictByContainerID(containerID string) {
	r.mu.Lock()
	var evicted []Slot
	for k, slot := range r.slots {
		if slot.ContainerID == containerID {
			delete(r.slots, k)
			evicted = append(evicted, slot)
		}
	}
	r.mu.Unlock()
	for _, slot := range evicted {
		r.log.Info().
			Str("agent", slot.AgentName).
			Str("container_id", slot.ContainerID).
			Msg("agentslots: pending slot evicted on container exit")
	}
}

func (r *registryImpl) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.slots)
}

func (r *registryImpl) Stop() {
	r.stopOnce.Do(func() { close(r.stop) })
	r.wg.Wait()
}

func (r *registryImpl) janitor() {
	defer r.wg.Done()
	tickC := r.tickC
	if tickC == nil {
		ticker := time.NewTicker(r.period)
		defer ticker.Stop()
		tickC = ticker.C
	}
	for {
		select {
		case <-r.stop:
			return
		case _, ok := <-tickC:
			if !ok {
				// Test-side pulse channel was closed — exit cleanly.
				return
			}
			r.sweep()
		}
	}
}

func (r *registryImpl) sweep() {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	swept := 0
	for key, slot := range r.slots {
		if !now.Before(slot.ExpiresAt) {
			delete(r.slots, key)
			r.log.Debug().Str("agent", key.AgentName).Msg("agentslots: swept expired slot")
			swept++
		}
	}
	if swept > 0 {
		// Surface non-zero sweeps at info — an agent that announces but
		// never registers (firewall break, container hang) shows up here.
		r.log.Info().Int("swept", swept).Msg("agentslots: evicted expired slots")
	}
}

// pkceChallenge mirrors the CLI's S256 derivation:
// base64url(sha256(verifier)) with no padding. Used by both Consume
// and the package's own tests to produce challenges from verifiers.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
