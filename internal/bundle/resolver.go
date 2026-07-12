package bundle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

// Resolver resolves component addresses across the three tiers — embedded
// floor, loose local dirs, and installed/in-place bundles — for one loaded
// config. Bare names resolve lazily (user loose > project loose > floor);
// qualified names resolve from bundle content only. The installed/in-place
// bundle set is scanned once and memoized, and bare resolution never triggers
// that scan so a broken bundle declaration cannot block a floor-only build.
type Resolver struct {
	cfg config.Config

	bundlesOnce     sync.Once
	bundles         map[BundleID]*ResolvedBundle
	bundlesWarnings []Warning
	bundlesErr      error
}

// ResolvedBundle is a bundle placed on a tier (in-place or installed) with the
// selected content root already loaded, carrying the declaration that made it
// resolvable.
type ResolvedBundle struct {
	Bundle *Bundle
	Tier   Tier
	// Source is the canonical source coordinate of the declaration.
	Source string
	// File is the config file whose declaration made this bundle resolvable.
	File string
	// Version is the selected cache version directory (empty for in-place).
	Version string
}

// errInvalidComponentType guards the Resolver entry points against a
// ComponentType value outside the defined enum.
var errInvalidComponentType = errors.New("invalid component type")

// NewResolver constructs a resolver bound to cfg.
func NewResolver(cfg config.Config) *Resolver {
	return &Resolver{cfg: cfg} //nolint:exhaustruct // memoization fields deliberately zero until first Bundles() call
}

// userBase is the user-tier loose convention-dir base (<config-dir>).
func (r *Resolver) userBase() string {
	return consts.ConfigDir()
}

// projectBase is the project-tier loose convention-dir base
// (<project-root>/.clawker), or "" when no project root is anchored.
func (r *Resolver) projectBase() string {
	root := r.cfg.ProjectRoot()
	if root == "" {
		return ""
	}
	return filepath.Join(root, consts.DotClawkerDir)
}

// Resolve resolves one component address of the given type to its backing
// Component. A bare name resolves user loose > project loose > floor (stopping
// at the first hit); a qualified namespace.bundle.component address resolves
// from the declared/cached bundle set only.
func (r *Resolver) Resolve(t ComponentType, name string) (Component, error) {
	if !t.Valid() {
		return Component{}, errInvalidComponentType
	}
	addr, err := ParseAddress(name)
	if err != nil {
		return Component{}, err
	}
	if addr.Qualified() {
		return r.resolveQualified(t, addr)
	}
	return r.resolveBare(t, addr.Name)
}

// resolveBare walks the bare tiers in precedence order, stopping at the first
// hit — at most two on-disk stats (user then project) before the embedded
// floor. It reports the winner's provenance without eagerly computing shadows
// (List does that); a bare resolve never scans the bundle set.
func (r *Resolver) resolveBare(t ComponentType, name string) (Component, error) {
	if c, ok := looseComponent(TierLooseUser, r.userBase(), t, name); ok {
		return c, nil
	}
	if c, ok := looseComponent(TierLooseProject, r.projectBase(), t, name); ok {
		return c, nil
	}
	if c, ok := floorComponent(t, name); ok {
		return c, nil
	}
	return Component{}, fmt.Errorf(
		"%s %q not found in any loose convention directory or the built-in floor", t, name)
}

// resolveQualified resolves a qualified address from the declared/cached bundle
// set. A declared-but-uncached bundle yields ErrNotCached; a resolved bundle
// that ships no matching component is a hard error.
func (r *Resolver) resolveQualified(t ComponentType, addr Address) (Component, error) {
	id := addr.ID()
	bundles, _, err := r.Bundles()
	if err != nil {
		return Component{}, err
	}
	rb, ok := bundles[id]
	if !ok {
		return Component{}, fmt.Errorf("%s %q: %w", t, addr, ErrNotCached)
	}
	comp, ok := rb.Bundle.Component(t, addr.Name)
	if !ok {
		return Component{}, fmt.Errorf("bundle %s ships no %s %q", id, t, addr.Name)
	}
	comp.Provenance = Provenance{Tier: rb.Tier, Dir: comp.Dir, Bundle: id, Shadows: nil}
	return comp, nil
}

