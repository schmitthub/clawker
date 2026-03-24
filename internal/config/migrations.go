package config

import "github.com/schmitthub/clawker/internal/storage"

// projectMigrations returns migrations for the project config store.
// Migrations run on each file during load and auto-save if they return true.
func projectMigrations() []storage.Migration {
	return []storage.Migration{
		migrateRunInstructionsToStrings,
	}
}

// migrateRunInstructionsToStrings converts the legacy []RunInstruction format
// (list of {cmd: "...", alpine: "...", debian: "..."} maps) to plain []string
// (list of command strings). Only the "cmd" field is preserved; alpine/debian
// variants are dropped as they are no longer supported.
//
// Before: build.instructions.user_run: [{cmd: "npm ci"}, {cmd: "pip install"}]
// After:  build.instructions.user_run: ["npm ci", "pip install"]
func migrateRunInstructionsToStrings(raw map[string]any) bool {
	build, ok := raw["build"].(map[string]any)
	if !ok {
		return false
	}
	inst, ok := build["instructions"].(map[string]any)
	if !ok {
		return false
	}

	changed := false
	for _, key := range []string{"user_run", "root_run"} {
		items, ok := inst[key].([]any)
		if !ok || len(items) == 0 {
			continue
		}
		// Check if already migrated (first element is a string).
		if _, isStr := items[0].(string); isStr {
			continue
		}
		// Convert [{cmd: "x"}, ...] → ["x", ...]
		migrated := make([]any, 0, len(items))
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if cmd, ok := m["cmd"].(string); ok && cmd != "" {
				migrated = append(migrated, cmd)
			}
		}
		if len(migrated) > 0 {
			inst[key] = migrated
			changed = true
		}
	}
	return changed
}
