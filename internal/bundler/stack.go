package bundler

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/template"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/harness"
	"github.com/schmitthub/clawker/internal/stack"
)

// Shipped stack definitions. Each subdirectory of assets/stacks is a
// complete definition (stack.yaml + root and/or user Dockerfile fragments)
// embedded in the binary. Shipped definitions are the virtual base layer of
// stack resolution: they resolve straight from this embedded FS, always, and
// are shadowed only by a matching key at a closer layer (a project stacks:
// registry entry, or a harness bundle's own stacks/ directory).
//
//go:embed all:assets/stacks
var stacksFS embed.FS

const stackAssetsRoot = "assets/stacks"

// sourceBuilt is the provenance label for the embedded layer compiled into
// the binary (the virtual base of both the stack and harness lookup chains).
const sourceBuilt = "built"

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

// isShippedStack reports whether name is one of the embedded definitions.
func isShippedStack(name string) bool {
	return slices.Contains(ShippedStackNames(), name)
}

// loadEmbeddedStack loads a shipped definition straight from the embedded
// assets (the virtual base layer).
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

// stackProvenance records where a declared stack name resolved from and which
// farther layers it shadowed. Emitted in build output only when a closer layer
// shadows a farther one (len(shadows) > 0) — an unshadowed resolution is not
// noteworthy.
type stackProvenance struct {
	name    string   // the declared stack name
	source  string   // the layer it resolved from (e.g. "project (./stacks/node)")
	shadows []string // farther layers that also define name, closest first
}

// line renders the provenance as a single build-output line.
func (p stackProvenance) line() string {
	return fmt.Sprintf("stack %s ← %s shadows %s", p.name, p.source, strings.Join(p.shadows, ", "))
}

// noteworthy reports whether the resolution is worth a build-output line: a
// closer layer shadowed a farther one. Unshadowed resolutions stay silent.
func (p stackProvenance) noteworthy() bool {
	return len(p.shadows) > 0
}

// resolveStack loads one declared stack name from the closest layer that
// defines it. Lineage is scoped by whether a harness bundle is in play:
//
//	base image    (bundle == nil): project stacks: registry > shipped
//	harness image (bundle != nil): project stacks: registry > bundle stacks/ > shipped
//
// A matching key at a closer layer wins WHOLESALE — never merged. When a closer
// layer shadows a farther one, the returned provenance names both; the caller
// decides whether to surface it. An unresolvable name is a hard error naming the
// registration remedy — never a silent skip.
func resolveStack(cfg config.Config, bundle *harness.Bundle, name string) (*stack.Definition, stackProvenance, error) {
	if err := stack.ValidateName(name); err != nil {
		return nil, stackProvenance{}, fmt.Errorf("resolve stack: %w", err)
	}

	projEntry, projHas := projectStackEntry(cfg, name)
	bundleHas := bundle != nil && bundle.HasStack(name)
	shippedHas := isShippedStack(name)

	switch {
	case projHas:
		def, err := loadProjectStack(cfg, name, projEntry)
		if err != nil {
			return nil, stackProvenance{}, err
		}
		prov := stackProvenance{
			name:    name,
			source:  fmt.Sprintf("project (%s)", projEntry.Path),
			shadows: fartherStackLayers(bundle, bundleHas, shippedHas),
		}
		return def, prov, nil
	case bundleHas:
		def, err := bundle.Stack(name)
		if err != nil {
			return nil, stackProvenance{}, fmt.Errorf("resolve stack: %w", err)
		}
		prov := stackProvenance{
			name:    name,
			source:  bundleLabel(bundle),
			shadows: fartherStackLayers(nil, false, shippedHas),
		}
		return def, prov, nil
	case shippedHas:
		def, err := loadEmbeddedStack(name)
		if err != nil {
			return nil, stackProvenance{}, err
		}
		return def, stackProvenance{name: name, source: sourceBuilt, shadows: nil}, nil
	default:
		return nil, stackProvenance{}, fmt.Errorf(
			"%w: %q is declared in clawker.yaml but resolves nowhere — register a definition with `clawker stack register <path> --name %s`, or declare a shipped stack (%v)",
			ErrUnknownStack,
			name,
			name,
			ShippedStackNames(),
		)
	}
}

// fartherStackLayers lists the farther lookup layers that also define a
// declared name — closest first — for shadow provenance. bundle may be nil
// only when bundleHas is false.
func fartherStackLayers(bundle *harness.Bundle, bundleHas, shippedHas bool) []string {
	var layers []string
	if bundleHas {
		layers = append(layers, bundleLabel(bundle))
	}
	if shippedHas {
		layers = append(layers, sourceBuilt)
	}
	return layers
}

