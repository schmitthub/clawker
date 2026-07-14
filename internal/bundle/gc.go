package bundle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/schmitthub/clawker/internal/config"
)

// errNoRootsProvider is returned by Prune when the Manager was constructed
// without WithRegisteredRoots — without the registry the roots union is
// incomplete, and an incomplete union must never collect.
var errNoRootsProvider = errors.New(
	"bundle cache pruning requires the registered-project roots provider")

// rootedSource is one declared remote source value rooting cache entries,
// with every config file that declares it.
type rootedSource struct {
	src   Source
	files []string
}

// PruneDrop is one cache entry the GC removed: an entry whose exact declared
// value no registered project (nor the user layer) declares anymore.
type PruneDrop struct {
	// ID is the bundle identity the entry belonged to.
	ID BundleID
	// Key is the value-keyed entry directory name that was removed.
	Key string
	// Source is the canonical source value the entry was fetched from (from
	// its receipt).
	Source string
}

// RepositoryRoot is one repository coordinate rooting an identity, with the
// config files declaring a source from it.
type RepositoryRoot struct {
	// Repository is the pin-stripped source coordinate (clone URL, plus the
	// subdir for a monorepo bundle).
	Repository string
	// Files are the declaring config files.
	Files []string
}

// IdentitySources flags one bundle identity whose surviving cache entries are
// rooted from two or more DISTINCT repositories across projects — legitimate
// during a fork migration or an ssh↔https transition, but also exactly what a
// cross-project mirror attack looks like (a look-alike repository shipping the
// same namespace.name), so prune surfaces it for the operator to judge.
type IdentitySources struct {
	ID    BundleID
	Repos []RepositoryRoot
}

// PruneReport is the outcome of a full-cache prune sweep.
type PruneReport struct {
	// Drops are the removed entries.
	Drops []PruneDrop
	// MultiSource lists identities rooted from ≥2 distinct repositories.
	MultiSource []IdentitySources
}

// collectRoots unions the declared remote bundle-source values that count as
// GC roots, keyed by their cache key: the current config's declarations (its
// walk-up layers plus the user layer) and every registered project root's
// declarations (via config.BundleDeclarationsAt). The union is exact by
// construction — resolution is registry-based, so an unregistered checkout
// cannot resolve project-layer bundles and contributes no liveness the union
// could miss. Any unreadable input is an error: roots must be computable
// before anything is collected.
func (m *Manager) collectRoots(ctx context.Context) (map[string]*rootedSource, error) {
	if m.registeredRoots == nil {
		return nil, errNoRootsProvider
	}
	roots := map[string]*rootedSource{}
	addRootDecls(roots, m.cfg.BundleDeclarations())
	dirs, err := m.registeredRoots(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing registered project roots: %w", err)
	}
	for _, dir := range dirs {
		decls, dErr := config.BundleDeclarationsAt(dir)
		if dErr != nil {
			return nil, fmt.Errorf("collecting bundle GC roots: %w", dErr)
		}
		addRootDecls(roots, decls)
	}
	return roots, nil
}

// addRootDecls merges one declaration set into the roots union, keyed by cache
// key, accumulating the declaring files per value.
func addRootDecls(roots map[string]*rootedSource, decls []config.BundleDeclaration) {
	for _, d := range decls {
		src := SourceFromConfig(d.Source)
		if src.IsLocal() {
			continue
		}
		key := src.Key()
		r := roots[key]
		if r == nil {
			r = &rootedSource{src: src, files: nil}
			roots[key] = r
		}
		if !slices.Contains(r.files, d.File) {
			r.files = append(r.files, d.File)
		}
	}
}

// Prune sweeps the whole cache against the exact declaration roots: every
// managed entry whose value key no root addresses is removed, and every
// identity left rooted from ≥2 distinct repositories is reported for operator
// judgment. Hand-placed entries (no fetch receipt) are never collected — they
// are not refetchable, so they are not "just cache".
func (m *Manager) Prune(ctx context.Context) (PruneReport, error) {
	roots, err := m.collectRoots(ctx)
	if err != nil {
		return PruneReport{}, err
	}
	cacheDir, err := cacheRoot()
	if err != nil {
		return PruneReport{}, err
	}
	entries, err := scanInstalled(cacheDir)
	if err != nil {
		return PruneReport{}, err
	}
	drops, err := gcEntries(ctx, cacheDir, entries, roots)
	if err != nil {
		return PruneReport{Drops: drops, MultiSource: nil}, err
	}
	return PruneReport{Drops: drops, MultiSource: multiSourceIdentities(entries, roots)}, nil
}

// AutoGC reconciles the given identities' cache entries against the exact
// declaration roots — the opportunistic sweep piggybacking on install and
// update, so an edited declaration's stranded old entry does not sit in the
// cache forever. It never blocks the operation it rides on: every outcome
// (a removed entry, an incomputable root set) is an advisory Warning. On a
// Manager without a roots provider it is a silent no-op (GC is off).
func (m *Manager) AutoGC(ctx context.Context, ids ...BundleID) []Warning {
	if m.registeredRoots == nil || len(ids) == 0 {
		return nil
	}
	roots, err := m.collectRoots(ctx)
	if err != nil {
		return []Warning{{Message: fmt.Sprintf("bundle cache maintenance skipped: %v", err)}}
	}
	cacheDir, err := cacheRoot()
	if err != nil {
		return []Warning{{Message: fmt.Sprintf("bundle cache maintenance skipped: %v", err)}}
	}
	var warnings []Warning
	for _, id := range dedupIDs(ids) {
		entries, scanErr := identityEntries(cacheDir, id)
		if scanErr != nil {
			warnings = append(warnings, Warning{Message: fmt.Sprintf(
				"bundle cache maintenance skipped for %s: %v", id, scanErr)})
			continue
		}
		drops, gcErr := gcEntries(ctx, cacheDir, entries, roots)
		for _, d := range drops {
			warnings = append(warnings, Warning{Message: fmt.Sprintf(
				"removed stale cache entry of %s: key %s (%s)", d.ID, d.Key, d.Source)})
		}
		if gcErr != nil {
			warnings = append(warnings, Warning{Message: fmt.Sprintf(
				"bundle cache maintenance for %s incomplete: %v", id, gcErr)})
		}
	}
	return warnings
}

