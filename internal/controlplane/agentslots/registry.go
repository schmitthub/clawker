// Package agentslots holds short-lived registration slots reserved by
// AdminService.AnnounceAgent and consumed by AgentService.Register.
//
// A slot stores the CLI-asserted attributes for one in-flight container
// startup: container_id, expected mTLS cert thumbprint, and a PKCE S256
// challenge. Consume is atomic and single-use — successful consumption
// deletes the slot, which is how replay defense works without a separate
// nonce field. Mismatched verifiers do NOT delete the slot; the TTL
// janitor handles eviction so the slot is still available for a legitimate
// retry from a transiently confused agent (a benign bug in clawkerd
// shouldn't hand the slot to an attacker just by failing once).
//
// Identity binding at this layer ends with "the verifier hashes to the
// stored challenge". Cert-thumbprint check, peer IP check, and label
// cross-check live in the AgentService handler and consume the Slot
// returned by Consume.
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
// and Register. Fields are written by Reserve and read at Consume; the
// caller treats the returned slot as immutable.
type Slot struct {
	AgentName              string
	ContainerID            string
	ExpectedCertThumbprint string
	Challenge              string
	ChallengeMethod        string
	ReservedAt             time.Time
	ExpiresAt              time.Time
}

// ErrSlotInvalid covers every failure mode of Consume — missing slot,
// expired slot, wrong verifier — collapsed into one sentinel so the
// error type itself does not leak which check failed. The handler maps
// it directly to codes.PermissionDenied with a generic "registration
// rejected" message.
var ErrSlotInvalid = errors.New("agentslots: slot invalid or expired")

// ErrSlotExists is returned by Reserve when a slot for this agent_name
// is already pending. Callers can distinguish this from invalid-slot
// because it's a CLI-side bug (announcing twice for the same name) rather
// than an attacker behavior.
var ErrSlotExists = errors.New("agentslots: slot already reserved")

// Registry is the consumer-facing contract.
//
//go:generate moq -rm -pkg mocks -out mocks/registry_mock.go . Registry
type Registry interface {
	// Reserve stores the slot keyed by AgentName. Duplicate names
	// return ErrSlotExists.
	Reserve(slot Slot) error
	// Consume verifies S256(verifier) == slot.Challenge in constant
	// time and atomically removes the slot on success. Mismatch /
	// missing / expired all map to ErrSlotInvalid; mismatch leaves the
	// slot in place so a benign retry can succeed.
	Consume(agentName, verifier string) (*Slot, error)
	// Len reports the number of live slots — used for the CP /healthz
	// snapshot and for tests.
	Len() int
	// Stop terminates the background TTL janitor cleanly.
	Stop()
}

type registryImpl struct {
	mu       sync.Mutex
	slots    map[string]Slot
	now      func() time.Time
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	log      *logger.Logger
	period   time.Duration
}

// NewRegistry creates a Registry and starts the background janitor.
// `now` is injectable for deterministic tests; pass `time.Now` in
// production. `sweepPeriod` controls how often the janitor wakes up;
// pass 0 to default to half of consts.AgentSlotTTL so a slot is at most
// half its TTL past expiry before eviction.
func NewRegistry(now func() time.Time, sweepPeriod time.Duration, log *logger.Logger) Registry {
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
	}
	r.wg.Add(1)
	go r.janitor()
	return r
}

func (r *registryImpl) Reserve(slot Slot) error {
	if slot.AgentName == "" {
		return errors.New("agentslots: agent name required")
	}
	if slot.ChallengeMethod != "S256" {
		// S256-only at the type level. The plan is explicit that no
		// other PKCE method is supported; reject early so a buggy CLI
		// doesn't reserve an unenforceable slot.
		return errors.New("agentslots: challenge method must be S256")
	}
	if !slot.ExpiresAt.After(slot.ReservedAt) {
		return errors.New("agentslots: slot already expired at reserve time")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.slots[slot.AgentName]; exists {
		return ErrSlotExists
	}
	r.slots[slot.AgentName] = slot
	return nil
}

func (r *registryImpl) Consume(agentName, verifier string) (*Slot, error) {
	// Hash the verifier unconditionally before any branching on slot
	// presence so an attacker probing for valid agent_names can't use
	// SHA-256 wall-clock latency to distinguish "name unknown" from
	// "name known, wrong verifier".
	expected := pkceChallenge(verifier)

	r.mu.Lock()
	defer r.mu.Unlock()

	slot, ok := r.slots[agentName]
	if !ok {
		return nil, ErrSlotInvalid
	}
	if !r.now().Before(slot.ExpiresAt) {
		delete(r.slots, agentName)
		return nil, ErrSlotInvalid
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(slot.Challenge)) != 1 {
		// Mismatch leaves the slot for a benign retry; TTL handles eviction.
		return nil, ErrSlotInvalid
	}
	delete(r.slots, agentName)
	return &slot, nil
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
	ticker := time.NewTicker(r.period)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			r.sweep()
		}
	}
}

func (r *registryImpl) sweep() {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	swept := 0
	for name, slot := range r.slots {
		if !now.Before(slot.ExpiresAt) {
			delete(r.slots, name)
			r.log.Debug().Str("agent", name).Msg("agentslots: swept expired slot")
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