// Bundles resolves and memoizes the full installed/in-place bundle set keyed by
// identity, returning any C1 identity collisions as a hard error and any
// bundle-load warnings for the command layer to print.
//
// Everything resolvable traces to an explicit declaration. An in-place (local
// path) declaration loads directly from disk; a cached bundle resolves only
// while a live remote declaration matches the source recorded in its
// source.yaml — deleting the `bundles:` entry makes the cached copy inert until
// it is re-declared (no refetch) or purged with `clawker bundle remove`. Two
// sources claiming one identity from different coordinates are a C1 collision —
// hard error naming both, never a silent winner (the author remedies it: drop
// one declaration, `clawker bundle remove` the cached copy, or change the
// namespace).
func (r *Resolver) Bundles() (map[BundleID]*ResolvedBundle, []Warning, error) {
	r.bundlesOnce.Do(func() {
		r.bundles, r.bundlesWarnings, r.bundlesErr = r.scanBundles()
	})
	return r.bundles, r.bundlesWarnings, r.bundlesErr
}

// scanBundles builds the identity→bundle map from in-place declarations (C1
// checked) and the declared subset of the on-disk cache.
func (r *Resolver) scanBundles() (map[BundleID]*ResolvedBundle, []Warning, error) {
	out := map[BundleID]*ResolvedBundle{}
	claims, warnings, err := r.claimInPlaceBundles(out)
	if err != nil {
		return nil, nil, err
	}
	cacheWarnings, err := mergeCachedBundles(out, claims, r.remoteDeclarations())
	if err != nil {
		return nil, nil, err
	}
	warnings = append(warnings, cacheWarnings...)
	return out, warnings, nil
}

// bundleClaim records the canonical source coordinate and declaring file that
// first claimed an identity, so a second in-place declaration of the same
// identity is either an idempotent no-op (same source) or a C1 collision
// (different source).
type bundleClaim struct {
	canonical string
	file      string
}

// claimInPlaceBundles loads every declared LOCAL (in-place) bundle source into
// out, C1-checking identities as it goes. Remote declarations are skipped here —
// they resolve via the cache scan, gated on their declaration matching the
// cache entry's source.yaml (mergeCachedBundles).
func (r *Resolver) claimInPlaceBundles(
	out map[BundleID]*ResolvedBundle,
) (map[BundleID]bundleClaim, []Warning, error) {
	claims := map[BundleID]bundleClaim{}
	var warnings []Warning
	for _, decl := range r.cfg.BundleDeclarations() {
		src := SourceFromConfig(decl.Source)
		if !src.IsLocal() {
			continue
		}
		dir, err := resolveLocalPath(src, decl.File)
		if err != nil {
			return nil, nil, err
		}
		// The claim key is the RESOLVED absolute directory, not the declared
		// spelling — "./vendor/x" in a project layer and the equivalent
		// absolute path in another layer are the same source, not a C1
		// collision.
		canonical := "path:" + dir
		b, loadErr := LoadBundleDir(os.DirFS(dir), dir)
		if loadErr != nil {
			return nil, nil, loadErr
		}
		prev, seen := claims[b.ID]
		if seen && prev.canonical != canonical {
			return nil, nil, &CollisionError{
				Identity:   b.ID,
				AFile:      prev.file,
				BFile:      decl.File,
				ACanonical: prev.canonical,
				BCanonical: canonical,
			}
		}
		if seen {
			continue // idempotent re-declaration
		}
		claims[b.ID] = bundleClaim{canonical: canonical, file: decl.File}
		out[b.ID] = &ResolvedBundle{
			Bundle: b, Tier: TierInPlace, Source: canonical, File: decl.File, Version: "",
		}
		warnings = append(warnings, b.Warnings...)
	}
	return claims, warnings, nil
}