// dedupIDs returns the unique identities in first-seen order.
func dedupIDs(ids []BundleID) []BundleID {
	var out []BundleID
	seen := map[BundleID]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// identityEntries scans one identity's value-keyed cache entries; a
// never-cached identity yields none.
func identityEntries(cacheDir string, id BundleID) ([]InstalledEntry, error) {
	if _, err := os.Stat(filepath.Join(cacheDir, id.Namespace, id.Name)); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat bundle cache %s: %w", id, err)
	}
	return scanBundleName(cacheDir, id.Namespace, id.Name)
}

// gcEntries removes every entry whose value key no root addresses. An entry
// without a readable receipt is skipped — GC only collects what a declaration
// once fetched and a declaration could fetch again. Removal serializes on the
// per-entry lock the install pipeline uses; emptied identity and namespace
// directories are cleaned up behind the last removed entry.
func gcEntries(
	ctx context.Context, cacheDir string, entries []InstalledEntry, roots map[string]*rootedSource,
) ([]PruneDrop, error) {
	var drops []PruneDrop
	for _, e := range entries {
		if roots[e.Key] != nil {
			continue
		}
		receipt, ok, err := readReceipt(e.Root)
		if err != nil || !ok {
			// Hand-placed or receipt unreadable: not provably refetchable, so
			// never collected (the unmanaged `bundle list` hint covers it).
			continue
		}
		if rmErr := removeEntry(ctx, e); rmErr != nil {
			return drops, rmErr
		}
		drops = append(drops, PruneDrop{ID: e.ID, Key: e.Key, Source: receipt.Canonical})
		cleanEmptyIdentityDirs(cacheDir, e.ID)
	}
	return drops, nil
}

// removeEntry deletes one cache entry under the same per-entry advisory lock
// the install pipeline commits under, then drops the lock file itself. A
// concurrent install of the same value implies the value is declared — such an
// entry is rooted and never reaches here — so the lock only serializes against
// in-flight writes of an entry already condemned. The subsequent
// cleanEmptyIdentityDirs can still race a concurrent install of a SIBLING key
// (shared ns/name parent, no identity-level lock); the install commit retries
// once on a vanished parent to absorb exactly that window.
func removeEntry(ctx context.Context, e InstalledEntry) error {
	err := withBundleLock(ctx, e.Root, func() error {
		if rmErr := os.RemoveAll(e.Root); rmErr != nil {
			return fmt.Errorf("remove cache entry %s/%s: %w", e.ID, e.Key, rmErr)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Lock-file removal failure is unactionable: a leftover .lock is a file,
	// invisible to the dir-only cache scan, and reused on any future write.
	if rmErr := os.Remove(e.Root + lockSuffix); rmErr != nil {
		_ = rmErr
	}
	return nil
}

// cleanEmptyIdentityDirs removes the identity directory, then the namespace
// directory, when the last entry beneath them is gone. [os.Remove] refuses a
// non-empty directory, which is exactly the guard needed — a failure here
// means siblings remain (or a concurrent write landed) and is not an error.
func cleanEmptyIdentityDirs(cacheDir string, id BundleID) {
	if err := os.Remove(filepath.Join(cacheDir, id.Namespace, id.Name)); err != nil {
		return
	}
	// Namespace-dir removal failure just means other bundles share the
	// namespace; nothing to act on.
	if err := os.Remove(filepath.Join(cacheDir, id.Namespace)); err != nil {
		_ = err
	}
}

// multiSourceThreshold is the repository count at which an identity's rooting
// becomes an anomaly worth reporting: two distinct repositories.
const multiSourceThreshold = 2

// multiSourceIdentities groups the surviving rooted entries by identity and
// reports every identity rooted from ≥2 distinct repositories.
func multiSourceIdentities(entries []InstalledEntry, roots map[string]*rootedSource) []IdentitySources {
	reposByID := repoFilesByIdentity(entries, roots)
	var out []IdentitySources
	for id, repos := range reposByID {
		if len(repos) < multiSourceThreshold {
			continue
		}
		is := IdentitySources{ID: id, Repos: nil}
		for repo, files := range repos {
			sort.Strings(files)
			is.Repos = append(is.Repos, RepositoryRoot{Repository: repo, Files: files})
		}
		sort.Slice(is.Repos, func(i, j int) bool { return is.Repos[i].Repository < is.Repos[j].Repository })
		out = append(out, is)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out
}

// repoFilesByIdentity maps each cached identity to its rooting repositories
// and, per repository, the declaring config files.
func repoFilesByIdentity(
	entries []InstalledEntry, roots map[string]*rootedSource,
) map[BundleID]map[string][]string {
	reposByID := map[BundleID]map[string][]string{}
	for _, e := range entries {
		r := roots[e.Key]
		if r == nil {
			continue
		}
		repo := r.src.repository()
		if reposByID[e.ID] == nil {
			reposByID[e.ID] = map[string][]string{}
		}
		for _, f := range r.files {
			if !slices.Contains(reposByID[e.ID][repo], f) {
				reposByID[e.ID][repo] = append(reposByID[e.ID][repo], f)
			}
		}
	}
	return reposByID
}
