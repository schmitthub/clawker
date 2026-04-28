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
	// Reserve stores a slot keyed by slot.ContainerID. AdminService.
	// AnnounceAgent calls this on every container start; the slot is
	// the CP's record that the clawker CLI specifically initiated
	// this start (raw `docker start` paths produce no slot). The slot
	// carries no auth-bearing material — agent identity verification
	// flows through agentregistry when CP dials the running clawkerd.
	// Duplicate container_id returns ErrSlotExists. Reserve stamps
	// ReservedAt + ExpiresAt from the registry's clock.
	//
	// The Slot type retains optional PKCE/thumbprint fields preserved
	// for future agent→CP RPCs that may rebind to a per-cert flow;
	// callers that don't need them leave them zero-valued.
	Reserve(slot Slot) error
	// Consume removes and returns the slot for container_id. Returns
	// ErrSlotInvalid on no slot or expired slot. Single-use — a second
	// Consume of the same container_id returns ErrSlotInvalid even if
	// the first was within TTL.
	Consume(containerID string) (*Slot, error)
	// EvictByContainerID removes any slot whose ContainerID matches.
	// Mirrors agentregistry's eviction shape so dockerevents can drive
	// both registries identically.
	EvictByContainerID(containerID string)
	// Len reports the number of live slots — used for the CP
	// /healthz snapshot and for tests.
	Len() int
	// Stop terminates the background TTL janitor cleanly.
	Stop()
}

type registryImpl struct {
	mu sync.Mutex
	// slots is keyed solely by container_id. AdminService.AnnounceAgent
	// reserves on every container start; agentdial consumes when CP
	// successfully dials the container's clawkerd listener. The slot
	// carries no auth-bearing material — its presence is the data
	// point that says "this start was clawker-CLI-initiated".
	slots    map[string]Slot
	now      func() time.Time
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	log      *logger.Logger
	period   time.Duration
	// tickC drives janitor sweeps. Default is a real time.Ticker; tests
	// inject a channel they can drive deterministically via
	// NewRegistryWithPulseChan so a sweep test doesn't have to
	// wall-clock-poll the result.
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
		slots:  make(map[string]Slot),
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
	if slot.ContainerID == "" {
		// Programming-error invariant: the only caller is the
		// AdminService.AnnounceAgent handler, which validates a
		// non-empty container_id at the wire boundary. Panic loudly
		// so the wiring bug surfaces during development rather than
		// turning into a silent identity-binding gap.
		panic("agentslots: Reserve called with empty ContainerID")
	}
	now := r.now()
	slot.ReservedAt = now
	slot.ExpiresAt = now.Add(consts.AgentSlotTTL)

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.slots[slot.ContainerID]; exists {
		return ErrSlotExists
	}
	r.slots[slot.ContainerID] = slot
	return nil
}

func (r *registryImpl) Consume(containerID string) (*Slot, error) {
	if containerID == "" {
		return nil, ErrSlotInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	slot, ok := r.slots[containerID]
	if !ok {
		return nil, ErrSlotInvalid
	}
	if !r.now().Before(slot.ExpiresAt) {
		delete(r.slots, containerID)
		return nil, ErrSlotInvalid
	}
	delete(r.slots, containerID)
	return &slot, nil
}

func (r *registryImpl) EvictByContainerID(containerID string) {
	if containerID == "" {
		return
	}
	r.mu.Lock()
	_, ok := r.slots[containerID]
	if ok {
		delete(r.slots, containerID)
	}
	r.mu.Unlock()
	if ok {
		r.log.Info().
			Str("container_id", containerID).
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
	for containerID, slot := range r.slots {
		if !now.Before(slot.ExpiresAt) {
			delete(r.slots, containerID)
			r.log.Debug().Str("container_id", containerID).Msg("agentslots: swept expired slot")
			_ = slot
			swept++
		}
	}
	if swept > 0 {
		// Surface non-zero sweeps at info — a container that announced
		// but never had its clawkerd dialed shows up here.
		r.log.Info().Int("swept", swept).Msg("agentslots: evicted expired slots")
	}
}
