package state

import "github.com/schmitthub/clawker/internal/storage"

// StateMigrations returns the migration functions for the CLI state store. They
// run on the discovered state file during load and trigger an atomic re-save
// when any returns true. The list is intentionally additive — append a
// migration here when the schema evolves; never edit a shipped one in place.
func StateMigrations() []storage.Migration {
	return []storage.Migration{
		dropLegacyUpdateKeys,
	}
}

// dropLegacyUpdateKeys removes keys written by the pre-store update checker that
// are no longer in the schema: latest_url and current_version. Before this
// branch the update checker marshalled its own struct straight to
// update-state.yaml; the store now reads the still-valid keys (checked_at,
// latest_version) in place, but storage preserves unknown keys on re-save — so
// without this the two dead keys would linger in the file indefinitely.
// Returning true triggers an atomic re-save of the cleaned file at load time.
// It is idempotent: a file with neither key returns false (no re-save).
func dropLegacyUpdateKeys(raw map[string]any) bool {
	changed := false
	// Historical wire keys from the deleted update-checker struct — there is no
	// live symbol to reference, so they are spelled out here intentionally.
	for _, key := range []string{"latest_url", "current_version"} {
		if _, ok := raw[key]; ok {
			delete(raw, key)
			changed = true
		}
	}
	return changed
}
