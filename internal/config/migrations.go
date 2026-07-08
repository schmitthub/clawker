package config

import (
	"fmt"
	"os"
	"sort"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// ProjectMigrations returns migrations for the project config store.
// Migrations run on the store during construction and auto-save if they return
// true. Exported so callers creating temporary probe stores (e.g.
// HasLocalProjectConfig) apply the same migrations as the production loader.
func ProjectMigrations() []storage.Migration[Project] {
	return []storage.Migration[Project]{
		migrateRunInstructionsToStrings,
		// Order matters: the legacy-key strip runs before the claude_code →
		// harnesses rewrite so agent.claude_code.use_host_auth (a deleted
		// field) is removed with its own notice instead of riding the block
		// move into harnesses.claude as a dead key.
		migrateRemoveLegacyBuildKeys,
		migrateClaudeCodeToHarnesses,
	}
}

// SettingsMigrations returns migrations for the user settings store. Same
// shape as ProjectMigrations — runs on the settings store during construction,
// re-saving when any returns true.
//
// The settings-side harness/stack registry was retired in the stack-contract
// redesign: registration now lives in the project's clawker.yaml and shipped
// definitions resolve from the binary's embedded assets. The registry-key
// migration strips any leftover settings-side stacks:/harnesses: keys so
// storage's preserve-unknown-keys behaviour doesn't keep them on disk forever.
func SettingsMigrations() []storage.Migration[Settings] {
	return []storage.Migration[Settings]{
		migrateRemoveLegacyMonitoringKeys,
		migrateRemoveLegacyRegistryKeys,
	}
}

// legacySettingsRegistryKeys returns the settings-side registry nodes removed
// in the stack-contract redesign (registration moved to project clawker.yaml).
func legacySettingsRegistryKeys() []string {
	return []string{"stacks", "harnesses"}
}

// migrateRemoveLegacyRegistryKeys strips the retired settings-side stacks:/
// harnesses: registry blocks from a user's settings.yaml on first load
// post-upgrade and prints a one-shot stderr notice. Without it the keys would
// linger (storage preserves unknown keys on re-save) and read as still-live
// configuration when they no longer do anything.
func migrateRemoveLegacyRegistryKeys(s *storage.Store[Settings]) (bool, error) {
	var removed []string
	for _, key := range legacySettingsRegistryKeys() {
		if !s.Has(key) {
			continue
		}
		removed = append(removed, key)
		if _, rErr := s.Remove(key); rErr != nil {
			return false, fmt.Errorf("removing settings %s: %w", key, rErr)
		}
	}
	if len(removed) == 0 {
		return false, nil
	}
	sort.Strings(removed)
	fmt.Fprintln(os.Stderr, "warning: the settings-side stack/harness registry was removed in this clawker version:")
	for _, key := range removed {
		fmt.Fprintf(os.Stderr, "  settings %s: (dropped)\n", key)
	}
	fmt.Fprintln(os.Stderr, "Register stacks and harnesses per-project in clawker.yaml with `clawker stack register`")
	fmt.Fprintln(os.Stderr, "and `clawker harness register`; shipped definitions need no registration.")
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

// legacyUseHostAuthKey is the deleted host-credential-copy toggle: host
// credentials are no longer copied into containers at all, so the key's
// removal doubles as the user's notice that the auth model changed.
const legacyUseHostAuthKey = "agent.claude_code.use_host_auth"

// migrateRemoveLegacyBuildKeys strips project keys deleted in the
// multi-harness refactor — build.image/build.dockerfile/build.context (images
// now build from the pinned clawker substrate; a user-selected base image and
// the custom-Dockerfile path are gone) and agent.claude_code.use_host_auth —
// and warns the user on stderr, naming each removed key with the value it
// carried and pointing at the replacement surface. Without the migration the
// keys would be silently ignored (and preserved on re-save) while the build
// produced something entirely different from what the user configured.
// Mirrors migrateRemoveLegacyMonitoringKeys: one-shot notice, file rewritten
// clean by the migration framework's auto-save, per file layer (a legacy key
// duplicated in clawker.local.yaml or the user config-dir clawker.yaml is
// cleaned in each owning file).
func migrateRemoveLegacyBuildKeys(s *storage.Store[Project]) (bool, error) {
	buildRemoved, removed, err := stripLegacyKeys(s, []string{
		"build.image",
		"build.dockerfile",
		"build.context",
	})
	if err != nil {
		return false, err
	}
	hostAuthRemoved, hostAuthLines, err := stripLegacyKeys(s, []string{legacyUseHostAuthKey})
	if err != nil {
		return false, err
	}
	removed = append(removed, hostAuthLines...)
	if len(removed) == 0 {
		return false, nil
	}
	if pruneErr := pruneStrippedParents(s, buildRemoved, hostAuthRemoved); pruneErr != nil {
		return false, pruneErr
	}
	printLegacyKeyNotice(removed, buildRemoved, hostAuthRemoved)
	return true, nil
}

// stripLegacyKeys removes each present key, returning whether anything was
// removed plus a "  key = value" notice line per removal.
func stripLegacyKeys(s *storage.Store[Project], keys []string) (bool, []string, error) {
	var removed []string
	for _, key := range keys {
		var v any
		exists, gErr := s.Get(key, &v)
		if gErr != nil {
			return false, nil, fmt.Errorf("reading %s: %w", key, gErr)
		}
		if !exists {
			continue
		}
		removed = append(removed, fmt.Sprintf("  %s = %v", key, v))
		if _, rErr := s.Remove(key); rErr != nil {
			return false, nil, fmt.Errorf("removing %s: %w", key, rErr)
		}
	}
	return len(removed) > 0, removed, nil
}

// pruneStrippedParents removes the parent blocks the legacy-key strip may
// have hollowed out (a file that only pinned build.image, or a claude_code
// block that only set use_host_auth), so `build: {}` noise never lands on
// disk. Only parents actually stripped from are considered.
func pruneStrippedParents(s *storage.Store[Project], buildRemoved, hostAuthRemoved bool) error {
	var parents []string
	if buildRemoved {
		parents = append(parents, "build")
	}
	if hostAuthRemoved {
		parents = append(parents, "agent.claude_code", "agent")
	}
	for _, parent := range parents {
		if err := removeEmptyMapping(s, parent); err != nil {
			return err
		}
	}
	return nil
}

// printLegacyKeyNotice emits the one-shot stderr notice for the legacy-key
// strip: each removed key with its value, then the replacement guidance for
// whichever key families were hit.
func printLegacyKeyNotice(removed []string, buildRemoved, hostAuthRemoved bool) {
	sort.Strings(removed)
	fmt.Fprintln(os.Stderr, "warning: legacy project config keys removed in this clawker version:")
	for _, line := range removed {
		fmt.Fprintln(os.Stderr, line)
	}
	if buildRemoved {
		fmt.Fprintln(
			os.Stderr,
			"Images now build from the pinned clawker substrate; build.image, build.dockerfile, and",
		)
		fmt.Fprintln(
			os.Stderr,
			"build.context no longer apply. Declare languages with build.stacks and customize",
		)
		fmt.Fprintln(os.Stderr, "the image with build.packages, build.instructions, and build.inject.")
	}
	if hostAuthRemoved {
		fmt.Fprintln(
			os.Stderr,
			"Host credentials are no longer copied into containers; harness auth happens in-container.",
		)
		fmt.Fprintln(os.Stderr, "Authenticate once on first run — the login persists in the harness config volume.")
	}
}

// migrateClaudeCodeToHarnesses moves the deprecated agent.claude_code block
// to harnesses.claude, the project-root map entry that replaced it. The move
// is field-for-field — the legacy key decodes into the same HarnessConfig
// shape as a harnesses map entry — via a raw mapping so any unknown keys ride
// along instead of being silently dropped. When a harnesses.claude entry
// already exists, it out-ranks the legacy block (Project.HarnessConfigFor
// consults the map before the shim), so the legacy key is dropped with a
// notice instead of moved. The read shim in schema.go stays as a safety net
// for layers loaded without migrations (read-only contexts).
//
// Migrations run per file layer, and layers merge the harnesses map
// whole-map last-wins — a moved entry in a lower-priority layer can be masked
// by a higher layer's harnesses map, the same clobber semantics any two
// layered harnesses maps already have.
func migrateClaudeCodeToHarnesses(s *storage.Store[Project]) (bool, error) {
	const legacyKey = "agent.claude_code"
	newKey := "harnesses." + consts.DefaultHarnessName

	var v any
	exists, err := s.Get(legacyKey, &v)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", legacyKey, err)
	}
	if !exists {
		return false, nil
	}
	block, isMap := v.(map[string]any)
	if !isMap {
		return false, fmt.Errorf(
			"migrating %s: unexpected type %T (want a mapping)", legacyKey, v,
		)
	}

	switch {
	case len(block) == 0:
		fmt.Fprintf(os.Stderr,
			"notice: removed empty deprecated %s block from project config (its replacement is the %s map entry)\n",
			legacyKey, newKey)
	case s.Has(newKey):
		fmt.Fprintf(os.Stderr,
			"warning: dropped deprecated %s from project config — the existing %s entry already overrides it\n",
			legacyKey, newKey)
	default:
		if sErr := s.Set(newKey, block); sErr != nil {
			return false, fmt.Errorf("setting %s: %w", newKey, sErr)
		}
		fmt.Fprintf(os.Stderr,
			"notice: moved project config %s to %s (its replacement)\n",
			legacyKey, newKey)
	}
	if _, rErr := s.Remove(legacyKey); rErr != nil {
		return false, fmt.Errorf("removing %s: %w", legacyKey, rErr)
	}
	if pruneErr := removeEmptyMapping(s, "agent"); pruneErr != nil {
		return false, pruneErr
	}
	return true, nil
}

// removeEmptyMapping deletes path when it currently holds an empty mapping —
// the residue a legacy-key strip leaves behind. Absent keys, non-mapping
// values, and non-empty mappings are left untouched.
func removeEmptyMapping(s *storage.Store[Project], path string) error {
	var v any
	exists, err := s.Get(path, &v)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if !exists {
		return nil
	}
	m, isMap := v.(map[string]any)
	if !isMap || len(m) != 0 {
		return nil
	}
	if _, rErr := s.Remove(path); rErr != nil {
		return fmt.Errorf("removing emptied %s: %w", path, rErr)
	}
	return nil
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