// remoteDeclarations indexes the live REMOTE bundle declarations by canonical
// source coordinate → declaring file (the highest-priority declaring layer
// wins for display). This is the declaration side of the cache gate: a cached
// bundle resolves only while its source.yaml canonical appears here.
func (r *Resolver) remoteDeclarations() map[string]string {
	decls := map[string]string{}
	for _, decl := range r.cfg.BundleDeclarations() {
		src := SourceFromConfig(decl.Source)
		if src.IsLocal() {
			continue
		}
		if _, seen := decls[src.Canonical()]; !seen {
			decls[src.Canonical()] = decl.File
		}
	}
	return decls
}

// mergeCachedBundles folds the DECLARED subset of the on-disk cache into the
// identity map. A cached bundle resolves only while a live declaration matches
// the source recorded in its source.yaml; an undeclared entry is inert (it
// stays on disk until `clawker bundle remove`, and re-declaring the same
// source reactivates it instantly, no refetch). An entry with no source.yaml
// at all (hand-placed) never resolves — there is no declared source it could
// trace to. A declared cache entry whose identity is also claimed by an
// in-place declaration is a C1 collision naming both declaring files — the
// resolver cannot know the two are the same bundle and never silently picks a
// winner.
func mergeCachedBundles(
	out map[BundleID]*ResolvedBundle,
	claims map[BundleID]bundleClaim,
	remoteDecls map[string]string,
) ([]Warning, error) {
	root, err := cacheRoot()
	if err != nil {
		return nil, err
	}
	installed, err := scanInstalled(root)
	if err != nil {
		return nil, err
	}
	var warnings []Warning
	for _, ib := range installed {
		rb, resolvable, entryErr := resolveCachedBundle(ib, claims, remoteDecls)
		if entryErr != nil {
			return nil, entryErr
		}
		if !resolvable {
			continue
		}
		out[ib.ID] = rb
		warnings = append(warnings, rb.Bundle.Warnings...)
	}
	return warnings, nil
}

// resolveCachedBundle applies the declaration gate to one cache entry: a
// hand-placed entry (no source.yaml) or an undeclared source reports
// resolvable=false; a declared entry whose identity an in-place declaration
// already claims is a C1 collision; otherwise the matched source's most
// recently fetched version is loaded.
func resolveCachedBundle(
	ib InstalledBundle,
	claims map[BundleID]bundleClaim,
	remoteDecls map[string]string,
) (*ResolvedBundle, bool, error) {
	meta, ok, err := readSourceMeta(ib.Root)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil // hand-placed: no source record to trace to a declaration
	}
	canonical := meta.source().Canonical()
	declFile, declared := remoteDecls[canonical]
	if !declared {
		return nil, false, nil // undeclared: the cached copy is inert until re-declared
	}
	if claim, claimed := claims[ib.ID]; claimed {
		return nil, false, &CollisionError{
			Identity:   ib.ID,
			AFile:      claim.file,
			BFile:      declFile,
			ACanonical: claim.canonical,
			BCanonical: canonical,
		}
	}
	version := selectVersion(ib, meta)
	versionRoot := ib.versionRoot(version)
	b, loadErr := LoadBundleDir(os.DirFS(versionRoot), versionRoot)
	if loadErr != nil {
		return nil, false, loadErr
	}
	return &ResolvedBundle{
		Bundle: b, Tier: TierInstalled, Source: canonical, File: declFile, Version: version,
	}, true, nil
}

// selectVersion picks the content root to resolve for a declared cached
// bundle: the most recently fetched version per source.yaml. ib.Versions is
// sorted, so a FetchedAt tie settles deterministically on the later directory
// name; an on-disk version the metadata does not record cannot win, but when
// NO version is recorded the last-sorted directory serves as a fallback so a
// metadata gap never makes a cached bundle unresolvable.
func selectVersion(ib InstalledBundle, meta sourceMeta) string {
	selected := ""
	var newest time.Time
	for _, v := range ib.Versions {
		vm, recorded := meta.Versions[v]
		if !recorded {
			continue
		}
		if selected == "" || !vm.FetchedAt.Before(newest) {
			newest = vm.FetchedAt
			selected = v
		}
	}
	if selected == "" {
		return ib.Versions[len(ib.Versions)-1]
	}
	return selected
}

