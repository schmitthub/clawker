// Package state owns the CLI's persisted runtime state: the update-check cache
// (last-checked timestamp, latest observed version) and the changelog cursor
// (the last changelog version the user has been shown).
//
// It is backed by storage.Store[State] — the same engine config and the
// project registry use — so every field mutation is a dirty-path merge with
// atomic writes, never a whole-struct marshal+rename. That field merge is what
// lets the background 24h update goroutine and the foreground changelog cursor
// write the same file without clobbering each other.
//
// The file lives in the state dir under consts.CLIStateFile, the same key the
// update checker uses. An existing install's state file is read in place — its
// checked_at / latest_version carry forward, and last_seen_changelog starts
// empty. Storage preserves unknown keys on re-save, so the dropLegacyUpdateKeys
// migration strips the legacy latest_url / current_version keys from an older
// binary's file on load rather than letting them linger.
package state

import (
	"fmt"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// StateStore is the domain facade over the CLI's persisted runtime state — a
// lazy, [sync.Once]-cached Factory noun (f.CLIState()) constructed by the factory
// closure via New, not by Main. The background update check and the changelog
// teaser share the one cached instance.
//
// Reads are value-specific: a consumer asks for the single value it needs, never
// a snapshot of the whole schema. Writes field-merge a disjoint subset of the
// fields, so independent writers cannot clobber each other.
//
//go:generate moq -rm -pkg mocks -out mocks/state_mock.go . StateStore
type StateStore interface {
	// CheckedAt reports when the update checker last queried GitHub; the zero
	// time means "never checked".
	CheckedAt() time.Time
	// LatestVersion reports the newest release version observed at the last
	// check (bare semver); empty means "none observed".
	LatestVersion() string
	// LastSeenChangelog reports the changelog cursor: the highest changelog
	// version already shown to the user. Empty means "not yet seeded".
	LastSeenChangelog() string

	// RecordUpdateCheck persists checked_at + latest_version as one unit.
	RecordUpdateCheck(checkedAt time.Time, latestVersion string) error
	// SetLastSeenChangelog persists the changelog cursor.
	SetLastSeenChangelog(version string) error
}

// stateStoreImpl is the storage-backed implementation of StateStore. It embeds
// *storage.Store[State] so the engine verbs stay reachable as the escape hatch;
// those promoted methods never leak past the StateStore interface, since the
// type is unexported and only ever handed out as the interface (the canonical
// store-backed pattern — see .claude/rules/store-backed-package.md).
type stateStoreImpl struct {
	*storage.Store[State]
}

// New constructs the CLI state facade over a file-backed store. The constructor
// IS the load: discovery, migrations, merge, and the strict schema decode all
// run here, so a broken state file surfaces as an error from New rather than at
// first read. All option wiring lives here, once.
func New() (StateStore, error) {
	store, err := storage.New[State](
		storage.WithFilenames(consts.CLIStateFile),
		storage.WithDefaultFilename(consts.CLIStateFile),
		storage.WithStateDir(),
		storage.WithMigrations(StateMigrations()...),
		storage.WithLock(),
	)
	if err != nil {
		return nil, fmt.Errorf("state: loading CLI state: %w", err)
	}
	return &stateStoreImpl{Store: store}, nil
}

// NewFromString is the in-memory seam: the seed YAML is the only layer, parsed
// through the real schema with no directory, no discovery, and no disk. It
// deliberately omits every path option so it can never read or write a file —
// that is the whole point. Used by mocks/stubs and by intra-package tests that
// need a seeded store without an isolated FS env. Migrations do NOT run on a
// seed; that path is covered by New() + testenv tests.
//
//nolint:ireturn // returns the StateStore domain interface by design — stateStoreImpl stays package-private
func NewFromString(seed string) (StateStore, error) {
	store, err := storage.NewFromString[State](seed)
	if err != nil {
		return nil, fmt.Errorf("state: loading CLI state from string: %w", err)
	}
	return &stateStoreImpl{Store: store}, nil
}

// CheckedAt returns the last update-check timestamp.
func (s *stateStoreImpl) CheckedAt() time.Time {
	v, err := storage.Get[time.Time](s.Store, "checked_at")
	if err != nil {
		// Absent (ErrKeyNotFound) → never checked, the domain default. No other
		// error is reachable: checked_at is a declared KindTime leaf, so the
		// strict decode in New already rejected an incompatible value.
		return time.Time{}
	}
	return v
}

// LatestVersion returns the newest release version seen by the update checker.
func (s *stateStoreImpl) LatestVersion() string {
	v, err := storage.Get[string](s.Store, "latest_version")
	if err != nil {
		// Absent (ErrKeyNotFound) → none observed. No other error is reachable
		// for a declared string leaf (see CheckedAt).
		return ""
	}
	return v
}

// LastSeenChangelog returns the show-once changelog cursor.
func (s *stateStoreImpl) LastSeenChangelog() string {
	v, err := storage.Get[string](s.Store, "last_seen_changelog")
	if err != nil {
		// Absent (ErrKeyNotFound) → unseeded cursor, which CheckForChanges reads
		// as a first run. No other error is reachable for a declared string leaf.
		return ""
	}
	return v
}

// RecordUpdateCheck persists the update-check fields (checked_at,
// latest_version) as one unit. It owns only those two fields — the changelog
// cursor belongs to SetLastSeenChangelog — so the two writers touch disjoint
// paths and cannot clobber each other's values.
func (s *stateStoreImpl) RecordUpdateCheck(checkedAt time.Time, latestVersion string) error {
	if err := s.Set([]string{"checked_at"}, checkedAt); err != nil {
		return fmt.Errorf("state: recording update check: setting checked_at: %w", err)
	}
	if err := s.Set([]string{"latest_version"}, latestVersion); err != nil {
		return fmt.Errorf("state: recording update check: setting latest_version: %w", err)
	}
	if err := s.Write(); err != nil {
		return fmt.Errorf("state: recording update check: %w", err)
	}
	return nil
}

// SetLastSeenChangelog persists the changelog cursor. It owns only
// last_seen_changelog — disjoint from RecordUpdateCheck's fields, so the two
// writers cannot clobber each other's values.
func (s *stateStoreImpl) SetLastSeenChangelog(version string) error {
	if err := s.Set([]string{"last_seen_changelog"}, version); err != nil {
		return fmt.Errorf("state: setting changelog cursor: %w", err)
	}
	if err := s.Write(); err != nil {
		return fmt.Errorf("state: setting changelog cursor: %w", err)
	}
	return nil
}
