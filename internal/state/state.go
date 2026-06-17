// Package state owns the CLI's persisted runtime state: the update-check cache
// (last-checked timestamp, latest observed version) and the changelog cursor
// (the last changelog version the user has been shown).
//
// It is backed by storage.Store[State] — the same engine config and the
// project registry use — so every field mutation is a dirty-path merge under a
// mutex with atomic writes, never a whole-struct marshal+rename. That field
// merge is what lets the background 24h update goroutine and the foreground
// changelog cursor write the same file without clobbering each other.
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

// State is the facade over the CLI's persisted runtime state. It is NOT a
// Factory noun: Main constructs one per process via New and shares the single
// facade between the background update check and the changelog teaser. Readers
// get an immutable snapshot; mutators field-merge a single field without
// disturbing siblings.
//
//go:generate moq -rm -pkg mocks -out mocks/state_mock.go . StateStore
type StateStore interface {
	State() *State
	RecordUpdateCheck(checkedAt time.Time, latestVersion string) error
	SetLastSeenChangelog(version string) error
}

// stateStoreImpl is the storage-backed implementation of StateStore. It embeds
// *storage.Store[State] for the Read/Set/Write primitives; those promoted methods
// never leak past the StateStore interface, since the type is unexported and only
// ever handed out as the interface (the canonical store-backed pattern — see
// .claude/rules/store-backed-package.md).
type stateStoreImpl struct {
	*storage.Store[State]
}

// NewFromString creates a StateStore seeded from a YAML string, returning an
// error if the seed fails to load. The seed is merged as the lowest-priority
// virtual layer through the real storage pipeline.
func NewFromString(stateStr string) (StateStore, error) {
	store, err := storage.NewFromString[State](stateStr)
	if err != nil {
		return nil, fmt.Errorf("state: loading CLI state from string: %w", err)
	}
	return &stateStoreImpl{Store: store}, nil
}

// New constructs the CLI state facade, reading through the storage layer
// (merge + migrations + atomic writes) — the canonical path for clawker files,
// never a raw file read.
func New() (StateStore, error) {
	stateStore, err := storage.NewFromString[State]("",
		storage.WithFilenames(consts.CLIStateFile),
		storage.WithDefaultFilename(consts.CLIStateFile),
		storage.WithMigrations(StateMigrations()...),
		storage.WithStateDir(),
		storage.WithLock(),
	)
	if err != nil {
		return nil, fmt.Errorf("state: loading CLI state: %w", err)
	}
	return &stateStoreImpl{Store: stateStore}, nil
}

// State returns an immutable snapshot of the persisted CLI state (delegates to
// the embedded store's Read). Reads go through st.State().<Field>; there are no
// per-field getters.
func (s *stateStoreImpl) State() *State {
	return s.Read()
}

// RecordUpdateCheck field-merges the update-check fields (checked_at,
// latest_version) and persists them. It does NOT touch last_seen_changelog —
// the changelog cursor is owned by SetLastSeenChangelog — so the background
// update goroutine cannot clobber the cursor.
func (s *stateStoreImpl) RecordUpdateCheck(checkedAt time.Time, latestVersion string) error {
	if err := s.Set(func(st *State) {
		st.CheckedAt = checkedAt
		st.LatestVersion = latestVersion
	}); err != nil {
		return fmt.Errorf("state: recording update check: %w", err)
	}
	if err := s.Write(); err != nil {
		return fmt.Errorf("state: writing CLI state: %w", err)
	}
	return nil
}

// SetLastSeenChangelog field-merges the changelog cursor and persists it. It
// does NOT touch the update-check fields, so it cannot clobber a concurrent
// update-check write.
func (s *stateStoreImpl) SetLastSeenChangelog(version string) error {
	if err := s.Set(func(st *State) {
		st.LastSeenChangelog = version
	}); err != nil {
		return fmt.Errorf("state: setting changelog cursor: %w", err)
	}
	if err := s.Write(); err != nil {
		return fmt.Errorf("state: writing CLI state: %w", err)
	}
	return nil
}
