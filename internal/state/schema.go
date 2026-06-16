package state

import (
	"time"

	"github.com/schmitthub/clawker/internal/storage"
)

// CliState is the CLI's persisted runtime state, stored as YAML in the state
// dir. It implements storage.Schema so it can back a storage.Store.
type State struct {
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
func (s State) Fields() storage.FieldSet {
	return storage.NormalizeFields(s)
}

// KeyNotFoundError indicates a configuration key was not found.
type KeyNotFoundError struct {
	Key string
}

func (e KeyNotFoundError) Error() string {
	return "key not found: " + e.Key
}
