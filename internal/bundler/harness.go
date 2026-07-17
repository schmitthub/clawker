package bundler

import (
	"fmt"
	"sort"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

// DefaultHarnessName is the harness used when a command selects none.
const DefaultHarnessName = consts.DefaultHarnessName

// ShippedHarnessNames lists the harness components on the embedded floor.
func ShippedHarnessNames() []string {
	return bundle.FloorNames(bundle.ComponentHarness)
}

// ResolveHarnessName returns the effective harness selection: the explicit
// name when non-empty (validated), else the build.harness selection key
// (highest layer that sets it wins, like build.stacks), else the built-in
// DefaultHarnessName.
func ResolveHarnessName(cfg config.Config, explicit string) (string, error) {
	if explicit != "" {
		if err := ValidateHarnessSelector(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	if proj := cfg.Project(); proj != nil && proj.Build.Harness != "" {
		if err := consts.ValidateHarnessRef(proj.Build.Harness); err != nil {
			return "", fmt.Errorf("build.harness: %w", err)
		}
		return proj.Build.Harness, nil
	}
	return DefaultHarnessName, nil
}

// ValidateHarnessSelector validates a harness selection key. It accepts a bare
// name (embedded floor or a loose convention dir) or a qualified
// namespace.bundle.component address (installed bundle). For a bare name it
// additionally enforces the reserved image-tag alias rule, since a bare harness
// name doubles as its built image's tag; a qualified dotted address can never
// collide with a bare alias, so that check does not apply to it.
func ValidateHarnessSelector(name string) error {
	if err := consts.ValidateHarnessRef(name); err != nil {
		return fmt.Errorf("harness %w", err)
	}
	return nil
}

// KnownHarnessNames lists every harness a build can select: floor, loose
// convention dirs, and installed/in-place bundle harnesses, by their selection
// spelling (bare or dotted), sorted.
func KnownHarnessNames(cfg config.Config) []string {
	comps, _, err := bundle.NewResolver(cfg).List(bundle.ComponentHarness)
	if err != nil {
		// List errors only on a C1 identity collision in the declared bundle
		// set; that error is surfaced on the primary resolve path the caller
		// already hit. This is a best-effort "known harnesses" hint for an error
		// message, so degrade to the always-available floor names rather than
		// emit a second copy of the collision error from a hint helper.
		return ShippedHarnessNames()
	}
	names := make([]string, 0, len(comps))
	for _, c := range comps {
		names = append(names, c.Address.String())
	}
	sort.Strings(names)
	return names
}

// IsKnownHarness reports whether name resolves to a harness on any tier — the
// embedded floor, a loose convention dir, or an installed/in-place bundle.
func IsKnownHarness(cfg config.Config, name string) bool {
	_, err := bundle.NewResolver(cfg).Resolve(bundle.ComponentHarness, name)
	return err == nil
}

// validateOverlayKeys rejects any build.harnesses.<name> overlay whose key
// names no known harness. Such an overlay is dead config — no build could ever
// select it, so its packages/stacks/inject would silently never render into
// any image. Keys (exact selection spelling, bare or qualified) are checked in
// sorted order so the error is deterministic. It resolves through the caller's
// resolver so a generation performs one memoized installed-bundle scan, not
// one per key.
func validateOverlayKeys(cfg config.Config, r *bundle.Resolver) error {
	overlays := cfg.Project().Build.Harnesses
	keys := make([]string, 0, len(overlays))
	for key := range overlays {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := r.Resolve(bundle.ComponentHarness, key); err != nil {
			return fmt.Errorf(
				"build.harnesses.%s: unknown harness %q — no floor, loose, or installed-bundle"+
					" harness resolves to it; known harnesses: %v",
				key, key, KnownHarnessNames(cfg),
			)
		}
	}
	return nil
}

// LoadHarness resolves and loads the named harness bundle through the single
// resolution algorithm (bare = floor/loose, qualified = installed/in-place).
func LoadHarness(cfg config.Config, name string) (*Bundle, error) {
	b, _, err := loadHarnessResolved(bundle.NewResolver(cfg), name)
	return b, err
}

// loadHarnessResolved resolves name to a harness Component through the resolver
// and loads its bundle, returning the component's provenance for build-output
// reporting. The Bundle's Name is the exact selection spelling (bare or dotted
// qualified), which downstream becomes the image tag, the harness label, and
// the per-harness overlay key.
func loadHarnessResolved(r *bundle.Resolver, name string) (*Bundle, bundle.Component, error) {
	comp, err := r.Resolve(bundle.ComponentHarness, name)
	if err != nil {
		return nil, bundle.Component{}, fmt.Errorf("resolve harness %q: %w", name, err)
	}
	b, loadErr := LoadBundle(name, comp.FS)
	if loadErr != nil {
		return nil, bundle.Component{}, loadErr
	}
	return b, comp, nil
}
