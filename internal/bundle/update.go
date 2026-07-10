package bundle

import (
	"context"
	"fmt"
)

// UpdateOutcome classifies what an update attempt did to one cached bundle.
type UpdateOutcome int

const (
	// UpdateSkippedPinned means a sha-pinned source was left untouched.
	UpdateSkippedPinned UpdateOutcome = iota
	// UpdateSkippedUnmanaged means the cached bundle carries no source metadata
	// or no movable ref (a hand-placed cache entry), so there is nothing to
	// compare an update against.
	UpdateSkippedUnmanaged
	// UpdateUnchanged means the ref still resolves to the cached commit.
	UpdateUnchanged
	// UpdateRefetched means the ref moved and a new version was fetched.
	UpdateRefetched
	// UpdateFailed means the refetch/resolve failed; the cache still serves.
	UpdateFailed
)

// UpdateResult is the per-bundle outcome of an update pass.
type UpdateResult struct {
	ID         BundleID
	Outcome    UpdateOutcome
	NewVersion string
	Err        error
}

// Update refetches cached bundles whose ref-based source has moved. A non-zero
// id updates only that bundle; a zero id updates every cached bundle. A
// sha-pinned source is skipped; a resolve/refetch failure is reported per bundle
// and never purges the cache. It returns one result per bundle considered; the
// top-level error is only for a cache-enumeration failure.
func (m *Manager) Update(ctx context.Context, id BundleID) ([]UpdateResult, error) {
	root, err := cacheRoot()
	if err != nil {
		return nil, err
	}
	if !id.zero() {
		ib, ok, ibErr := installedBundle(root, id)
		if ibErr != nil {
			return nil, ibErr
		}
		if !ok {
			return nil, fmt.Errorf("bundle %s: %w", id, ErrNotCached)
		}
		return []UpdateResult{m.updateOne(ctx, ib)}, nil
	}

	installed, err := scanInstalled(root)
	if err != nil {
		return nil, err
	}
	results := make([]UpdateResult, 0, len(installed))
	for _, ib := range installed {
		results = append(results, m.updateOne(ctx, ib))
	}
	return results, nil
}

// updateOne resolves a cached bundle's ref and refetches it when the ref has
// moved since the last fetch. A sha-pinned source is a skip; a resolve or fetch
// error is captured on the result, leaving the cache intact.
func (m *Manager) updateOne(ctx context.Context, ib InstalledBundle) UpdateResult {
	meta, ok, err := readSourceMeta(ib.Root)
	if err != nil {
		return UpdateResult{ID: ib.ID, Outcome: UpdateFailed, NewVersion: "", Err: err}
	}
	src := meta.source()
	switch {
	case meta.pinned():
		return UpdateResult{ID: ib.ID, Outcome: UpdateSkippedPinned, NewVersion: "", Err: nil}
	case !ok || src.Ref == "":
		return UpdateResult{ID: ib.ID, Outcome: UpdateSkippedUnmanaged, NewVersion: "", Err: nil}
	}

	newSHA, err := m.fetcher.ResolveRef(ctx, src.URL, src.Ref)
	if err != nil {
		return UpdateResult{
			ID: ib.ID, Outcome: UpdateFailed, NewVersion: "",
			Err: &SourceError{Source: src, Err: err},
		}
	}
	if newSHA == meta.latestVersionSHA() {
		return UpdateResult{ID: ib.ID, Outcome: UpdateUnchanged, NewVersion: "", Err: nil}
	}

	_, version, err := m.fetchIntoCache(ctx, src)
	if err != nil {
		return UpdateResult{ID: ib.ID, Outcome: UpdateFailed, NewVersion: "", Err: err}
	}
	return UpdateResult{ID: ib.ID, Outcome: UpdateRefetched, NewVersion: version, Err: nil}
}

// AutoUpdateCheck refetches opt-in ref-based bundles whose source has moved,
// fired at the start of bundle-consuming commands. It NEVER errors and NEVER
// blocks: every problem (a resolve failure, a refetch failure) becomes a Warning
// and the cached version keeps serving. Only bundles whose declaration opted in
// (auto_update: true) and whose source is a movable ref are considered.
func (m *Manager) AutoUpdateCheck(ctx context.Context) []Warning {
	optedIn := m.autoUpdateCanonicals()
	if len(optedIn) == 0 {
		return nil
	}
	root, err := cacheRoot()
	if err != nil {
		return []Warning{{Message: fmt.Sprintf("bundle auto-update skipped: %v", err)}}
	}
	installed, err := scanInstalled(root)
	if err != nil {
		return []Warning{{Message: fmt.Sprintf("bundle auto-update skipped: %v", err)}}
	}

	var warnings []Warning
	for _, ib := range installed {
		meta, ok, metaErr := readSourceMeta(ib.Root)
		if metaErr != nil || !ok || !optedIn[meta.source().Canonical()] {
			continue
		}
		if w, emit := autoUpdateWarning(m.updateOne(ctx, ib)); emit {
			warnings = append(warnings, w)
		}
	}
	return warnings
}

// autoUpdateCanonicals collects the canonical source keys of every declared
// bundle that opted into auto-update and is a movable ref (not sha-pinned, not
// local).
func (m *Manager) autoUpdateCanonicals() map[string]bool {
	optedIn := map[string]bool{}
	for _, decl := range m.cfg.BundleDeclarations() {
		if !decl.Source.AutoUpdate {
			continue
		}
		src := SourceFromConfig(decl.Source)
		if src.IsLocal() || src.SHA != "" || src.Ref == "" {
			continue
		}
		optedIn[src.Canonical()] = true
	}
	return optedIn
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
	case UpdateSkippedPinned, UpdateSkippedUnmanaged, UpdateUnchanged:
		return Warning{}, false
	default:
		return Warning{}, false
	}
}
