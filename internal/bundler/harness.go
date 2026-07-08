package bundler

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/harness"
)

// Shipped harness bundles. Each subdirectory of assets/harnesses is a
// complete bundle (harness.yaml + Dockerfile.harness.tmpl + assets) embedded
// in the binary. Shipped bundles are the virtual base layer of harness
// resolution: they load straight from this embedded FS and are shadowed only
// by a project harnesses: registry entry naming the same key.
//
//go:embed all:assets/harnesses
var harnessesFS embed.FS

const harnessAssetsRoot = "assets/harnesses"

// DefaultHarnessName is the harness used when a command selects none.
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
// when non-empty (validated), else the built-in DefaultHarnessName. Harness
// selection is explicit — a build with no -t builds the default; there is no
// registry default flag.
func ResolveHarnessName(_ config.Config, explicit string) (string, error) {
	if explicit != "" {
		if err := ValidateHarnessKey(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	return DefaultHarnessName, nil
}

// ValidateHarnessKey rejects registry keys that cannot serve as harness
// names — delegates to the unified naming rule shared by stacks, harnesses,
// and their registry/overlay keys, plus the harness-specific reserved-tag-
// alias check (see consts.ValidateHarnessName).
func ValidateHarnessKey(name string) error {
	if err := consts.ValidateHarnessName(name); err != nil {
		return fmt.Errorf("harness %w", err)
	}
	return nil
}

// KnownHarnessNames lists every harness a build can select: the shipped
// bundles plus any project-registered harness (a harnesses.<name>.path entry
// in clawker.yaml), sorted and deduplicated.
func KnownHarnessNames(cfg config.Config) []string {
	set := map[string]struct{}{}
	for _, n := range ShippedHarnessNames() {
		set[n] = struct{}{}
	}
	if p := cfg.Project(); p != nil {
		for name, h := range p.Harnesses {
			if h.Path != "" {
				set[name] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// IsKnownHarness reports whether name resolves to a shipped bundle or a
// project-registered harness.
func IsKnownHarness(cfg config.Config, name string) bool {
	return isShippedHarness(name) || projectHarnessPath(cfg, name) != ""
}

// validateOverlayKeys rejects any build.harnesses.<name> overlay whose key
// names no known harness (shipped or project-registered). Such an overlay is
// dead config — no build could ever select it, so its packages/stacks/inject
// would silently never render into any image. Keys are checked in sorted
// order so the error is deterministic.
func validateOverlayKeys(cfg config.Config) error {
	overlays := cfg.Project().Build.Harnesses
	keys := make([]string, 0, len(overlays))
	for key := range overlays {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !IsKnownHarness(cfg, key) {
			return fmt.Errorf(
				"build.harnesses.%s: unknown harness %q — register its bundle with"+
					" `clawker harness register <path> --name %s`"+
					" (or add harnesses.%s.path to clawker.yaml); known harnesses: %v",
				key, key, key, key, KnownHarnessNames(cfg),
			)
		}
	}
	return nil
}

// harnessProvenance records where a harness bundle resolved from and whether
// it shadowed the shipped bundle of the same name. Unlike stack provenance it
// is ALWAYS surfaced in build output — every harness resolution names its
// source.
type harnessProvenance struct {
	name    string
	source  string // "<path> (project registry)" or "shipped"
	shadows bool   // a project entry shadowing a shipped bundle of the same name
}

// line renders the provenance as a single build-output line.
func (p harnessProvenance) line() string {
	if p.shadows {
		return fmt.Sprintf("harness %s ← %s shadows shipped", p.name, p.source)
	}
	return fmt.Sprintf("harness %s ← %s", p.name, p.source)
}

// projectHarnessPath returns the project harnesses: registry path for name,
// or "" when the project registers no bundle path for it. An empty path on an
// existing entry means the entry only carries per-harness init config, not a
// registration (the config front-door separately rejects an explicitly-empty
// path value).
func projectHarnessPath(cfg config.Config, name string) string {
	if p := cfg.Project(); p != nil {
		return p.Harnesses[name].Path
	}
	return ""
}

// LoadHarness resolves and loads the named harness bundle from its closest
// layer (project registry > shipped embedded).
func LoadHarness(cfg config.Config, name string) (*harness.Bundle, error) {
	b, _, err := loadHarnessResolved(cfg, name)
	return b, err
}

// loadHarnessResolved loads the named harness bundle from the closest layer
// that defines it — project harnesses: registry entry > shipped embedded —
// and returns its provenance (which layer it came from, whether it shadowed a
// shipped bundle) for build-output reporting. An unresolvable name is a hard
// error naming the registration remedy.
func loadHarnessResolved(cfg config.Config, name string) (*harness.Bundle, harnessProvenance, error) {
	if err := ValidateHarnessKey(name); err != nil {
		return nil, harnessProvenance{}, err
	}
	path := projectHarnessPath(cfg, name)
	shipped := isShippedHarness(name)
	switch {
	case path != "":
		dir, err := resolveRegistryPath(cfg, fmt.Sprintf("harness %q", name), path)
		if err != nil {
			return nil, harnessProvenance{}, err
		}
		if _, statErr := os.Stat(filepath.Join(dir, harness.ManifestFile)); statErr != nil {
			return nil, harnessProvenance{}, fmt.Errorf(
				"harness %q: no bundle at registered path %s (%w) — fix harnesses.%s.path in clawker.yaml",
				name, dir, statErr, name,
			)
		}
		b, loadErr := harness.Load(name, os.DirFS(dir))
		if loadErr != nil {
			return nil, harnessProvenance{}, fmt.Errorf("load harness %q from %s: %w", name, dir, loadErr)
		}
		prov := harnessProvenance{
			name:    name,
			source:  fmt.Sprintf("%s (project registry)", path),
			shadows: shipped,
		}
		return b, prov, nil
	case shipped:
		b, loadErr := loadEmbeddedHarness(name)
		if loadErr != nil {
			return nil, harnessProvenance{}, loadErr
		}
		return b, harnessProvenance{name: name, source: sourceShipped, shadows: false}, nil
	default:
		return nil, harnessProvenance{}, fmt.Errorf(
			"harness %q is not registered — register its bundle with `clawker harness register <path> --name %s` (or add harnesses.%s.path to clawker.yaml); shipped harnesses: %v",
			name,
			name,
			name,
			ShippedHarnessNames(),
		)
	}
}

// isShippedHarness reports whether name is one of the embedded bundles.
func isShippedHarness(name string) bool {
	return slices.Contains(ShippedHarnessNames(), name)
}

// loadEmbeddedHarness loads a shipped bundle straight from the embedded
// assets (the virtual base layer).
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
