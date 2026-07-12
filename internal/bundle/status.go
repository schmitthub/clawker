package bundle

import "sort"

// StatusState classifies one row of the declaration↔cache linkage.
type StatusState int

const (
	// StatusResolving is a declared, loadable bundle (in-place, or a cached
	// entry whose declaration is live) — its components resolve.
	StatusResolving StatusState = iota
	// StatusNotInstalled is a declared remote source with no cache entry; its
	// identity is unknown until `clawker bundle install` fetches it.
	StatusNotInstalled
	// StatusUndeclared is a cached bundle whose recorded source no live
	// declaration matches — inert until re-declared or purged.
	StatusUndeclared
	// StatusUnmanaged is a cached bundle without source metadata (hand-placed);
	// it traces to no declaration and never resolves.
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

	rows := resolvingStatuses(bundles)
	cachedRows, matchedCanonicals, err := unresolvableCacheStatuses(installed, remote)
	if err != nil {
		return nil, err
	}
	rows = append(rows, cachedRows...)
	rows = append(rows, uninstalledSourceStatuses(remote, matchedCanonicals)...)

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ID.String() != rows[j].ID.String() {
			return rows[i].ID.String() < rows[j].ID.String()
		}
		return rows[i].Source < rows[j].Source
	})
	return rows, nil
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
// undeclared and hand-placed (unmanaged) — and reports which declaration
// canonicals matched a cache entry so declared-but-uncached sources can be
// derived by exclusion.
func unresolvableCacheStatuses(
	installed []InstalledBundle,
	remote []remoteDecl,
) ([]Status, map[string]bool, error) {
	var rows []Status
	matchedCanonicals := map[string]bool{}
	for _, ib := range installed {
		meta, ok, err := readSourceMeta(ib.Root)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			rows = append(rows, Status{
				ID: ib.ID, Source: "", File: "", Tier: TierInstalled, Version: "", State: StatusUnmanaged,
			})
			continue
		}
		declared := false
		for _, d := range remote {
			if _, matched := matchVersion(ib, meta, d.src); matched {
				matchedCanonicals[d.src.Canonical()] = true
				declared = true
			}
		}
		if !declared {
			rows = append(rows, Status{
				ID: ib.ID, Source: meta.source().Canonical(), File: "",
				Tier: TierInstalled, Version: "", State: StatusUndeclared,
			})
		}
	}
	return rows, matchedCanonicals, nil
}

// uninstalledSourceStatuses rows the declared remote sources matching no cache
// entry — installable, identity unknown until fetched.
func uninstalledSourceStatuses(remote []remoteDecl, matchedCanonicals map[string]bool) []Status {
	var rows []Status
	for _, d := range remote {
		canonical := d.src.Canonical()
		if matchedCanonicals[canonical] {
			continue
		}
		rows = append(rows, Status{
			ID: BundleID{Namespace: "", Name: ""}, Source: canonical, File: d.file,
			Tier: TierInstalled, Version: "", State: StatusNotInstalled,
		})
	}
	return rows
}
