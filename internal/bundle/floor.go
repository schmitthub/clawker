package bundle

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
)

// floorFS holds the embedded floor: the shipped harnesses, stacks, and
// monitoring extensions baked into the binary as bare-named components. The
// tree is structurally identical to a loose convention-dir tier —
// assets/{harnesses,stacks,monitoring}/<name>/ — with no bundle wrapper, so
// floor resolution shares the exact enumeration path as every other tier.
//
//go:embed all:assets
var floorFS embed.FS

// floorAssetsRoot is the embed root inside floorFS.
const floorAssetsRoot = "assets"

// FloorNames lists the bare component names of the given type shipped in this
// build's embedded floor, sorted. It is the floor analog of a convention-dir
// ReadDir.
func FloorNames(t ComponentType) []string {
	entries, err := floorFS.ReadDir(path.Join(floorAssetsRoot, t.Dir()))
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

// FloorFS returns a sub-filesystem rooted at the named floor component's
// directory, ready to hand to the type's loader (bundler.LoadBundle /
// LoadStackDefinition / LoadMonitoringUnit). It errors when the floor ships no
// such component.
func FloorFS(t ComponentType, name string) (fs.FS, error) {
	dir := path.Join(floorAssetsRoot, t.Dir(), name)
	sub, err := fs.Sub(floorFS, dir)
	if err != nil {
		return nil, fmt.Errorf("floor %s %q: %w", t, name, err)
	}
	// fs.Sub does not verify existence; confirm the component directory is
	// present so a missing floor component is a clean error, not a lazy failure
	// at first read.
	if _, statErr := fs.Stat(floorFS, dir); statErr != nil {
		return nil, fmt.Errorf("floor %s %q: %w", t, name, statErr)
	}
	return sub, nil
}

// floorComponent builds the Component for a floor-tier resolution, if present.
func floorComponent(t ComponentType, name string) (Component, bool) {
	dir := path.Join(floorAssetsRoot, t.Dir(), name)
	sub, err := FloorFS(t, name)
	if err != nil {
		return Component{}, false
	}
	return Component{
		Address:    BareAddress(name),
		Type:       t,
		FS:         sub,
		Dir:        dir,
		Provenance: Provenance{Tier: TierFloor, Dir: dir, Bundle: BundleID{Namespace: "", Name: ""}, Shadows: nil},
	}, true
}