// bundleLabel is the provenance phrase for a bundle-embedded stack layer.
func bundleLabel(bundle *harness.Bundle) string {
	return fmt.Sprintf("%s bundle", bundle.Name)
}

// projectStackEntry returns the project stacks: registry entry for name, and
// whether one with a non-empty path exists. An empty path is rejected at the
// config front-door (internal/config validate.go); it is treated as absent
// here only defensively, never as a silent skip of a valid registration.
func projectStackEntry(cfg config.Config, name string) (config.StackRegistryEntry, bool) {
	p := cfg.Project()
	if p == nil {
		return config.StackRegistryEntry{}, false
	}
	entry, ok := p.Stacks[name]
	if !ok || entry.Path == "" {
		return config.StackRegistryEntry{}, false
	}
	return entry, true
}

// loadProjectStack loads a definition through its project stacks: registry
// entry, resolving a relative path against the project root.
func loadProjectStack(cfg config.Config, name string, entry config.StackRegistryEntry) (*stack.Definition, error) {
	dir, err := resolveRegistryPath(cfg, fmt.Sprintf("stack %q", name), entry.Path)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(filepath.Join(dir, stack.ManifestFile)); statErr != nil {
		return nil, fmt.Errorf(
			"stack %q: no definition at registered path %s (%w) — fix stacks.%s.path in clawker.yaml",
			name, dir, statErr, name,
		)
	}
	def, loadErr := stack.Load(name, os.DirFS(dir))
	if loadErr != nil {
		return nil, fmt.Errorf("load stack %q from %s: %w", name, dir, loadErr)
	}
	return def, nil
}

// resolveStackDecls resolves a declaration list against the given bundle
// (nil = base-image lineage), accumulating fragments in declaration order and
// shadow provenance. dupIsError controls duplicate handling: build.stacks
// rejects a repeated name; a harness manifest silently renders it once.
func resolveStackDecls(
	cfg config.Config,
	bundle *harness.Bundle,
	decls []string,
	dupIsError bool,
) ([]namedFragment, []namedFragment, []stackProvenance, error) {
	seen := map[string]bool{}
	var defs []*stack.Definition
	var prov []stackProvenance
	for _, name := range decls {
		if seen[name] {
			if dupIsError {
				return nil, nil, nil, fmt.Errorf("build.stacks: duplicate stack declaration %q", name)
			}
			continue
		}
		seen[name] = true
		def, p, resolveErr := resolveStack(cfg, bundle, name)
		if resolveErr != nil {
			return nil, nil, nil, resolveErr
		}
		defs = append(defs, def)
		if p.noteworthy() {
			prov = append(prov, p)
		}
	}
	root, user := splitFragments(defs)
	return root, user, prov, nil
}

// resolveProjectStacks resolves the project's build.stacks declarations for
// the BASE image. The bundle is deliberately absent: the shared base is
// harness-agnostic, so project declarations resolve from the project registry
// and the shipped set only — a bundle-embedded definition can never leak into
// the base. Returns root-scope and user-scope fragment lists in declaration
// order, plus the provenance of any resolution that shadowed a farther layer.
func resolveProjectStacks(
	cfg config.Config,
	decls []string,
) ([]namedFragment, []namedFragment, []stackProvenance, error) {
	return resolveStackDecls(cfg, nil, decls, true)
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

// resolveHarnessStacks resolves the harness declarations for the HARNESS
// image. Every declared name ALWAYS renders here with its lineage-resolved
// definition (project registry > bundle stacks/ > shipped) — there is no
// cross-stratum dedup against project-declared base stacks. A project that
// also declares the same name in build.stacks gets it in the base too; both
// render, and fragment self-guards / apt idempotence / PATH shadowing own any
// interaction (design §2). Returns root- and user-scope fragments in
// declaration order plus shadow provenance.
func resolveHarnessStacks(
	cfg config.Config,
	bundle *harness.Bundle,
	harnessDecls []string,
) ([]namedFragment, []namedFragment, []stackProvenance, error) {
	return resolveStackDecls(cfg, bundle, harnessDecls, false)
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

// resolveRegistryPath resolves a registry path entry to an absolute path: an
// absolute entry as-is, a relative entry against the project root. A relative
// entry with no project root set (config-dir-only loads) is a hard error —
// resolving it against the process CWD would silently load whatever happens to
// live there.
func resolveRegistryPath(cfg config.Config, key, p string) (string, error) {
	if p == "" || filepath.IsAbs(p) {
		return p, nil
	}
	root := cfg.ProjectRoot()
	if root == "" {
		return "", fmt.Errorf(
			"%s: registry path %q is relative but no project root is resolved — use an absolute path or run inside the project",
			key,
			p,
		)
	}
	return filepath.Join(root, p), nil
}
