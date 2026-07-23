package firewall

import (
	"fmt"
	"maps"
	"math"
	"sort"
	"sync"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// Identity space partitioning. Identities are userspace-allocated u32 route
// identities keying dns_cache and route_map (Cilium's local-identity pattern:
// allocated, never derived, never renumbered while live — a hash-derived or
// recomputed identity would let pinned dns_cache entries alias another
// domain's route after rule churn).
const (
	// IdentityNone is the zero identity: "no domain attribution".
	// dns_cache misses and direct-IP connects observe it.
	IdentityNone uint32 = 0

	// MinIdentity is the first allocatable identity. [1, MinIdentity) is
	// reserved for future well-known infra identities.
	MinIdentity uint32 = 256
)

// IdentityEntry is one persisted dst→identity allocation. IDs are int64 in
// the schema (the storage field set has no unsigned kind) but always hold
// uint32 values; the constructor range-checks on load.
type IdentityEntry struct {
	Dst string `yaml:"dst"`
	ID  int64  `yaml:"id"`
}

// IdentityTableFile is the on-disk schema for the sticky identity table.
type IdentityTableFile struct {
	// Entries holds the live allocations, sorted by dst for stable diffs.
	Entries []IdentityEntry `yaml:"entries"`
	// Next is the round-robin allocation cursor. Persisting it keeps
	// freed identities out of circulation until the space wraps, so a
	// stale pinned dns_cache entry cannot alias a newly added dst.
	Next int64 `yaml:"next"`
}

// Fields implements [storage.Schema] for IdentityTableFile.
//
//nolint:ireturn // storage.Schema mandates returning the FieldSet interface.
func (f IdentityTableFile) Fields() storage.FieldSet {
	return storage.NormalizeFields(f)
}

const (
	identityEntriesField = "entries"
	identityNextField    = "next"
)

// NewIdentityStore creates the storage.Store for the identity table in the
// firewall data subdirectory, beside the egress rules store.
func NewIdentityStore(cfg config.Config) (*storage.Store[IdentityTableFile], error) {
	dataDir, err := cfg.FirewallDataSubdir()
	if err != nil {
		return nil, fmt.Errorf("firewall: resolving data dir: %w", err)
	}
	return storage.New[IdentityTableFile]("",
		storage.WithFilenames(consts.RouteIdentitiesFile),
		storage.WithPaths(dataDir),
		storage.WithLock(), // CP and break-glass tooling may share this file.
	)
}

// IdentityResolver answers "which identity does this dst hold" for route
// building and Corefile generation. Wire (*IdentityAllocator).IdentityFor.
// Returning ok=false means the dst holds no identity — consumers fail closed
// (no route, no dnsbpf write).
type IdentityResolver func(dst string) (uint32, bool)

// IdentityAllocator owns the sticky dst→identity table. Allocation is
// round-robin next-free over MinIdentity..MaxUint32; a live dst keeps
// its identity across arbitrary rule churn and CP restarts (the table is
// persisted), and a released identity is not reissued until the cursor wraps.
type IdentityAllocator struct {
	mu    sync.Mutex
	store *storage.Store[IdentityTableFile]
	byDst map[string]uint32
	byID  map[uint32]string
	next  uint32
}

// NewIdentityAllocator loads the persisted table. A corrupt table (identity
// below MinIdentity, or two dsts sharing an identity) fails construction:
// enforcing routes against an ambiguous table would silently misroute, so
// this is a startup-gate error, not a degrade.
func NewIdentityAllocator(store *storage.Store[IdentityTableFile]) (*IdentityAllocator, error) {
	var a IdentityAllocator
	a.store = store
	a.byDst = make(map[string]uint32)
	a.byID = make(map[uint32]string)
	a.next = MinIdentity

	entries, next, err := readPersistedTable(store)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if adoptErr := a.adoptEntry(e); adoptErr != nil {
			return nil, adoptErr
		}
	}
	if next >= int64(MinIdentity) && next <= math.MaxUint32 {
		a.next = uint32(next)
	}
	return &a, nil
}

