// Package state owns the CLI's persisted runtime state: the update-check cache
// (last-checked timestamp, latest observed version) and the changelog cursor
// (the last changelog version the user has been shown).
//
// It is backed by storage.Store[CliState] — the same engine config and the
// project registry use — so every field mutation is a dirty-path merge under a
// mutex with atomic writes, never a whole-struct marshal+rename. That field
// merge is what lets the background 24h update goroutine and the foreground
// changelog cursor write the same file without clobbering each other.
//
// The file lives in the state dir under consts.CliStateFile, the same key the
// update checker uses. An existing install's state file is read in place — its
// checked_at / latest_version / current_version carry forward, and
// last_seen_changelog starts empty.
package state

import (
	"fmt"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// CliState is the CLI's persisted runtime state, stored as YAML in the state
// dir. It implements storage.Schema so it can back a storage.Store.
type CliState struct {
	// CheckedAt is when the update checker last queried GitHub.
	CheckedAt time.Time `yaml:"checked_at,omitempty" label:"Checked At" desc:"Timestamp of the last update check"`
	// LatestVersion is the newest release version observed at the last check
	// (bare semver, no leading v).
	LatestVersion string `yaml:"latest_version,omitempty" label:"Latest Version" desc:"Newest release version seen by the update checker"`
	// LastSeenChangelog is the changelog cursor: the highest changelog version
	// already shown to the user. The show-once teaser displays entries in
	// (LastSeenChangelog, current]. Empty means "not yet seeded".
	LastSeenChangelog string `yaml:"last_seen_changelog,omitempty" label:"Last Seen Changelog" desc:"Highest changelog version already shown to the user (cursor)"`
}

// Fields implements [storage.Schema] for CliState.
func (s CliState) Fields() storage.FieldSet {
	return storage.NormalizeFields(s)
}

// Migrations returns the migration functions for the CLI state store. They run
// on the discovered state file during load and trigger an atomic re-save when
// any returns true. The list is intentionally additive — append a migration
// here when the schema evolves; never edit a shipped one in place.
//
// The current list is empty: the CLI state file shares this schema's yaml keys
// exactly (checked_at / latest_version), so it is read in place with no
// transformation. A legacy current_version key from an older binary is simply
// ignored (no longer in the schema); last_seen_changelog starts absent and is
// seeded at runtime by the changelog cursor logic.
func Migrations() []storage.Migration {
	return []storage.Migration{}
}

// State is the facade over the CLI state store. Construct one per process via
// New and inject it; the CLI factory exposes it as f.State. Readers get an
// immutable snapshot; mutators field-merge a single field without disturbing
// siblings.
type State struct {
	store *storage.Store[CliState]
}

// Option configures New.
type Option func(*options)

type options struct {
	dir string
}

// WithStateDirOverride places the state file in dir instead of the resolved
// state directory. Injection seam for tests; production callers use the
// default (the XDG state dir).
func WithStateDirOverride(dir string) Option {
	return func(o *options) { o.dir = dir }
}

// New constructs the CLI state facade, reading through the storage layer
// (merge + migrations + atomic writes) — the canonical path for clawker files,
// never a raw file read.
func New(opts ...Option) (*State, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	storageOpts := []storage.Option{
		storage.WithFilenames(consts.CliStateFile),
		storage.WithMigrations(Migrations()...),
		storage.WithLock(),
	}
	if o.dir != "" {
		storageOpts = append(storageOpts, storage.WithPaths(o.dir))
	} else {
		storageOpts = append(storageOpts, storage.WithStateDir())
	}
	store, err := storage.New[CliState]("", storageOpts...)
	if err != nil {
		return nil, fmt.Errorf("state: loading CLI state: %w", err)
	}
	return &State{store: store}, nil
}

// Read returns an immutable snapshot of the current CLI state. Never nil for a
// store built by New.
func (s *State) Read() CliState {
	cur := s.store.Read()
	if cur == nil {
		return CliState{}
	}
	return *cur
}

// LastCheckedAt returns the timestamp of the last update check (zero if never).
func (s *State) LastCheckedAt() time.Time { return s.Read().CheckedAt }

// LatestVersion returns the newest release version seen at the last check.
func (s *State) LatestVersion() string { return s.Read().LatestVersion }

// LastSeenChangelog returns the changelog cursor (empty if not yet seeded).
func (s *State) LastSeenChangelog() string { return s.Read().LastSeenChangelog }

// RecordUpdateCheck field-merges the update-check fields (checked_at,
// latest_version) and persists them. It does NOT touch last_seen_changelog —
// the changelog cursor is owned by SetLastSeenChangelog — so the background
// update goroutine cannot clobber the cursor.
func (s *State) RecordUpdateCheck(checkedAt time.Time, latestVersion string) error {
	if err := s.store.Set(func(st *CliState) {
		st.CheckedAt = checkedAt
		st.LatestVersion = latestVersion
	}); err != nil {
		return fmt.Errorf("state: recording update check: %w", err)
	}
	if err := s.store.Write(); err != nil {
		return fmt.Errorf("state: writing CLI state: %w", err)
	}
	return nil
}

// SetLastSeenChangelog field-merges the changelog cursor and persists it. It
// does NOT touch the update-check fields, so it cannot clobber a concurrent
// update-check write.
func (s *State) SetLastSeenChangelog(version string) error {
	if err := s.store.Set(func(st *CliState) {
		st.LastSeenChangelog = version
	}); err != nil {
		return fmt.Errorf("state: setting changelog cursor: %w", err)
	}
	if err := s.store.Write(); err != nil {
		return fmt.Errorf("state: writing CLI state: %w", err)
	}
	return nil
}
