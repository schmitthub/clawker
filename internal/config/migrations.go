package config

import (
	"fmt"
	"os"
	"sort"

	"github.com/schmitthub/clawker/internal/storage"
)

// ProjectMigrations returns migrations for the project config store.
// Migrations run on each file during load and auto-save if they return true.
// Exported so that callers creating temporary probe stores (e.g. HasLocalProjectConfig)
// can apply the same migrations as the production config loader.
func ProjectMigrations() []storage.Migration {
	return []storage.Migration{
		migrateRunInstructionsToStrings,
	}
}

// SettingsMigrations returns migrations for the user settings store. Same
// shape as ProjectMigrations — runs on each settings.yaml during load, the
// file is rewritten when any returns true.
func SettingsMigrations() []storage.Migration {
	return []storage.Migration{
		migrateRemoveLegacyMonitoringKeys,
	}
}

// legacyMonitoringKeys is the set of monitoring.* keys removed in the
// OpenSearch refactor. migrateRemoveLegacyMonitoringKeys detects them
// in a user's settings.yaml on first load post-upgrade, prints a one-
// shot stderr notice naming each value the user had customized, then
// removes the keys so the file is rewritten clean.
var legacyMonitoringKeys = []string{
	"loki_port",
	"jaeger_port",
	"grafana_port",
	"otel_collector_internal",
	"otel_collector_endpoint",
	"otel_cp_port",
}

// migrateRemoveLegacyMonitoringKeys strips removed monitoring keys
// from a settings file and warns the user on stderr. Without this the
// keys are silently dropped by yaml.Unmarshal and a custom port the
// user had set (e.g. otel_cp_port: 5319) disappears unnoticed. We
// print once per upgrade because the migration framework auto-saves
// the file when this returns true.
func migrateRemoveLegacyMonitoringKeys(raw map[string]any) bool {
	mon, ok := raw["monitoring"].(map[string]any)
	if !ok {
		return false
	}
	var removed []string
	for _, key := range legacyMonitoringKeys {
		if v, exists := mon[key]; exists {
			removed = append(removed, fmt.Sprintf("  monitoring.%s = %v", key, v))
			delete(mon, key)
		}
	}
	if len(removed) == 0 {
		return false
	}
	sort.Strings(removed)
	fmt.Fprintln(os.Stderr, "warning: legacy monitoring settings removed in this clawker version:")
	for _, line := range removed {
		fmt.Fprintln(os.Stderr, line)
	}
	fmt.Fprintln(os.Stderr, "These keys reference services that no longer ship (Loki/Jaeger/Grafana) or have")
	fmt.Fprintln(os.Stderr, "been renamed; the values above are dropped. See `clawker monitor init` to scaffold")
	fmt.Fprintln(os.Stderr, "the OpenSearch + Prometheus stack with the current settings surface.")
	return true
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
