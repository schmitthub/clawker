package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/harness"
	"github.com/schmitthub/clawker/internal/storage"
)

// ProjectMigrations returns migrations for the project config store.
// Migrations run on the store during construction and auto-save if they return
// true. Exported so callers creating temporary probe stores (e.g.
// HasLocalProjectConfig) apply the same migrations as the production loader.
func ProjectMigrations() []storage.Migration[Project] {
	return []storage.Migration[Project]{
		migrateRunInstructionsToStrings,
	}
}

// SettingsMigrations returns migrations for the user settings store. Same
// shape as ProjectMigrations — runs on the settings store during construction,
// re-saving when any returns true.
func SettingsMigrations() []storage.Migration[Settings] {
	return []storage.Migration[Settings]{
		migrateRemoveLegacyMonitoringKeys,
		migrateSeedHarnessRegistry,
	}
}

// migrateSeedHarnessRegistry adds the harness registry to a settings.yaml
// that predates the harnesses key, seeding the built-in harness as the
// default so the registry — the harness customization surface — is visible
// and editable in the file. A file that already has the key (including an
// explicitly emptied one) is untouched; new shipped harnesses are registered
// by the build-time ensure, not by this migration.
func migrateSeedHarnessRegistry(s *storage.Store[Settings]) (bool, error) {
	if s.Has("harnesses") {
		return false, nil
	}
	seed := map[string]HarnessSettings{
		consts.DefaultHarnessName: {
			Default: true,
			// Every registry entry carries an explicit bundle path — no
			// name-keyed fallback resolution exists.
			Path: filepath.Join(consts.ConfigDir(), harness.HarnessesSubdir, consts.DefaultHarnessName),
		},
	}
	if err := s.Set("harnesses", seed); err != nil {
		return false, fmt.Errorf("seeding harness registry: %w", err)
	}
	return true, nil
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
}

// migrateRemoveLegacyMonitoringKeys strips removed monitoring keys
// from a settings file and warns the user on stderr. Without this the
// keys are silently dropped by yaml.Unmarshal and a custom port the
// user had set (e.g. otel_cp_port: 5319) disappears unnoticed. We
// print once per upgrade because the migration framework auto-saves
// the file when this returns true.
func migrateRemoveLegacyMonitoringKeys(s *storage.Store[Settings]) (bool, error) {
	if !s.Has("monitoring") {
		return false, nil
	}
	changed := false

	renamed, err := migrateOtelCPPort(s)
	if err != nil {
		return false, err
	}
	changed = changed || renamed

	var removed []string
	for _, key := range legacyMonitoringKeys {
		var v any
		exists, gErr := s.Get("monitoring."+key, &v)
		if gErr != nil {
			return false, fmt.Errorf("reading monitoring.%s: %w", key, gErr)
		}
		if !exists {
			continue
		}
		removed = append(removed, fmt.Sprintf("  monitoring.%s = %v", key, v))
		if _, rErr := s.Remove("monitoring." + key); rErr != nil {
			return false, fmt.Errorf("removing monitoring.%s: %w", key, rErr)
		}
	}
	if len(removed) == 0 {
		return changed, nil
	}
	sort.Strings(removed)
	fmt.Fprintln(os.Stderr, "warning: legacy monitoring settings removed in this clawker version:")
	for _, line := range removed {
		fmt.Fprintln(os.Stderr, line)
	}
	fmt.Fprintln(os.Stderr, "These keys reference services that no longer ship (Loki/Jaeger/Grafana) or have")
	fmt.Fprintln(os.Stderr, "been renamed; the values above are dropped. See `clawker monitor init` to scaffold")
	fmt.Fprintln(os.Stderr, "the OpenSearch + Prometheus stack with the current settings surface.")
	return true, nil
}

// migrateOtelCPPort renames the legacy monitoring.otel_cp_port to
// monitoring.otel_infra_port, carrying the value forward when only the legacy
// key is set and warning + dropping on collision.
func migrateOtelCPPort(s *storage.Store[Settings]) (bool, error) {
	var old any
	had, err := s.Get("monitoring.otel_cp_port", &old)
	if err != nil {
		return false, fmt.Errorf("reading monitoring.otel_cp_port: %w", err)
	}
	if !had {
		return false, nil
	}
	if _, rErr := s.Remove("monitoring.otel_cp_port"); rErr != nil {
		return false, fmt.Errorf("removing monitoring.otel_cp_port: %w", rErr)
	}
	if s.Has("monitoring.otel_infra_port") {
		fmt.Fprintf(
			os.Stderr,
			"warning: both monitoring.otel_cp_port (%v) and monitoring.otel_infra_port present; keeping otel_infra_port, dropping otel_cp_port\n",
			old,
		)
		return true, nil
	}
	if sErr := s.Set("monitoring.otel_infra_port", old); sErr != nil {
		return false, fmt.Errorf("setting monitoring.otel_infra_port: %w", sErr)
	}
	fmt.Fprintf(os.Stderr,
		"notice: monitoring.otel_cp_port renamed to monitoring.otel_infra_port; carried value %v forward\n", old)
	return true, nil
}

// migrateRunInstructionsToStrings converts the legacy []RunInstruction format
// (list of {cmd: "...", alpine: "...", debian: "..."} maps) to plain []string
// (list of command strings). Only the "cmd" field is preserved; alpine/debian
// variants are dropped as they are no longer supported.
//
// Before: build.instructions.user_run: [{cmd: "npm ci"}, {cmd: "pip install"}]
// After:  build.instructions.user_run: ["npm ci", "pip install"]
func migrateRunInstructionsToStrings(s *storage.Store[Project]) (bool, error) {
	changed := false
	for _, key := range []string{"user_run", "root_run"} {
		c, err := migrateRunList(s, "build.instructions."+key)
		if err != nil {
			return false, err
		}
		changed = changed || c
	}
	return changed, nil
}

// migrateRunList converts a single legacy [{cmd: "x"}, ...] run list at path to
// a plain []string. Returns false (no change) when the key is absent, empty, or
// already in string form. A list of legacy maps is rewritten to its cmd strings;
// a map without a non-empty "cmd" is dropped (a legacy alpine/debian-only entry,
// now unsupported), and a list whose entries all drop out becomes an empty list
// rather than being left in map form (which would fail the strict typed decode
// with an opaque error). A non-map element in an otherwise-legacy list is a
// hand-mangled config and returns an error instead of being silently discarded.
func migrateRunList(s *storage.Store[Project], path string) (bool, error) {
	var items []any
	found, err := s.Get(path, &items)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	if !found || len(items) == 0 {
		return false, nil
	}
	// Already migrated (first element is a string).
	if _, isStr := items[0].(string); isStr {
		return false, nil
	}
	migrated := make([]string, 0, len(items))
	for i, item := range items {
		m, isMap := item.(map[string]any)
		if !isMap {
			return false, fmt.Errorf(
				"migrating %s: element %d has unexpected type %T (want a legacy {cmd: ...} map)",
				path, i, item,
			)
		}
		if cmd, isStr := m["cmd"].(string); isStr && cmd != "" {
			migrated = append(migrated, cmd)
		}
	}
	// The list was in legacy map form (first element was not a string), so it
	// must be rewritten — even to an empty list when every entry dropped, so the
	// un-decodable map-shaped value never survives to the strict typed decode.
	if err = s.Set(path, migrated); err != nil {
		return false, fmt.Errorf("setting %s: %w", path, err)
	}
	return true, nil
}
