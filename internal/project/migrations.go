package project

import "github.com/schmitthub/clawker/internal/storage"

// RegistryMigrations returns the migration functions for the project registry.
// They run inside construction, once against each discovered file layer, and
// trigger an atomic re-save of that layer when any returns true. The list is
// intentionally additive — append a migration here when the registry schema
// evolves; never edit a shipped one in place. Each migration must be
// idempotent and precondition-guarded so an already-current file is untouched.
func RegistryMigrations() []storage.Migration[ProjectRegistry] {
	return nil
}
