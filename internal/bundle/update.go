package bundle

import (
	"context"
	"fmt"
)

// UpdateOutcome classifies what an update attempt did to one declared bundle
// source.
type UpdateOutcome int

const (
	// UpdateSkippedPinned means a sha-pinned source was left untouched.
	UpdateSkippedPinned UpdateOutcome = iota
	// UpdateSkippedNotInstalled means the declared source has no cache entry
	// for its value — installing, not updating, is the remedy.
	UpdateSkippedNotInstalled
	// UpdateUnchanged means the tracked tip still resolves to the cached commit.
	UpdateUnchanged
	// UpdateRefetched means the tip moved and the entry was refetched in place.
	UpdateRefetched
	// UpdateFailed means the refetch/resolve failed; the cache still serves.
	UpdateFailed
)

// UpdateResult is the per-source outcome of an update pass.
type UpdateResult struct {
	// ID is the cached identity; zero for a declared source that was never
	// fetched (identity comes only from the manifest).
	ID BundleID
	// Source is the canonical declared source coordinate.
	Source     string
	Outcome    UpdateOutcome
	NewVersion string
	Err        error
}

// Subject is the display label for a result row: the cached identity when
// known, else the declared source coordinate.
func (r UpdateResult) Subject() string {
	if r.ID.zero() {
		return r.Source
	}
	return r.ID.String()
}

// Update refetches declared bundle sources whose tracked tip — a ref, or the
// remote's default branch for an unpinned source — has moved. The pass is
// declaration-driven: each remote declaration is compared against its own
// value-keyed cache entry, so an update can never touch content another
// declaration addresses. A non-zero id updates only the declarations whose
// cached identity matches; a zero id updates every declared source. A
// sha-pinned source is skipped; a resolve/refetch failure is reported per
// source and never purges the cache. The top-level error is only for a
// cache-enumeration failure, or an id that matches no cached entry.
func (m *Manager) Update(ctx context.Context, id BundleID) ([]UpdateResult, error) {
	root, err := cacheRoot()
	if err != nil {
		return nil, err
	}
	installed, err := scanInstalled(root)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]InstalledEntry, len(installed))
	for _, e := range installed {
		byKey[e.Key] = e
	}

	var results []UpdateResult
	for _, d := range m.resolver.remoteDeclarations() {
		entry, cached := byKey[d.src.Key()]
		if !id.zero() && (!cached || entry.ID != id) {
			continue
		}
		results = append(results, m.updateOne(ctx, d.src, entry, cached))
	}
	if !id.zero() && len(results) == 0 {
		return nil, fmt.Errorf(
			"bundle %s has no declared source to update — declare it in a `bundles:` entry first", id)
	}
	return results, nil
}

// updateOne resolves one declared source's tracked tip — its ref, or the
// remote's default branch for an unpinned source — and refetches its cache
// entry in place when the tip has moved since the last fetch. A sha-pinned
// source is a skip; a resolve or fetch error is captured on the result,
// leaving the cache intact.
func (m *Manager) updateOne(ctx context.Context, src Source, entry InstalledEntry, cached bool) UpdateResult {
	result := UpdateResult{
		ID: entry.ID, Source: src.Canonical(), Outcome: UpdateFailed, NewVersion: "", Err: nil,
	}
	switch {
	case !cached:
		result.Outcome = UpdateSkippedNotInstalled
		return result
	case src.SHA != "":
		result.Outcome = UpdateSkippedPinned
		return result
	}

	newSHA, err := m.fetcher.ResolveRef(ctx, src.URL, src.Ref)
	if err != nil {
		result.Err = &SourceError{Source: src, Err: err}
		return result
	}
	// A missing or corrupt receipt just means there is nothing to compare —
	// refetch rather than fail (the receipt is display/compare-only).
	receipt, ok, err := readReceipt(entry.Root)
	if err == nil && ok && newSHA == receipt.SHA {
		result.Outcome = UpdateUnchanged
		return result
	}

	_, version, err := m.fetchIntoCache(ctx, src)
	if err != nil {
		result.Err = err
		return result
	}
	result.Outcome = UpdateRefetched
	result.NewVersion = version
	return result
}

// AutoUpdateCheck refetches opt-in bundles whose tracked source has moved —
// a ref, or the remote's default branch for an unpinned source — fired at the
// start of bundle-consuming commands. It NEVER errors and NEVER blocks: every
// problem (a resolve failure, a refetch failure) becomes a Warning and the
// cached version keeps serving. Only declarations that opted in
// (auto_update: true), are movable (not sha-pinned, not local), and have a
// cache entry to compare are considered.
func (m *Manager) AutoUpdateCheck(ctx context.Context) []Warning {
	root, err := cacheRoot()
	if err != nil {
		return []Warning{{Message: fmt.Sprintf("bundle auto-update skipped: %v", err)}}
	}
	installed, err := scanInstalled(root)
	if err != nil {
		return []Warning{{Message: fmt.Sprintf("bundle auto-update skipped: %v", err)}}
	}
	byKey := make(map[string]InstalledEntry, len(installed))
	for _, e := range installed {
		byKey[e.Key] = e
	}

	var warnings []Warning
	for _, src := range m.autoUpdateSources() {
		entry, cached := byKey[src.Key()]
		if !cached {
			continue
		}
		if w, emit := autoUpdateWarning(m.updateOne(ctx, src, entry, true)); emit {
			warnings = append(warnings, w)
		}
	}
	return warnings
}

// autoUpdateSources lists the declared sources eligible for auto-update —
// opted in (auto_update: true) and movable (not sha-pinned, not local) —
// deduplicated by value key.
func (m *Manager) autoUpdateSources() []Source {
	var sources []Source
	seen := map[string]bool{}
	for _, decl := range m.cfg.BundleDeclarations() {
		if !decl.Source.AutoUpdate {
			continue
		}
		src := SourceFromConfig(decl.Source)
		if src.IsLocal() || src.SHA != "" || seen[src.Key()] {
			continue
		}
		seen[src.Key()] = true
		sources = append(sources, src)
	}
	return sources
}

// autoUpdateWarning renders the advisory for an auto-update outcome: a refetch or
// a failure warns; an unchanged or pinned bundle is silent.
func autoUpdateWarning(r UpdateResult) (Warning, bool) {
	switch r.Outcome {
	case UpdateRefetched:
		return Warning{Message: fmt.Sprintf("bundle %s auto-updated to version %s", r.ID, r.NewVersion)}, true
	case UpdateFailed:
		return Warning{Message: fmt.Sprintf(
			"bundle %s auto-update failed (%v); using the cached version", r.ID, r.Err)}, true
	case UpdateSkippedPinned, UpdateSkippedNotInstalled, UpdateUnchanged:
		return Warning{}, false
	default:
		return Warning{}, false
	}
}
