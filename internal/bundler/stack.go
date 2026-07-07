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
	"github.com/schmitthub/clawker/internal/stack"
)

// Shipped stack definitions. Each subdirectory of assets/stacks is a
// complete definition (stack.yaml + root and/or user Dockerfile
// fragments) that is
// materialized into the user config dir, where it is user-owned and
// editable. The embedded copy is only a seed and a hermetic fallback — the
// materialized definition always wins when present.
//
//go:embed all:assets/stacks
var stacksFS embed.FS

const stackAssetsRoot = "assets/stacks"

// ShippedStackNames lists the definitions embedded in this build.
func ShippedStackNames() []string {
	entries, err := stacksFS.ReadDir(stackAssetsRoot)
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

// ShippedStackDefaultDir is the materialization destination seeded into
// the registry for a shipped stack. It is NOT a resolution fallback —
// lookup always goes through the registry entry's explicit path.
func ShippedStackDefaultDir(name string) string {
	return filepath.Join(consts.ConfigDir(), stack.StacksSubdir, name)
}

// EnsureStacks makes the shipped stacks usable and visible: every
// shipped definition is materialized into the user config dir
// (copy-if-missing — user edits are never clobbered; definitions whose
// registry entry points elsewhere land there instead) and the settings
// registry gains an entry per shipped stack. The registry is the
// customization surface — seeding it is what makes the shipped set
// discoverable and editable.
//
// The returned warnings name every materialized copy of a shipped definition
// whose stamp no longer matches the embedded tree (or that predates the
// stamp) — same contract as EnsureHarnesses: never auto-overwritten, the user
// deletes the directory to refresh.
func EnsureStacks(cfg config.Config) ([]string, error) {
	warnings, err := ensureShippedCopies(
		shippedKindStack,
		ShippedStackNames(),
		func(name string) (fs.FS, error) {
			return fs.Sub(stacksFS, stackAssetsRoot+"/"+name)
		},
		func(name string) string {
			if s := cfg.Settings(); s != nil {
				if tc, ok := s.Stacks[name]; ok && tc.Path != "" {
					return tc.Path
				}
			}
			return ShippedStackDefaultDir(name)
		},
	)
	if err != nil {
		return warnings, err
	}
	return warnings, ensureStackRegistry(cfg)
}

// ensureStackRegistry seeds a settings registry entry for every shipped
// stack that has none, with the same copy-if-missing contract as the
// files: an existing entry is never touched. No-op when nothing is missing.
func ensureStackRegistry(cfg config.Config) error {
	s := cfg.Settings()
	if s == nil {
		return nil
	}
	reg := make(map[string]config.StackSettings, len(s.Stacks))
	maps.Copy(reg, s.Stacks)
	changed := false
	for _, name := range ShippedStackNames() {
		if tc, ok := reg[name]; ok {
			// Every entry carries an explicit definition path; heal an entry
			// that predates the requirement. Non-empty paths (user
			// relocations) are never touched.
			if tc.Path == "" {
				tc.Path = ShippedStackDefaultDir(name)
				reg[name] = tc
				changed = true
			}
			continue
		}
		reg[name] = config.StackSettings{Path: ShippedStackDefaultDir(name)}
		changed = true
	}
	if !changed {
		return nil
	}
	store := cfg.SettingsStore()
	if err := store.Set("stacks", reg); err != nil {
		return fmt.Errorf("registering shipped stacks in settings: %w", err)
	}
	if err := store.Write(); err != nil {
		return fmt.Errorf("writing settings stack registry: %w", err)
	}
	return nil
}

// isShippedStack reports whether name is one of the embedded definitions.
func isShippedStack(name string) bool {
	return slices.Contains(ShippedStackNames(), name)
}

// loadEmbeddedStack loads a shipped definition straight from the
// embedded assets.
func loadEmbeddedStack(name string) (*stack.Definition, error) {
	src, err := fs.Sub(stacksFS, stackAssetsRoot+"/"+name)
	if err != nil {
		return nil, fmt.Errorf("shipped stack %q: %w", name, err)
	}
	def, loadErr := stack.Load(name, src)
	if loadErr != nil {
		return nil, fmt.Errorf("load shipped stack %q: %w", name, loadErr)
	}
	return def, nil
}

// resolveStack loads one declared stack name from its unique source.
// All sources share one flat namespace: a name available both from the
// selected harness bundle AND from the registry/shipped set is a collision
// error — explicit over clever; bundle authors prefix bespoke definitions.
func resolveStack(cfg config.Config, bundle *harness.Bundle, name string) (*stack.Definition, error) {
	if err := stack.ValidateName(name); err != nil {
		return nil, fmt.Errorf("resolve stack: %w", err)
	}

	bundleHas := bundle != nil && bundle.HasStack(name)
	var regEntry config.StackSettings
	regHas := false
	if s := cfg.Settings(); s != nil {
		regEntry, regHas = s.Stacks[name]
	}

	if bundleHas {
		if regHas || isShippedStack(name) {
			return nil, stackCollisionError(bundle.Name, name, regEntry, regHas)
		}
		def, err := bundle.Stack(name)
		if err != nil {
			return nil, fmt.Errorf("resolve stack: %w", err)
		}
		return def, nil
	}
	if regHas {
		return loadRegisteredStack(name, regEntry)
	}
	// Bootstrap seam: no registry entry yet (fresh boot before the
	// build-time ensure has seeded it, or a hermetic test config) — shipped
	// definitions load from the embedded copy.
	if isShippedStack(name) {
		return loadEmbeddedStack(name)
	}
	return nil, fmt.Errorf(
		"%w: %q (known: shipped %v, settings stacks registry, or a definition embedded in the selected harness bundle)",
		ErrUnknownStack,
		name,
		ShippedStackNames(),
	)
}

// stackCollisionError names both definitions claiming a flat-namespace
// stack name.
func stackCollisionError(bundleName, name string, regEntry config.StackSettings, regHas bool) error {
	other := "the shipped clawker definition"
	if regHas {
		other = fmt.Sprintf("the registered definition at %s (settings stacks.%s.path)", regEntry.Path, name)
	}
	return fmt.Errorf(
		"stack %q is defined both by harness bundle %q and by %s — stack names share one namespace; rename the bundle-embedded definition",
		name,
		bundleName,
		other,
	)
}

// resolveProjectStacks resolves the project's build.stacks
// declarations for the BASE image. The bundle is deliberately absent: the
// shared base is harness-agnostic, so project declarations resolve from the
// shipped set and the settings registry only — a bundle-embedded definition
// can never leak into the base. Returns root-scope and user-scope
// fragment lists in declaration order.
func resolveProjectStacks(
	cfg config.Config,
	decls []string,
) ([]namedFragment, []namedFragment, error) {
	seen := map[string]bool{}
	var defs []*stack.Definition
	for _, name := range decls {
		if seen[name] {
			return nil, nil, fmt.Errorf("build.stacks: duplicate stack declaration %q", name)
		}
		seen[name] = true
		def, resolveErr := resolveStack(cfg, nil, name)
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
func splitFragments(defs []*stack.Definition) ([]namedFragment, []namedFragment) {
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

// resolveHarnessStacks resolves the bundle manifest's declarations for
// the HARNESS image: every harness-declared name the project did not
// already declare (earliest stage wins — a project-declared stack is in
// the base the harness image builds FROM). Project-declared names are still
// collision-checked against the bundle's embedded definitions so a bundle
// can never silently shadow the definition the base actually used.
func resolveHarnessStacks(
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
	var defs []*stack.Definition
	for _, name := range harnessDecls {
		if projectSet[name] || seen[name] {
			continue
		}
		seen[name] = true
		def, resolveErr := resolveStack(cfg, bundle, name)
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
	if bundle == nil || !bundle.HasStack(name) {
		return nil
	}
	var regEntry config.StackSettings
	regHas := false
	if s := cfg.Settings(); s != nil {
		regEntry, regHas = s.Stacks[name]
	}
	return stackCollisionError(bundle.Name, name, regEntry, regHas)
}

// renderStackSteps executes each fragment against the Dockerfile
// context, yielding the strings the template anchors splice in.
func renderStackSteps(fragments []namedFragment, tctx *DockerfileContext) ([]string, error) {
	var steps []string
	for _, f := range fragments {
		tmpl, err := template.New(f.name).Parse(f.fragment)
		if err != nil {
			return nil, fmt.Errorf("stack %q: parse fragment: %w", f.name, err)
		}
		var buf bytes.Buffer
		if execErr := tmpl.Execute(&buf, tctx); execErr != nil {
			return nil, fmt.Errorf("stack %q: render fragment: %w", f.name, execErr)
		}
		steps = append(steps, strings.TrimRight(buf.String(), "\n"))
	}
	return steps, nil
}

// loadRegisteredStack loads a definition through its settings registry
// entry's explicit path.
func loadRegisteredStack(name string, regEntry config.StackSettings) (*stack.Definition, error) {
	if regEntry.Path == "" {
		return nil, fmt.Errorf(
			"stack %q registry entry has no path (settings stacks.%s.path)",
			name, name,
		)
	}
	if _, statErr := os.Stat(filepath.Join(regEntry.Path, stack.ManifestFile)); statErr != nil {
		return nil, fmt.Errorf(
			"stack %q: no definition at registered path %s (%w) — fix stacks.%s.path in settings or rebuild to re-materialize",
			name,
			regEntry.Path,
			statErr,
			name,
		)
	}
	def, loadErr := stack.Load(name, os.DirFS(regEntry.Path))
	if loadErr != nil {
		return nil, fmt.Errorf("load stack %q from %s: %w", name, regEntry.Path, loadErr)
	}
	return def, nil
}
