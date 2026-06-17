package state

import (
	"time"

	"github.com/schmitthub/clawker/internal/storage"
)

// State is the CLI's persisted runtime state, stored as YAML in the state
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

// Fields implements [storage.Schema] for State.
func (s State) Fields() storage.FieldSet {
	return storage.NormalizeFields(s)
}
