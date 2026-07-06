package bundler

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/template"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/harness"
	"github.com/schmitthub/clawker/internal/toolchain"
)

// Shipped toolchain definitions. Each subdirectory of assets/toolchains is a
// complete definition (toolchain.yaml + Dockerfile.toolchain.tmpl) that is
// materialized into the user config dir, where it is user-owned and
// editable. The embedded copy is only a seed and a hermetic fallback — the
// materialized definition always wins when present.
//
//go:embed all:assets/toolchains
var toolchainsFS embed.FS

const toolchainAssetsRoot = "assets/toolchains"

// ShippedToolchainNames lists the definitions embedded in this build.
func ShippedToolchainNames() []string {
	entries, err := toolchainsFS.ReadDir(toolchainAssetsRoot)
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

// ShippedToolchainDefaultDir is the materialization destination seeded into
// the registry for a shipped toolchain. It is NOT a resolution fallback —
// lookup always goes through the registry entry's explicit path.
func ShippedToolchainDefaultDir(name string) string {
	return filepath.Join(consts.ConfigDir(), toolchain.ToolchainsSubdir, name)
}

// EnsureToolchains makes the shipped toolchains usable and visible: every
// shipped definition is materialized into the user config dir
// (copy-if-missing — user edits are never clobbered; definitions whose
// registry entry points elsewhere land there instead) and the settings
// registry gains an entry per shipped toolchain. The registry is the
// customization surface — seeding it is what makes the shipped set
// discoverable and editable.
func EnsureToolchains(cfg config.Config) error {
	for _, name := range ShippedToolchainNames() {
		src, err := fs.Sub(toolchainsFS, toolchainAssetsRoot+"/"+name)
		if err != nil {
			return fmt.Errorf("shipped toolchain %q: %w", name, err)
		}
		dir := ShippedToolchainDefaultDir(name)
		if s := cfg.Settings(); s != nil {
			if tc, ok := s.Toolchains[name]; ok && tc.Path != "" {
				dir = tc.Path
			}
		}
		if matErr := harness.Materialize(src, dir); matErr != nil {
			return fmt.Errorf("materialize toolchain %q: %w", name, matErr)
		}
	}
	return ensureToolchainRegistry(cfg)
}

// ensureToolchainRegistry seeds a settings registry entry for every shipped
// toolchain that has none, with the same copy-if-missing contract as the
// files: an existing entry is never touched. No-op when nothing is missing.
func ensureToolchainRegistry(cfg config.Config) error {
	s := cfg.Settings()
	if s == nil {
		return nil
	}
	reg := make(map[string]config.ToolchainSettings, len(s.Toolchains))
	maps.Copy(reg, s.Toolchains)
	changed := false
	for _, name := range ShippedToolchainNames() {
		if tc, ok := reg[name]; ok {
			// Every entry carries an explicit definition path; heal an entry
			// that predates the requirement. Non-empty paths (user
			// relocations) are never touched.
			if tc.Path == "" {
				tc.Path = ShippedToolchainDefaultDir(name)
				reg[name] = tc
				changed = true
			}
			continue
		}
		reg[name] = config.ToolchainSettings{Path: ShippedToolchainDefaultDir(name)}
		changed = true
	}
	if !changed {
		return nil
	}
	store := cfg.SettingsStore()
	if err := store.Set("toolchains", reg); err != nil {
		return fmt.Errorf("registering shipped toolchains in settings: %w", err)
	}
	if err := store.Write(); err != nil {
		return fmt.Errorf("writing settings toolchain registry: %w", err)
	}
	return nil
}

// isShippedToolchain reports whether name is one of the embedded definitions.
func isShippedToolchain(name string) bool {
	return slices.Contains(ShippedToolchainNames(), name)
}

// loadEmbeddedToolchain loads a shipped definition straight from the
// embedded assets.
func loadEmbeddedToolchain(name string) (*toolchain.Definition, error) {
	src, err := fs.Sub(toolchainsFS, toolchainAssetsRoot+"/"+name)
	if err != nil {
		return nil, fmt.Errorf("shipped toolchain %q: %w", name, err)
	}
	def, loadErr := toolchain.Load(name, src)
	if loadErr != nil {
		return nil, fmt.Errorf("load shipped toolchain %q: %w", name, loadErr)
	}
	return def, nil
}

// resolveToolchain loads one declared toolchain name from its unique source.
// All sources share one flat namespace: a name available both from the
// selected harness bundle AND from the registry/shipped set is a collision
// error — explicit over clever; bundle authors prefix bespoke definitions.
func resolveToolchain(cfg config.Config, bundle *harness.Bundle, name string) (*toolchain.Definition, error) {
	if err := toolchain.ValidateName(name); err != nil {
		return nil, fmt.Errorf("resolve toolchain: %w", err)
	}

	bundleHas := bundle != nil && bundle.HasToolchain(name)
	var regEntry config.ToolchainSettings
	regHas := false
	if s := cfg.Settings(); s != nil {
		regEntry, regHas = s.Toolchains[name]
	}

	if bundleHas {
		if regHas || isShippedToolchain(name) {
			return nil, toolchainCollisionError(bundle.Name, name, regEntry, regHas)
		}
		def, err := bundle.Toolchain(name)
		if err != nil {
			return nil, fmt.Errorf("resolve toolchain: %w", err)
		}
		return def, nil
	}
	if regHas {
		return loadRegisteredToolchain(name, regEntry)
	}
	// Bootstrap seam: no registry entry yet (fresh boot before the
	// build-time ensure has seeded it, or a hermetic test config) — shipped
	// definitions load from the embedded copy.
	if isShippedToolchain(name) {
		return loadEmbeddedToolchain(name)
	}
	return nil, fmt.Errorf(
		"%w: %q (known: shipped %v, settings toolchains registry, or a definition embedded in the selected harness bundle)",
		ErrUnknownToolchain,
		name,
		ShippedToolchainNames(),
	)
}

// toolchainCollisionError names both definitions claiming a flat-namespace
// toolchain name.
func toolchainCollisionError(bundleName, name string, regEntry config.ToolchainSettings, regHas bool) error {
	other := "the shipped clawker definition"
	if regHas {
		other = fmt.Sprintf("the registered definition at %s (settings toolchains.%s.path)", regEntry.Path, name)
	}
	return fmt.Errorf(
		"toolchain %q is defined both by harness bundle %q and by %s — toolchain names share one namespace; rename the bundle-embedded definition",
		name,
		bundleName,
		other,
	)
}

// resolveProjectToolchains resolves the project's build.toolchains
// declarations for the BASE image. The bundle is deliberately absent: the
// shared base is harness-agnostic, so project declarations resolve from the
// shipped set and the settings registry only — a bundle-embedded definition
// can never leak into the base. Returns root-scope and user-scope
// fragment lists in declaration order.
func resolveProjectToolchains(
	cfg config.Config,
	decls []string,
) ([]namedFragment, []namedFragment, error) {
	seen := map[string]bool{}
	var defs []*toolchain.Definition
	for _, name := range decls {
		if seen[name] {
			return nil, nil, fmt.Errorf("build.toolchains: duplicate toolchain declaration %q", name)
		}
		seen[name] = true
		def, resolveErr := resolveToolchain(cfg, nil, name)
		if resolveErr != nil {
			return nil, nil, resolveErr
		}
		defs = append(defs, def)
	}
	root, user := splitFragments(defs)
	return root, user, nil
}

// namedFragment is one fragment of a definition, tagged with the
// definition's name for error context.
type namedFragment struct {
	name     string
	fragment string
}

// splitFragments partitions definitions' fragments into root- and
// user-scope lists, preserving declaration order. A definition contributes
// to either or both lists depending on which fragments it ships.
func splitFragments(defs []*toolchain.Definition) ([]namedFragment, []namedFragment) {
	var root, user []namedFragment
	for _, def := range defs {
		if def.RootFragment != "" {
			root = append(root, namedFragment{name: def.Name, fragment: def.RootFragment})
		}
		if def.UserFragment != "" {
			user = append(user, namedFragment{name: def.Name, fragment: def.UserFragment})
		}
	}
	return root, user
}

// resolveHarnessToolchains resolves the bundle manifest's declarations for
// the HARNESS image: every harness-declared name the project did not
// already declare (earliest stage wins — a project-declared toolchain is in
// the base the harness image builds FROM). Project-declared names are still
// collision-checked against the bundle's embedded definitions so a bundle
// can never silently shadow the definition the base actually used.
func resolveHarnessToolchains(
	cfg config.Config,
	bundle *harness.Bundle,
	projectDecls, harnessDecls []string,
) ([]namedFragment, []namedFragment, error) {
	projectSet := map[string]bool{}
	for _, name := range projectDecls {
		projectSet[name] = true
		if err := checkBundleShadow(cfg, bundle, name); err != nil {
			return nil, nil, err
		}
	}
	seen := map[string]bool{}
	var defs []*toolchain.Definition
	for _, name := range harnessDecls {
		if projectSet[name] || seen[name] {
			continue
		}
		seen[name] = true
		def, resolveErr := resolveToolchain(cfg, bundle, name)
		if resolveErr != nil {
			return nil, nil, resolveErr
		}
		defs = append(defs, def)
	}
	root, user := splitFragments(defs)
	return root, user, nil
}

// checkBundleShadow errors when the selected bundle embeds a definition for
// a project-declared name — the base already resolved that name from the
// shipped/registry set, and a bundle must never silently shadow it.
func checkBundleShadow(cfg config.Config, bundle *harness.Bundle, name string) error {
	if bundle == nil || !bundle.HasToolchain(name) {
		return nil
	}
	var regEntry config.ToolchainSettings
	regHas := false
	if s := cfg.Settings(); s != nil {
		regEntry, regHas = s.Toolchains[name]
	}
	return toolchainCollisionError(bundle.Name, name, regEntry, regHas)
}

// renderToolchainSteps executes each fragment against the Dockerfile
// context, yielding the strings the template anchors splice in.
func renderToolchainSteps(fragments []namedFragment, tctx *DockerfileContext) ([]string, error) {
	var steps []string
	for _, f := range fragments {
		tmpl, err := template.New(f.name).Parse(f.fragment)
		if err != nil {
			return nil, fmt.Errorf("toolchain %q: parse fragment: %w", f.name, err)
		}
		var buf bytes.Buffer
		if execErr := tmpl.Execute(&buf, tctx); execErr != nil {
			return nil, fmt.Errorf("toolchain %q: render fragment: %w", f.name, execErr)
		}
		steps = append(steps, strings.TrimRight(buf.String(), "\n"))
	}
	return steps, nil
}

// loadRegisteredToolchain loads a definition through its settings registry
// entry's explicit path.
func loadRegisteredToolchain(name string, regEntry config.ToolchainSettings) (*toolchain.Definition, error) {
	if regEntry.Path == "" {
		return nil, fmt.Errorf(
			"toolchain %q registry entry has no path (settings toolchains.%s.path)",
			name, name,
		)
	}
	if _, statErr := os.Stat(filepath.Join(regEntry.Path, toolchain.ManifestFile)); statErr != nil {
		return nil, fmt.Errorf(
			"toolchain %q: no definition at registered path %s (%w) — fix toolchains.%s.path in settings or rebuild to re-materialize",
			name,
			regEntry.Path,
			statErr,
			name,
		)
	}
	def, loadErr := toolchain.Load(name, os.DirFS(regEntry.Path))
	if loadErr != nil {
		return nil, fmt.Errorf("load toolchain %q from %s: %w", name, regEntry.Path, loadErr)
	}
	return def, nil
}
