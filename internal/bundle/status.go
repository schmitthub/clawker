package bundle

import "sort"

// StatusState classifies one row of the declaration↔cache linkage.
type StatusState int

const (
	// StatusResolving is a declared, loadable bundle (in-place, or the cache
	// entry keyed by a live declaration's value) — its components resolve.
	StatusResolving StatusState = iota
	// StatusNotInstalled is a declared remote source with no cache entry for
	// its exact value; its identity is unknown until `clawker bundle install`
	// fetches it.
	StatusNotInstalled
	// StatusUndeclared is a cache entry no live declaration addresses — inert
	// until re-declared or purged.
	StatusUndeclared
	// StatusUnmanaged is a cache entry without a fetch receipt (hand-placed);
	// it displays no source coordinate.
	StatusUnmanaged
)

// Status is one per-identity (or per-declared-source) row of the
// declaration↔cache linkage, the honest bundle-level view `clawker bundle
// list` renders alongside the component rows.
type Status struct {
	// ID is the bundle identity; zero when a declared source was never fetched
	// (identity comes only from the manifest, which is not yet on disk).
	ID BundleID
	// Source is the canonical source coordinate; empty for an unmanaged entry.
	Source string
	// File is the declaring config file; empty when no declaration is live.
	File string
	// Tier is the resolving tier (in-place or installed); meaningful only for
	// StatusResolving rows.
	Tier Tier
	// Version is the resolving content version; empty when nothing resolves.
	Version string
	// State classifies the row.
	State StatusState
}

// Statuses links the declaration side to the cache side, one row per bundle
// identity or never-fetched declared source: what resolves, what is declared
// but not installed, and what sits in the cache without a live declaration.
// It propagates the resolver's C1 collision error.
func (m *Manager) Statuses() ([]Status, error) {
	bundles, _, err := m.resolver.Bundles()
	if err != nil {
		return nil, err
	}
	root, err := cacheRoot()
	if err != nil {
		return nil, err
	}
	installed, err := scanInstalled(root)
	if err != nil {
		return nil, err
	}
	remote := m.resolver.remoteDeclarations()
	declaredKeys := make(map[string]bool, len(remote))
	for _, d := range remote {
		declaredKeys[d.src.Key()] = true
	}

	rows := resolvingStatuses(bundles)
	rows = append(rows, unresolvableCacheStatuses(installed, declaredKeys)...)
	rows = append(rows, uninstalledSourceStatuses(remote, cachedKeySet(installed))...)

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ID.String() != rows[j].ID.String() {
			return rows[i].ID.String() < rows[j].ID.String()
		}
		return rows[i].Source < rows[j].Source
	})
	return rows, nil
}

// cachedKeySet collects the source keys present in the cache scan.
func cachedKeySet(installed []InstalledEntry) map[string]bool {
	keys := make(map[string]bool, len(installed))
	for _, e := range installed {
		keys[e.Key] = true
	}
	return keys
}

// resolvingStatuses rows the resolved bundle set: in-place declarations and
// declared cached bundles, each with its declaring file and content version.
func resolvingStatuses(bundles map[BundleID]*ResolvedBundle) []Status {
	rows := make([]Status, 0, len(bundles))
	for id, rb := range bundles {
		version := rb.Version
		if version == "" {
			version = rb.Bundle.Manifest.Version
		}
		rows = append(rows, Status{
			ID: id, Source: rb.Source, File: rb.File,
			Tier: rb.Tier, Version: version, State: StatusResolving,
		})
	}
	return rows
}

// unresolvableCacheStatuses rows the cache entries that do NOT resolve —
// entries whose key no live declaration addresses. An entry with a readable
// fetch receipt names its source (undeclared); one without — hand-placed, or
// its receipt unreadable — has no source coordinate to show (unmanaged).
func unresolvableCacheStatuses(
	installed []InstalledEntry,
	declaredKeys map[string]bool,
) []Status {
	var rows []Status
	for _, e := range installed {
		if declaredKeys[e.Key] {
			continue
		}
		// A corrupt receipt degrades the row to unmanaged rather than failing
		// the whole listing — the receipt is display-only.
		receipt, ok, err := readReceipt(e.Root)
		if err != nil {
			ok = false
		}
		if !ok {
			rows = append(rows, Status{
				ID: e.ID, Source: "", File: "", Tier: TierInstalled, Version: "", State: StatusUnmanaged,
			})
			continue
		}
		rows = append(rows, Status{
			ID: e.ID, Source: receipt.Canonical, File: "",
			Tier: TierInstalled, Version: receipt.Version, State: StatusUndeclared,
		})
	}
	return rows
}

// uninstalledSourceStatuses rows the declared remote sources whose exact value
// has no cache entry — installable, identity unknown until fetched.
func uninstalledSourceStatuses(remote []remoteDecl, cachedKeys map[string]bool) []Status {
	var rows []Status
	for _, d := range remote {
		if cachedKeys[d.src.Key()] {
			continue
		}
		rows = append(rows, Status{
			ID: BundleID{Namespace: "", Name: ""}, Source: d.src.Canonical(), File: d.file,
			Tier: TierInstalled, Version: "", State: StatusNotInstalled,
		})
	}
	return rows
}
