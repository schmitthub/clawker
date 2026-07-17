package bundle

// InventoryItem is one row of the per-type component inventory rendered by the
// read-only listing commands (`clawker stack list`, `clawker harness list`,
// `clawker monitor extensions`): the component's resolvable spelling plus the
// provenance needed to trace it back to its source for cleanup.
type InventoryItem struct {
	// Name is the spelling that selects the component: bare for floor/loose
	// components, qualified namespace.bundle.component for a bundle component.
	Name string
	// Version is the owning bundle's version (manifest version, falling back to
	// the selected cache version); empty for a bare component.
	Version string
	// Bundle is the owning bundle identity; zero for floor/loose components.
	Bundle BundleID
	// Provenance records the resolution tier and any shadowed farther tiers.
	Provenance Provenance
}

// Inventory lists every resolvable component of one type across all three
// tiers, joined with its owning bundle's version. It propagates the resolver's
// C1 collision error and returns the bundle-load advisories.
func (m *Manager) Inventory(t ComponentType) ([]InventoryItem, []Warning, error) {
	components, warnings, err := m.resolver.List(t)
	if err != nil {
		return nil, nil, err
	}
	// Memoized by List's own scan — no second disk walk.
	bundles, _, err := m.resolver.Bundles()
	if err != nil {
		return nil, nil, err
	}
	items := make([]InventoryItem, 0, len(components))
	for _, c := range components {
		items = append(items, InventoryItem{
			Name:       c.Address.String(),
			Version:    owningBundleVersion(c, bundles),
			Bundle:     c.Provenance.Bundle,
			Provenance: c.Provenance,
		})
	}
	return items, warnings, nil
}

// owningBundleVersion returns the owning bundle's version for a qualified
// component — the manifest version, falling back to the selected cache version
// (the resolved sha for an unversioned bundle) — or "" for a bare component.
func owningBundleVersion(c Component, bundles map[BundleID]*ResolvedBundle) string {
	if !c.Address.Qualified() {
		return ""
	}
	rb, ok := bundles[c.Provenance.Bundle]
	if !ok {
		return ""
	}
	if rb.Bundle.Manifest.Version != "" {
		return rb.Bundle.Manifest.Version
	}
	return rb.Version
}