// resolveLocalPath resolves a local in-place source path to an absolute
// directory: an absolute path verbatim, a relative path against the directory
// of the file that declared it — one rule for every layer (a project-layer
// clawker.yaml sits at the project root, so the historical relative-to-root
// behavior is unchanged there). A relative path from a declaration with no
// file of origin is a hard error; declarations always carry their layer file,
// so this only defends against a malformed caller.
func resolveLocalPath(src Source, declFile string) (string, error) {
	if filepath.IsAbs(src.Path) {
		return filepath.Clean(src.Path), nil
	}
	if declFile == "" {
		return "", &SourceError{
			Source: src,
			Err:    fmt.Errorf("relative bundle path %q has no declaring file to resolve against", src.Path),
		}
	}
	return filepath.Join(filepath.Dir(declFile), src.Path), nil
}

// List enumerates every resolvable component of the given type across all tiers,
// with provenance and shadow markers. Bare-tier components merge across
// user/project/floor with the winner carrying the shadowed farther tiers;
// qualified bundle components are listed with their bundle provenance. It is
// eager — every tier is read — and returns the bundle-load warnings for the
// command layer to print.
func (r *Resolver) List(t ComponentType) ([]Component, []Warning, error) {
	if !t.Valid() {
		return nil, nil, errInvalidComponentType
	}
	components := r.listBare(t)

	bundles, warnings, err := r.Bundles()
	if err != nil {
		return nil, nil, err
	}
	components = append(components, r.listQualified(t, bundles)...)
	return components, warnings, nil
}

// listBare merges the bare tiers (user > project > floor) into one component per
// name, the winner carrying its shadowed farther tiers.
func (r *Resolver) listBare(t ComponentType) []Component {
	order, byName := r.bareCandidates(t)
	sort.Strings(order)
	out := make([]Component, 0, len(order))
	for _, name := range order {
		candidates := byName[name]
		winner := candidates[0]
		for _, shadowed := range candidates[1:] {
			winner.Provenance.Shadows = append(winner.Provenance.Shadows, shadowed.Provenance)
		}
		out = append(out, winner)
	}
	return out
}

// bareCandidates gathers every bare-tier resolution per name, in precedence
// order (user > project > floor), returning the insertion-ordered name list and
// the per-name candidate slices.
func (r *Resolver) bareCandidates(t ComponentType) ([]string, map[string][]Component) {
	order := []string{}
	byName := map[string][]Component{}
	add := func(c Component) {
		if _, seen := byName[c.Address.Name]; !seen {
			order = append(order, c.Address.Name)
		}
		byName[c.Address.Name] = append(byName[c.Address.Name], c)
	}
	for _, name := range looseNames(r.userBase(), t) {
		if c, ok := looseComponent(TierLooseUser, r.userBase(), t, name); ok {
			add(c)
		}
	}
	for _, name := range looseNames(r.projectBase(), t) {
		if c, ok := looseComponent(TierLooseProject, r.projectBase(), t, name); ok {
			add(c)
		}
	}
	for _, name := range FloorNames(t) {
		if c, ok := floorComponent(t, name); ok {
			add(c)
		}
	}
	return order, byName
}

// listQualified lists the qualified components of the given type across the
// resolved bundle set, sorted by address.
func (r *Resolver) listQualified(t ComponentType, bundles map[BundleID]*ResolvedBundle) []Component {
	var out []Component
	for id, rb := range bundles {
		for _, c := range rb.Bundle.Components {
			if c.Type != t {
				continue
			}
			c.Provenance = Provenance{Tier: rb.Tier, Dir: c.Dir, Bundle: id, Shadows: nil}
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Address.String() < out[j].Address.String()
	})
	return out
}
