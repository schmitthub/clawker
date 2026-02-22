package storage

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// layer holds the raw data from a single discovered file.
type layer struct {
	path     string         // absolute path to the source file
	filename string         // which filename matched (e.g., "clawker.yaml")
	data     map[string]any // raw YAML data from this file only
}

// loadFile reads a YAML file, runs migrations, and returns the raw map.
// If any migration modifies the data, the file is atomically re-saved.
func loadFile(path string, migrations []Migration) (map[string]any, error) {
	raw, err := loadRaw(path)
	if err != nil {
		return nil, err
	}

	migrated, err := runMigrations(path, raw, migrations)
	if err != nil {
		return nil, err
	}
	if migrated {
		encoded, mErr := yaml.Marshal(raw)
		if mErr != nil {
			return nil, fmt.Errorf("storage: encoding migrated %s: %w", path, mErr)
		}
		if err := atomicWrite(path, encoded, 0o644); err != nil {
			return nil, fmt.Errorf("storage: saving migrated %s: %w", path, err)
		}
	}

	return raw, nil
}

// loadRaw reads a YAML file into a raw map.
// Returns an empty map for empty files.
func loadRaw(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("storage: reading %s: %w", path, err)
	}

	raw := make(map[string]any)
	if len(data) == 0 {
		return raw, nil
	}

	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("storage: parsing %s: %w", path, err)
	}
	return raw, nil
}

// runMigrations applies each migration function to the raw map.
// Returns true if any migration modified the data.
func runMigrations(path string, raw map[string]any, migrations []Migration) (bool, error) {
	_ = path // reserved for future diagnostics
	changed := false
	for _, m := range migrations {
		if m(raw) {
			changed = true
		}
	}
	return changed, nil
}

// unmarshal converts a raw map to a typed struct T via YAML round-trip.
// Used at the end of construction to produce the typed read API.
func unmarshal[T any](raw map[string]any) (*T, error) {
	encoded, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("storage: re-encoding raw map: %w", err)
	}

	var result T
	if err := yaml.Unmarshal(encoded, &result); err != nil {
		return nil, fmt.Errorf("storage: unmarshalling to struct: %w", err)
	}
	return &result, nil
}