// readPersistedTable reads the raw entries + cursor from disk.
func readPersistedTable(store *storage.Store[IdentityTableFile]) ([]IdentityEntry, int64, error) {
	var entries []IdentityEntry
	var next int64
	err := store.Txn(func(tx *storage.Tx[IdentityTableFile]) error {
		if _, err := tx.Get(identityEntriesField, &entries); err != nil {
			return fmt.Errorf("reading identity entries: %w", err)
		}
		if _, err := tx.Get(identityNextField, &next); err != nil {
			return fmt.Errorf("reading identity cursor: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("loading identity table: %w", err)
	}
	return entries, next, nil
}

// adoptEntry validates one persisted entry and installs it in both indexes.
func (a *IdentityAllocator) adoptEntry(e IdentityEntry) error {
	if e.ID < int64(MinIdentity) || e.ID > math.MaxUint32 {
		return fmt.Errorf("identity table corrupt: %q has out-of-range identity %d", e.Dst, e.ID)
	}
	id := uint32(e.ID)
	if prev, dup := a.byID[id]; dup {
		return fmt.Errorf("identity table corrupt: identity %d held by both %q and %q", id, prev, e.Dst)
	}
	if _, dup := a.byDst[e.Dst]; dup {
		return fmt.Errorf("identity table corrupt: %q listed twice", e.Dst)
	}
	a.byDst[e.Dst] = id
	a.byID[id] = e.Dst
	return nil
}

// SyncFromRules reconciles the table against a rule set — the
// rule-dst-only convenience over [IdentityAllocator.SyncDsts].
func (a *IdentityAllocator) SyncFromRules(rules []config.EgressRule) error {
	dsts := make([]string, 0, len(rules))
	for _, r := range rules {
		dsts = append(dsts, r.Dst)
	}
	return a.SyncDsts(dsts)
}

// SyncDsts reconciles the table against the full desired dst set (rule dsts
// plus reserved internal hosts): dsts present keep their identity (sticky),
// new dsts are allocated, dsts no longer in the set are released. The set is
// declarative and this is its only writer, so set-diff gives the same
// lifetime semantics as per-caller reference counting. Persists only when
// the table changed.
func (a *IdentityAllocator) SyncDsts(dsts []string) error {
	desired := make(map[string]struct{}, len(dsts))
	for _, d := range dsts {
		dst := normalizeDst(d)
		if dst == "" {
			continue
		}
		desired[dst] = struct{}{}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	dirty := false
	for dst, id := range a.byDst {
		if _, keep := desired[dst]; !keep {
			delete(a.byDst, dst)
			delete(a.byID, id)
			dirty = true
		}
	}
	for dst := range desired {
		if _, have := a.byDst[dst]; have {
			continue
		}
		id, err := a.nextFreeLocked()
		if err != nil {
			return err
		}
		a.byDst[dst] = id
		a.byID[id] = dst
		dirty = true
	}

	if !dirty {
		return nil
	}
	return a.persistLocked()
}

// IdentityFor returns the identity for a dst (normalized before lookup).
func (a *IdentityAllocator) IdentityFor(dst string) (uint32, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, ok := a.byDst[normalizeDst(dst)]
	return id, ok
}

// DomainFor returns the dst holding an identity — the netlogger attribution
// surface (identity → dst_host is a direct read, not a hash inversion).
func (a *IdentityAllocator) DomainFor(id uint32) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	dst, ok := a.byID[id]
	return dst, ok
}

// Snapshot returns a copy of the live identity→dst table.
func (a *IdentityAllocator) Snapshot() map[uint32]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[uint32]string, len(a.byID))
	maps.Copy(out, a.byID)
	return out
}

// nextFreeLocked advances the round-robin cursor to the next unheld identity.
func (a *IdentityAllocator) nextFreeLocked() (uint32, error) {
	start := a.next
	for {
		candidate := a.next
		if a.next == math.MaxUint32 {
			a.next = MinIdentity
		} else {
			a.next++
		}
		if _, taken := a.byID[candidate]; !taken {
			return candidate, nil
		}
		if a.next == start {
			return 0, fmt.Errorf("identity space exhausted (%d live identities)", len(a.byID))
		}
	}
}

func (a *IdentityAllocator) persistLocked() error {
	entries := make([]IdentityEntry, 0, len(a.byDst))
	for dst, id := range a.byDst {
		entries = append(entries, IdentityEntry{Dst: dst, ID: int64(id)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Dst < entries[j].Dst })
	next := int64(a.next)
	err := a.store.Txn(func(tx *storage.Tx[IdentityTableFile]) error {
		if err := tx.Set(identityEntriesField, entries); err != nil {
			return fmt.Errorf("updating identity entries: %w", err)
		}
		if err := tx.Set(identityNextField, next); err != nil {
			return fmt.Errorf("updating identity cursor: %w", err)
		}
		if err := tx.Write(); err != nil {
			return fmt.Errorf("writing identity table: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("persisting identity table: %w", err)
	}
	return nil
}

// normalizeDst canonicalizes a rule destination for identity lookup: IP
// literals and CIDRs pass through untouched, domains go through the same
// normalizeDomain pass CoreDNS zone generation uses. Dsts arrive
// pre-validated lowercase (ValidateDst rejects uppercase).
func normalizeDst(dst string) string {
	if dst == "" {
		return ""
	}
	if isIPOrCIDR(dst) {
		return dst
	}
	return normalizeDomain(dst)
}
