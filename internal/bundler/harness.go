package bundler

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/harness"
)

// Shipped harness bundles. Each subdirectory of assets/harnesses is a
// complete bundle (harness.yaml + Dockerfile.harness.tmpl + assets) that is
// materialized into the user config dir, where it is user-owned and
// editable. The embedded copy is only a seed and a hermetic fallback — the
// materialized bundle always wins when present.
//
//go:embed all:assets/harnesses
var harnessesFS embed.FS

const harnessAssetsRoot = "assets/harnesses"

// DefaultHarnessName is the harness used when no settings registry entry
// sets default: true.
const DefaultHarnessName = consts.DefaultHarnessName

// ShippedHarnessNames lists the bundles embedded in this build.
func ShippedHarnessNames() []string {
	entries, err := harnessesFS.ReadDir(harnessAssetsRoot)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// ResolveHarnessName returns the effective harness name: the explicit name
// when non-empty, else the single registry entry marked default: true, else
// DefaultHarnessName. More than one entry marked default is a configuration
// error.
func ResolveHarnessName(cfg config.Config, explicit string) (string, error) {
	if explicit != "" {
		if err := ValidateHarnessKey(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	name, err := registryDefaultHarness(cfg)
	if err != nil {
		return "", err
	}
	if name == "" {
		return DefaultHarnessName, nil
	}
	if keyErr := ValidateHarnessKey(name); keyErr != nil {
		return "", keyErr
	}
	return name, nil
}

// registryDefaultHarness returns the single registry entry flagged default,
// empty when none is flagged, and an error when more than one is.
func registryDefaultHarness(cfg config.Config) (string, error) {
	s := cfg.Settings()
	if s == nil {
		return "", nil
	}
	names := make([]string, 0, len(s.Harnesses))
	for name, h := range s.Harnesses {
		if h.Default {
			names = append(names, name)
		}
	}
	if len(names) > 1 {
		sort.Strings(names)
		return "", fmt.Errorf(
			"multiple harnesses marked default in settings: %s — set default: true on exactly one",
			strings.Join(names, ", "),
		)
	}
	if len(names) == 0 {
		return "", nil
	}
	return names[0], nil
}

// harnessKeyRe is the docker image tag grammar — the registry key IS the
// image tag, so keys are validated against it.
var harnessKeyRe = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)

// ValidateHarnessKey rejects registry keys that cannot serve as image tags
// or that collide with the reserved tag aliases.
func ValidateHarnessKey(name string) error {
	switch name {
	case consts.ImageTagDefaultAlias, consts.ImageTagLatest, consts.ImageTagBase:
		return fmt.Errorf("harness name %q is reserved as an image tag alias", name)
	}
	if !harnessKeyRe.MatchString(name) {
		return fmt.Errorf(
			"harness name %q is not a valid image tag (must match %s)",
			name, harnessKeyRe.String(),
		)
	}
	return nil
}

// HarnessBundleDir returns the on-disk bundle directory for name: the
// registry entry's path when configured, else the conventional
// <config-dir>/harnesses/<name>.
func HarnessBundleDir(cfg config.Config, name string) (string, error) {
	s := cfg.Settings()
	if s == nil {
		return "", fmt.Errorf("harness %q: settings unavailable, cannot resolve bundle path", name)
	}
	h, ok := s.Harnesses[name]
	if !ok {
		return "", fmt.Errorf(
			"harness %q is not registered: add a settings entry harnesses.%s with an explicit path to its bundle directory",
			name,
			name,
		)
	}
	if h.Path == "" {
		return "", fmt.Errorf(
			"harness %q registry entry has no bundle path: every harness entry carries an explicit path (settings harnesses.%s.path)",
			name,
			name,
		)
	}
	return h.Path, nil
}

// ShippedBundleDefaultDir is the materialization destination seeded into the
// registry for a shipped harness. It is NOT a resolution fallback — lookup
// always goes through the registry entry's explicit path.
func ShippedBundleDefaultDir(name string) string {
	return filepath.Join(consts.ConfigDir(), harness.HarnessesSubdir, name)
}

// EnsureHarnesses makes the shipped harnesses usable and visible: every
// shipped bundle is materialized into the user config dir (copy-if-missing —
// user edits are never clobbered; bundles whose registry entry points
// elsewhere land there instead) and the settings registry gains an entry per
// shipped harness. The registry is the customization surface — seeding it is
// what makes the shipped set discoverable and editable.
func EnsureHarnesses(cfg config.Config) error {
	for _, name := range ShippedHarnessNames() {
		src, err := fs.Sub(harnessesFS, harnessAssetsRoot+"/"+name)
		if err != nil {
			return fmt.Errorf("shipped harness %q: %w", name, err)
		}
		// Materialize where the registry points when the user relocated the
		// bundle; the seeded default otherwise. Registry seeding below
		// records the explicit path either way.
		dir := ShippedBundleDefaultDir(name)
		if s := cfg.Settings(); s != nil {
			if h, ok := s.Harnesses[name]; ok && h.Path != "" {
				dir = h.Path
			}
		}
		if matErr := harness.Materialize(src, dir); matErr != nil {
			return fmt.Errorf("materialize harness %q: %w", name, matErr)
		}
	}
	return ensureHarnessRegistry(cfg)
}

// ensureHarnessRegistry seeds a settings registry entry for every shipped
// harness that has none, with the same copy-if-missing contract as bundle
// files: an existing entry is never touched, so a user who unset a default
// flag or relocated a path keeps that edit. The built-in default harness is
// marked default: true only when its entry is being created and no other
// entry already holds the flag. No-op when nothing is missing.
func ensureHarnessRegistry(cfg config.Config) error {
	s := cfg.Settings()
	if s == nil {
		return nil
	}
	reg, changed := seedShippedEntries(s.Harnesses)
	if !changed {
		return nil
	}
	store := cfg.SettingsStore()
	if err := store.Set("harnesses", reg); err != nil {
		return fmt.Errorf("registering shipped harnesses in settings: %w", err)
	}
	if err := store.Write(); err != nil {
		return fmt.Errorf("writing settings harness registry: %w", err)
	}
	return nil
}

// seedShippedEntries returns a copy of existing with an entry added for each
// shipped harness that lacks one, and whether anything was added. The
// built-in default harness takes default: true only when created fresh with
// no other entry holding the flag.
func seedShippedEntries(existing map[string]config.HarnessSettings) (map[string]config.HarnessSettings, bool) {
	reg := make(map[string]config.HarnessSettings, len(existing))
	hasDefault := false
	for name, h := range existing {
		reg[name] = h
		if h.Default {
			hasDefault = true
		}
	}
	changed := false
	for _, name := range ShippedHarnessNames() {
		if h, ok := reg[name]; ok {
			// Every entry carries an explicit bundle path; heal a shipped
			// entry that predates the requirement. Non-empty paths (user
			// relocations) are never touched.
			if h.Path == "" {
				h.Path = ShippedBundleDefaultDir(name)
				reg[name] = h
				changed = true
			}
			continue
		}
		entry := config.HarnessSettings{Default: false, Path: ShippedBundleDefaultDir(name)}
		if !hasDefault && name == DefaultHarnessName {
			entry.Default = true
			hasDefault = true
		}
		reg[name] = entry
		changed = true
	}
	return reg, changed
}

// LoadHarness resolves and loads the named harness bundle. The materialized
// (user-owned) bundle directory wins when present; a shipped bundle that has
// not been materialized yet falls back to the embedded copy so rendering
// works without touching the filesystem (tests, fresh installs).
func LoadHarness(cfg config.Config, name string) (*harness.Bundle, error) {
	// Bootstrap seam: with NO registry at all (fresh boot before the settings
	// migration/build-time ensure has seeded it, or a hermetic test config),
	// shipped bundles load from the embedded copy. Once any registry exists,
	// resolution is registry-only — every entry carries an explicit path.
	s := cfg.Settings()
	if (s == nil || len(s.Harnesses) == 0) && isShippedHarness(name) {
		return loadEmbeddedHarness(name)
	}

	dir, err := HarnessBundleDir(cfg, name)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(filepath.Join(dir, harness.ManifestFile)); statErr != nil {
		return nil, fmt.Errorf(
			"harness %q: no bundle at registered path %s (%w) — fix harnesses.%s.path in settings or rebuild to re-materialize",
			name,
			dir,
			statErr,
			name,
		)
	}
	b, loadErr := harness.Load(name, os.DirFS(dir))
	if loadErr != nil {
		return nil, fmt.Errorf("load harness %q from %s: %w", name, dir, loadErr)
	}
	return b, nil
}

// isShippedHarness reports whether name is one of the embedded bundles.
func isShippedHarness(name string) bool {
	return slices.Contains(ShippedHarnessNames(), name)
}

// loadEmbeddedHarness loads a shipped bundle straight from the embedded
// assets.
func loadEmbeddedHarness(name string) (*harness.Bundle, error) {
	src, err := fs.Sub(harnessesFS, harnessAssetsRoot+"/"+name)
	if err != nil {
		return nil, fmt.Errorf("shipped harness %q: %w", name, err)
	}
	b, loadErr := harness.Load(name, src)
	if loadErr != nil {
		return nil, fmt.Errorf("load shipped harness %q: %w", name, loadErr)
	}
	return b, nil
}
