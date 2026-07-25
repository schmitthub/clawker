package firewall

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"sort"
	"sync"

	"github.com/schmitthub/clawker/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// MinIdentity is the first allocatable identity, partitioning the identity
// space: 0 is the "none" sentinel ([ebpf.RouteIdentity.IsNone]) and
// [1, MinIdentity) is reserved for future well-known infra identities.
// Identities are userspace-allocated ebpf.RouteIdentity values keying
// dns_cache and route_map (Cilium's local-identity pattern: allocated, never
// derived, never renumbered while live — a hash-derived or recomputed
// identity would let pinned dns_cache entries alias another domain's route
// after rule churn).
const MinIdentity ebpf.RouteIdentity = 256

// IdentityEntry is one persisted dst→identity allocation. IDs are int64 in
// the schema (the storage field set has no unsigned kind) but always hold
// uint32 values; the constructor range-checks on load.
type IdentityEntry struct {
	Dst string `yaml:"dst" label:"Destination"    desc:"Normalized rule destination holding the identity"`
	ID  int64  `yaml:"id"  label:"Route Identity" desc:"Allocated route identity, at or above the reserved band"`
}

// IdentityTableFile is the on-disk schema for the sticky identity table.
type IdentityTableFile struct {
	// Entries holds the live allocations, sorted by dst for stable diffs.
	Entries []IdentityEntry `yaml:"entries" label:"Entries" desc:"Live destination-to-identity allocations"`
	// Next is the round-robin allocation cursor. Persisting it keeps
	// freed identities out of circulation until the space wraps, so a
	// stale pinned dns_cache entry cannot alias a newly added dst.
	Next int64 `yaml:"next" label:"Next Identity" desc:"Round-robin allocation cursor keeping released identities out of circulation"`
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

// RouteIdentityStore is the domain facade over the persisted route-identity
// table. The IdentityAllocator depends on this interface and never touches
// storage.Store or knows a file exists.
//
// Reads are value-specific — the live allocations and the allocation cursor —
// and both fold an absent key to the fresh-file default. Neither folds a read
// FAILURE: an unreadable table presented as an empty one would renumber every
// live identity on the next sync, which is the precise aliasing bug the sticky
// table exists to prevent (a pinned dns_cache entry would resolve to another
// domain's route). SetTable is the single write: entries and cursor are the
// whole table and are only ever persisted together.
//
//go:generate moq -rm -pkg mocks -out mocks/route_identity_store_mock.go . RouteIdentityStore
type RouteIdentityStore interface {
	// Entries returns the persisted dst→identity allocations. An unset table
	// yields no entries — the fresh-file shape, not a failure.
	Entries() ([]IdentityEntry, error)
	// Cursor returns the persisted round-robin allocation cursor. An unset
	// cursor yields 0, which the allocator maps to the MinIdentity default.
	Cursor() (int64, error)
	// SetTable persists the allocations and the cursor as one unit.
	SetTable(entries []IdentityEntry, cursor int64) error
}

// routeIdentityStoreImpl is the storage-backed RouteIdentityStore. It embeds
// *storage.Store[IdentityTableFile] so the engine verbs stay reachable as the
// escape hatch; they never leak past the interface, since the type is
// unexported and only ever handed out as RouteIdentityStore.
type routeIdentityStoreImpl struct {
	*storage.Store[IdentityTableFile]
}

// NewIdentityStore opens the identity table in the firewall data subdirectory,
// beside the egress rules store. The constructor IS the load: a table whose
// schema no longer decodes fails here, at CP startup. All option wiring lives
// here, once.
//
//nolint:ireturn // returns the RouteIdentityStore domain interface by design — routeIdentityStoreImpl stays package-private
func NewIdentityStore(cfg config.Config) (RouteIdentityStore, error) {
	dataDir, err := cfg.FirewallDataSubdir()
	if err != nil {
		return nil, fmt.Errorf("firewall: resolving data dir: %w", err)
	}
	store, err := storage.New[IdentityTableFile](
		storage.WithFilenames(consts.RouteIdentitiesFile),
		storage.WithDefaultFilename(consts.RouteIdentitiesFile),
		storage.WithPaths(dataDir),
		storage.WithLock(), // flock: defend against a second CP process racing the table.
	)
	if err != nil {
		return nil, fmt.Errorf("firewall: loading identity table: %w", err)
	}
	return &routeIdentityStoreImpl{Store: store}, nil
}

// NewIdentityStoreFromString is the in-memory seam for the identity table: the
// seed YAML is the only layer, decoded through the real schema with no
// directory, no discovery, and no disk. It deliberately omits every path option
// so it can never read or write a file — SetTable errors on the result by
// design. Used by tests (and any consumer double built on mocks/) that need a
// seeded table without an isolated filesystem.
//
//nolint:ireturn // returns the RouteIdentityStore domain interface by design — routeIdentityStoreImpl stays package-private
func NewIdentityStoreFromString(seed string) (RouteIdentityStore, error) {
	store, err := storage.NewFromString[IdentityTableFile](seed)
	if err != nil {
		return nil, fmt.Errorf("firewall: loading identity table from string: %w", err)
	}
	return &routeIdentityStoreImpl{Store: store}, nil
}

// Entries implements RouteIdentityStore.
func (s *routeIdentityStoreImpl) Entries() ([]IdentityEntry, error) {
	entries, err := storage.Get[[]IdentityEntry](s.Store, identityEntriesField)
	if err != nil {
		if errors.Is(err, storage.ErrKeyNotFound) {
			return nil, nil // fresh file: no allocations yet
		}
		return nil, fmt.Errorf("firewall: reading identity entries: %w", err)
	}
	return entries, nil
}

// Cursor implements RouteIdentityStore.
func (s *routeIdentityStoreImpl) Cursor() (int64, error) {
	next, err := storage.Get[int64](s.Store, identityNextField)
	if err != nil {
		if errors.Is(err, storage.ErrKeyNotFound) {
			return 0, nil // fresh file: the allocator applies its MinIdentity default
		}
		return 0, fmt.Errorf("firewall: reading identity cursor: %w", err)
	}
	return next, nil
}

// SetTable implements RouteIdentityStore.
func (s *routeIdentityStoreImpl) SetTable(entries []IdentityEntry, cursor int64) error {
	// The load path refuses an out-of-range or ambiguous table as a startup
	// gate, so persisting one here would defer a write fault into a CP boot
	// failure — where the operator has no context for it and every enrolled
	// agent is already fail-closed. Reject at the write front door instead.
	if _, _, err := indexIdentityEntries(entries); err != nil {
		return fmt.Errorf("firewall: updating identity entries: %w", err)
	}
	if err := s.Set([]string{identityEntriesField}, entries); err != nil {
		return fmt.Errorf("firewall: updating identity entries: %w", err)
	}
	if err := s.Set([]string{identityNextField}, cursor); err != nil {
		return fmt.Errorf("firewall: updating identity cursor: %w", err)
	}
	if err := s.Write(); err != nil {
		return fmt.Errorf("firewall: writing identity table: %w", err)
	}
	return nil
}

// IdentityResolver answers "which identity does this dst hold" for route
// building and Corefile generation. Wire (*IdentityAllocator).IdentityFor.
// Returning ok=false means the dst holds no identity — consumers fail closed
// (no route, no dnsbpf write).
type IdentityResolver func(dst string) (ebpf.RouteIdentity, bool)

// missTrackingResolver wraps idFor so every dst it cannot answer for is
// recorded (deduped, first-seen order — dedicated-listener fan-out asks about
// one dst once per in-range port). The returned collect func snapshots the
// misses after a generation pass; callers surface them so a fail-closed drop
// ("domain resolves via DNS but connect() denies") is never silent.
func missTrackingResolver(idFor IdentityResolver) (IdentityResolver, func() []string) {
	seen := make(map[string]struct{})
	var missed []string
	tracking := func(dst string) (ebpf.RouteIdentity, bool) {
		id, ok := idFor(dst)
		if !ok {
			if _, dup := seen[dst]; !dup {
				seen[dst] = struct{}{}
				missed = append(missed, dst)
			}
		}
		return id, ok
	}
	return tracking, func() []string { return missed }
}

// IdentityAllocator owns the sticky dst→identity table. Allocation is
// round-robin next-free over MinIdentity..MaxUint32; a live dst keeps
// its identity across arbitrary rule churn and CP restarts (the table is
// persisted), and a released identity is not reissued until the cursor wraps.
//
// mu is the serializer for the table's read-modify-write: the in-memory maps
// are the mutated state, so every acquire/release/persist cycle must run under
// it. The allocator is the table's only writer.
type IdentityAllocator struct {
	mu    sync.Mutex
	store RouteIdentityStore
	byDst map[string]ebpf.RouteIdentity
	byID  map[ebpf.RouteIdentity]string
	next  ebpf.RouteIdentity
	// needsPersist is set whenever a sync mutates the table and cleared
	// only on a successful persist, so a failed write is retried by the
	// next sync even when that sync itself changes nothing — otherwise the
	// table would silently never reach disk and a restart would renumber
	// every live identity.
	needsPersist bool
}

// NewIdentityAllocator loads the persisted table. A corrupt table (identity
// below MinIdentity, or two dsts sharing an identity) fails construction:
// enforcing routes against an ambiguous table would silently misroute, so
// this is a startup-gate error, not a degrade.
func NewIdentityAllocator(store RouteIdentityStore) (*IdentityAllocator, error) {
	if store == nil {
		return nil, ErrNilIdentityStore
	}
	var a IdentityAllocator
	a.store = store
	a.next = MinIdentity

	// An unset table is the fresh-file shape, not a failure: it yields no
	// entries and the zero cursor, which becomes the MinIdentity default.
	entries, err := store.Entries()
	if err != nil {
		return nil, fmt.Errorf("identity allocator: %w", err)
	}
	next, err := store.Cursor()
	if err != nil {
		return nil, fmt.Errorf("identity allocator: %w", err)
	}
	byDst, byID, err := indexIdentityEntries(entries)
	if err != nil {
		return nil, err
	}
	a.byDst, a.byID = byDst, byID
	switch {
	case next >= int64(MinIdentity) && next <= math.MaxUint32:
		a.next = ebpf.RouteIdentity(next)
	case len(a.byDst) > 0:
		// The persisted cursor is what keeps released identities out of
		// circulation until the space wraps; a populated table without a
		// usable cursor cannot honor that, so it gets the same
		// startup-gate treatment as ambiguous entries. An empty table
		// keeps the MinIdentity default — a fresh or never-persisted
		// file legitimately carries no cursor.
		return nil, fmt.Errorf("identity table corrupt: cursor %d out of range [%d, %d]",
			next, MinIdentity, uint32(math.MaxUint32))
	}
	return &a, nil
}

// indexIdentityEntries validates a whole table and returns the two indexes the
// allocator runs on. It rejects what the allocator cannot adopt: an identity
// outside the allocatable band, two dsts sharing one identity, or one dst
// listed twice.
//
// Both the load path (NewIdentityAllocator) and the write path
// (RouteIdentityStore.SetTable) run it, so the two can never disagree about
// what a legal table is — a shape the writer accepts but the loader refuses is
// a CP that starts fine today and refuses to boot tomorrow, with every enrolled
// agent already fail-closed and no supervisor to explain why. The load path
// installs the returned indexes rather than rebuilding them, so what is
// validated is exactly what is adopted.
func indexIdentityEntries(
	entries []IdentityEntry,
) (map[string]ebpf.RouteIdentity, map[ebpf.RouteIdentity]string, error) {
	byDst := make(map[string]ebpf.RouteIdentity, len(entries))
	byID := make(map[ebpf.RouteIdentity]string, len(entries))
	for _, e := range entries {
		if e.ID < int64(MinIdentity) || e.ID > math.MaxUint32 {
			return nil, nil, fmt.Errorf("identity table corrupt: %q has out-of-range identity %d", e.Dst, e.ID)
		}
		id := ebpf.RouteIdentity(e.ID)
		if prev, dup := byID[id]; dup {
			return nil, nil, fmt.Errorf("identity table corrupt: identity %d held by both %q and %q", id, prev, e.Dst)
		}
		if _, dup := byDst[e.Dst]; dup {
			return nil, nil, fmt.Errorf("identity table corrupt: %q listed twice", e.Dst)
		}
		byDst[e.Dst] = id
		byID[id] = e.Dst
	}
	return byDst, byID, nil
}

// SyncDsts reconciles the table against the full desired dst set (rule dsts
// plus reserved internal hosts): dsts present keep their identity (sticky),
// new dsts are allocated, dsts no longer in the set are released. The set is
// declarative and this is its only writer, so set-diff gives the same
// lifetime semantics as per-caller reference counting. Persists when the
// table changed or an earlier persist is still owed (see needsPersist).
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

	if a.byDst == nil || a.byID == nil {
		// Zero-value allocator: allocating into nil maps would panic,
		// and CP code degrades instead of crashing.
		return errors.New("identity allocator not constructed")
	}

	released := a.releaseStaleLocked(desired)
	allocated, err := a.allocateMissingLocked(desired)
	if err != nil {
		return err
	}

	a.needsPersist = a.needsPersist || released || allocated
	if !a.needsPersist {
		return nil
	}
	if persistErr := a.persistLocked(); persistErr != nil {
		// needsPersist stays set: the in-memory maps already mutated, so
		// the next sync must retry the write even when it changes
		// nothing itself.
		return persistErr
	}
	a.needsPersist = false
	return nil
}

