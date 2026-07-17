package bundle

import (
	"os"
	"path/filepath"
	"sort"
)

// looseComponentDir returns the on-disk directory a loose component of the given
// type and name occupies under a tier base: <base>/<type-dir>/<name>/.
func looseComponentDir(base string, t ComponentType, name string) string {
	return filepath.Join(base, t.Dir(), name)
}

// looseComponent resolves a single loose-tier component by bare name under a
// tier base, if the directory exists. tier must be TierLooseProject or
// TierLooseUser.
func looseComponent(tier Tier, base string, t ComponentType, name string) (Component, bool) {
	if base == "" {
		return Component{}, false
	}
	dir := looseComponentDir(base, t, name)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return Component{}, false
	}
	return Component{
		Address:    BareAddress(name),
		Type:       t,
		FS:         os.DirFS(dir),
		Dir:        dir,
		Provenance: Provenance{Tier: tier, Dir: dir, Bundle: BundleID{Namespace: "", Name: ""}, Shadows: nil},
	}, true
}

// looseNames lists the bare component names of the given type present in a
// tier's convention directory, sorted. A missing base or convention directory
// yields no names (not an error) — a tier simply ships nothing.
func looseNames(base string, t ComponentType) []string {
	if base == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(base, t.Dir()))
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