// releaseStaleLocked drops every allocation whose dst is no longer in the
// desired set. Returns true when anything was released. Caller holds a.mu.
func (a *IdentityAllocator) releaseStaleLocked(desired map[string]struct{}) bool {
	dirty := false
	for dst, id := range a.byDst {
		if _, keep := desired[dst]; !keep {
			delete(a.byDst, dst)
			delete(a.byID, id)
			dirty = true
		}
	}
	return dirty
}

// allocateMissingLocked mints an identity for every desired dst that holds
// none. Returns true when anything was allocated. Caller holds a.mu.
func (a *IdentityAllocator) allocateMissingLocked(desired map[string]struct{}) (bool, error) {
	dirty := false
	for dst := range desired {
		if _, have := a.byDst[dst]; have {
			continue
		}
		id, err := a.nextFreeLocked()
		if err != nil {
			return dirty, err
		}
		a.byDst[dst] = id
		a.byID[id] = dst
		dirty = true
	}
	return dirty, nil
}

// IdentityFor returns the identity for a dst (normalized before lookup).
func (a *IdentityAllocator) IdentityFor(dst string) (ebpf.RouteIdentity, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, ok := a.byDst[normalizeDst(dst)]
	return id, ok
}

// DomainFor returns the dst holding an identity — the netlogger attribution
// surface (identity → dst_host is a direct read, not a hash inversion).
func (a *IdentityAllocator) DomainFor(id ebpf.RouteIdentity) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	dst, ok := a.byID[id]
	return dst, ok
}

// Snapshot returns a copy of the live identity→dst table.
func (a *IdentityAllocator) Snapshot() map[ebpf.RouteIdentity]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[ebpf.RouteIdentity]string, len(a.byID))
	maps.Copy(out, a.byID)
	return out
}

// nextFreeLocked advances the round-robin cursor to the next unheld identity.
func (a *IdentityAllocator) nextFreeLocked() (ebpf.RouteIdentity, error) {
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
	// Entries and cursor are the whole table and are only ever written
	// together, which is why the store exposes them as one write. a.mu (held
	// by the caller) serializes the surrounding read-modify-write of the
	// in-memory maps; SetTable makes the persist itself indivisible.
	if err := a.store.SetTable(entries, int64(a.next)); err != nil {
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
