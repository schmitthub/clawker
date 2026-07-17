package bundle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

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
	// its receipt); empty when the receipt existed but could not be read.
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
	// Warnings are the sweep's advisories: staging trees reclaimed or
	// restored, and condemned entries whose fetch receipt could not be read.
	Warnings []Warning
}

// stagingSweepAge is the age past which a tree under the staging dir is
// presumed abandoned by a crashed install rather than in use by a live one —
// comfortably above any plausible install duration, so the sweep never
// reclaims a running install's clone/content/retire trees.
const stagingSweepAge = 24 * time.Hour

// collectRoots unions the declared remote bundle-source values that count as
// GC roots, keyed by their cache key: the current config's declarations (its
// walk-up layers plus the user layer) and every registered project root's
// declarations (config.BundleDeclarationsAt, which probes EVERY directory
// under the root — walk-up discovery makes any directory between a working
// directory and the project root a declaring layer, so nested layers root
// entries too). An unregistered checkout cannot resolve project-layer bundles,
// so it contributes no liveness; the residual gaps are the loader's documented
// walk bounds (dot-directories and symlinked subtrees are not descended into)
// — a layer hidden there is not counted, and a wrong collect self-heals with
// one refetch. Any unreadable input is an error: roots must be computable
// before anything is collected.
//
// The union is a SNAPSHOT: a declaration written after this returns (a
// concurrent `bundle install` of a new source) is invisible to the sweep that
// consumes it, and the per-root tree walks make the window wider than a
// single file read — see removeEntry for the consequence.
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

// Prune sweeps the whole cache against the exact declaration roots: abandoned
// staging trees are reclaimed first (a retired entry whose slot is still empty
// is restored — see sweepStaging), then every managed entry whose value key no
// root addresses is removed, as is every rooted duplicate a fresher fetch of
// the same value superseded (an upstream identity rename leaves one — see
// entriesByKey), and every identity left rooted from ≥2 distinct repositories
// is reported for operator judgment. Hand-placed entries (no fetch receipt at
// all) are never collected — they are not refetchable, so they are not "just
// cache"; an entry whose receipt exists but cannot be read WAS fetched and is
// collected when condemned, with the broken receipt surfaced as a warning.
func (m *Manager) Prune(ctx context.Context) (PruneReport, error) {
	roots, err := m.collectRoots(ctx)
	if err != nil {
		return PruneReport{}, err
	}
	cacheDir, err := cacheRoot()
	if err != nil {
		return PruneReport{}, err
	}
	// Sweep staging BEFORE the scan, so a restored entry is subject to the
	// same pass (rooted → kept, unrooted → collected) as everything else.
	warnings := sweepStaging(ctx, cacheDir)
	entries, err := scanInstalled(cacheDir)
	if err != nil {
		return PruneReport{Drops: nil, MultiSource: nil, Warnings: warnings}, err
	}
	drops, kept, gcWarnings, err := gcEntries(ctx, cacheDir, entries, roots, entriesByKey(entries))
	warnings = append(warnings, gcWarnings...)
	if err != nil {
		return PruneReport{Drops: drops, MultiSource: nil, Warnings: warnings}, err
	}
	return PruneReport{Drops: drops, MultiSource: multiSourceIdentities(kept, roots), Warnings: warnings}, nil
}

// AutoGC reconciles the given identities' cache entries against the exact
// declaration roots — the opportunistic sweep piggybacking on install and
// update, so an edited declaration's stranded old entry does not sit in the
// cache forever. An identity's scope includes same-KEY entries under other
// identities: after an upstream rename the fresh identity's key still has a
// superseded twin under the old one, and the verbs report the fresh identity —
// the key union is how the sweep reaches the twin. It never blocks the
// operation it rides on: every outcome (a removed entry, an incomputable root
// set) is an advisory Warning. On a Manager without a roots provider it is a
// silent no-op (GC is off).
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
	all, err := scanInstalled(cacheDir)
	if err != nil {
		return []Warning{{Message: fmt.Sprintf("bundle cache maintenance skipped: %v", err)}}
	}
	winners := entriesByKey(all)
	var warnings []Warning
	for _, id := range dedupIDs(ids) {
		drops, _, gcWarnings, gcErr := gcEntries(ctx, cacheDir, identityScopedEntries(all, id), roots, winners)
		warnings = append(warnings, gcWarnings...)
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

// identityScopedEntries selects one identity's cache entries from a full scan,
// plus every entry under another identity that shares one of its keys — the
// superseded twins an upstream rename leaves behind.
func identityScopedEntries(all []InstalledEntry, id BundleID) []InstalledEntry {
	keys := map[string]bool{}
	for _, e := range all {
		if e.ID == id {
			keys[e.Key] = true
		}
	}
	var out []InstalledEntry
	for _, e := range all {
		if e.ID == id || keys[e.Key] {
			out = append(out, e)
		}
	}
	return out
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

// gcEntries removes every condemned entry: one whose value key no root
// addresses (unrooted), or one whose rooted key is addressed by a DIFFERENT
// entry (superseded — the losing twin of entriesByKey after an upstream
// identity rename; the winner passed in must come from a FULL cache scan, or
// a scoped slice would crown the wrong twin). An entry with no receipt at all
// is hand-placed — not provably refetchable, never collected (the unmanaged
// `bundle list` hint covers it); an entry whose receipt exists but cannot be
// read WAS fetched, so it is collected when condemned, with the broken receipt
// surfaced as a warning and an empty drop source. Removal serializes on the
// per-entry lock the install pipeline uses; unrooted removals clean up emptied
// identity and namespace directories, superseded removals keep the lock file
// (their key is still declared — see removeSupersededEntry). The surviving
// entries are returned for the caller's reporting.
func gcEntries(
	ctx context.Context, cacheDir string, entries []InstalledEntry,
	roots map[string]*rootedSource, winners map[string]InstalledEntry,
) ([]PruneDrop, []InstalledEntry, []Warning, error) {
	var drops []PruneDrop
	var kept []InstalledEntry
	var warnings []Warning
	for _, e := range entries {
		rooted := roots[e.Key] != nil
		superseded := rooted && winners[e.Key] != e
		if rooted && !superseded {
			kept = append(kept, e)
			continue
		}
		drop, dropWarnings, collected, rmErr := collectEntry(ctx, cacheDir, e, superseded)
		warnings = append(warnings, dropWarnings...)
		if rmErr != nil {
			return drops, kept, warnings, rmErr
		}
		if !collected {
			kept = append(kept, e)
			continue
		}
		drops = append(drops, drop)
	}
	return drops, kept, warnings, nil
}

// collectEntry removes one condemned entry, reporting whether it was
// collected: a hand-placed entry (no receipt at all) never is, an unreadable
// receipt warns but does not protect, and the removal path depends on whether
// the entry is superseded (key still declared — lock file stays) or unrooted
// (lock and emptied parents swept).
func collectEntry(
	ctx context.Context, cacheDir string, e InstalledEntry, superseded bool,
) (PruneDrop, []Warning, bool, error) {
	receipt, ok, receiptErr := readReceipt(e.Root)
	if receiptErr == nil && !ok {
		return PruneDrop{}, nil, false, nil // hand-placed: never collected
	}
	var warnings []Warning
	if receiptErr != nil {
		warnings = append(warnings, Warning{Message: fmt.Sprintf(
			"bundle %s: cache entry %s has an unreadable fetch receipt (%v); "+
				"the entry was fetched, so it is collected", e.ID, e.Key, receiptErr)})
	}
	if superseded {
		if rmErr := removeSupersededEntry(ctx, e); rmErr != nil {
			return PruneDrop{}, warnings, false, rmErr
		}
	} else {
		if rmErr := removeEntry(ctx, e); rmErr != nil {
			return PruneDrop{}, warnings, false, rmErr
		}
		cleanEmptyIdentityDirs(cacheDir, e.ID)
	}
	return PruneDrop{ID: e.ID, Key: e.Key, Source: receipt.Canonical}, warnings, true, nil
}

// removeEntry deletes one UNROOTED cache entry under the same per-entry
// advisory lock the install pipeline commits under, then drops the lock file
// itself. A concurrent install of the same value implies the value is declared
// — such an entry is rooted and never reaches here — so the lock normally only
// serializes against in-flight writes of an entry already condemned. The
// caveat is that "rooted" is judged against the collectRoots SNAPSHOT: a
// `bundle install` declaring a NEW source concurrently with a prune can have
// its entry classified unrooted and its lock unlinked mid-commit, and the
// snapshot window now spans per-root directory walks, not just file reads.
// Superseded entries (rooted key, losing twin) take removeSupersededEntry
// instead, precisely because their key still has legitimate writers. The
// subsequent cleanEmptyIdentityDirs can still race a concurrent install of a
// SIBLING key (shared ns/name parent, no identity-level lock); the install
// commit retries once on a vanished parent to absorb exactly that window.
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

// removeSupersededEntry deletes one cache entry whose rooted key a fresher
// fetch of the same declared value now addresses under a different identity
// (an upstream rename's leftover twin). Unlike removeEntry, the lock FILE is
// left in place and the emptied parents are not swept: the key is still
// declared, so a concurrent install of that value is a legitimate writer that
// must keep locking one inode (the same reasoning as Manager.Remove).
func removeSupersededEntry(ctx context.Context, e InstalledEntry) error {
	return withBundleLock(ctx, e.Root, func() error {
		if rmErr := os.RemoveAll(e.Root); rmErr != nil {
			return fmt.Errorf("remove superseded cache entry %s/%s: %w", e.ID, e.Key, rmErr)
		}
		return nil
	})
}

// sweepStaging reclaims abandoned trees under the cache's staging dir — the
// debris a crashed or interrupted install leaves behind. Only trees older
// than stagingSweepAge are touched, so a live install's clone/content/retire
// staging is never reclaimed; restores additionally serialize on the origin
// entry's lock. A retire holding tree is judged by its origin sidecar (see
// sweepRetired); clone/content stages are plain discards. Anything under the
// staging dir that is not one of the pipeline's own tree shapes is left
// alone. Every outcome is an advisory Warning — staging hygiene must never
// fail a prune.
func sweepStaging(ctx context.Context, cacheDir string) []Warning {
	stageBase := filepath.Join(cacheDir, tmpDir)
	staged, err := os.ReadDir(stageBase)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []Warning{{Message: fmt.Sprintf("bundle staging sweep skipped: %v", err)}}
	}
	var warnings []Warning
	for _, d := range staged {
		if !d.IsDir() {
			continue
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			warnings = append(warnings, Warning{Message: fmt.Sprintf(
				"bundle staging sweep: cannot stat %s: %v", d.Name(), infoErr)})
			continue
		}
		if time.Since(info.ModTime()) < stagingSweepAge {
			continue // plausibly a live install's working set
		}
		path := filepath.Join(stageBase, d.Name())
		switch {
		case strings.HasPrefix(d.Name(), retireStagePrefix):
			warnings = append(warnings, sweepRetired(ctx, cacheDir, path)...)
		case strings.HasPrefix(d.Name(), cloneStagePrefix), strings.HasPrefix(d.Name(), contentStagePrefix):
			removeStaged(path)
		}
	}
	return warnings
}

// sweepRetired reclaims one abandoned retire holding tree. The origin sidecar
// (written BEFORE the entry moves aside — see retireEntry) names the entry
// slot the retired tree came from, which decides its fate:
//
//   - no retired tree at all: a crash between MkdirTemp and the rename left
//     only the sidecar (or nothing) — discard;
//   - origin slot still EMPTY: the swap never completed, so the retired tree
//     may be the only copy of a previously serving entry — restore it, under
//     the origin entry's lock so a concurrent commit cannot be raced;
//   - origin slot OCCUPIED: a later fetch committed — the retired tree is a
//     superseded copy, discard.
//
// A retired tree without a sidecar cannot happen through the pipeline
// (write-before-rename), and a sidecar naming a path outside the cache's
// entry layout is not the pipeline's either — both mean external
// interference, so the tree is left in place and surfaced rather than acted
// on.
func sweepRetired(ctx context.Context, cacheDir, holding string) []Warning {
	retired := filepath.Join(holding, retiredName)
	if _, err := os.Lstat(retired); err != nil {
		removeStaged(holding)
		return nil
	}
	rawOrigin, err := os.ReadFile(filepath.Join(holding, retiredOriginFile))
	if err != nil {
		return []Warning{{Message: fmt.Sprintf(
			"bundle staging sweep: retired entry %s has no readable origin record (%v); left in place", holding, err)}}
	}
	origin, ok := entrySlotPath(cacheDir, strings.TrimSpace(string(rawOrigin)))
	if !ok {
		return []Warning{{Message: fmt.Sprintf(
			"bundle staging sweep: retired entry %s records an origin outside the bundle cache (%q); left in place",
			holding, strings.TrimSpace(string(rawOrigin)))}}
	}

	// The identity dir may be gone (a sibling-key GC sweeps emptied parents),
	// and the lock file lives inside it — recreate it before locking, the same
	// order the install commit uses.
	if mkErr := os.MkdirAll(filepath.Dir(origin), cacheDirPerm); mkErr != nil {
		return []Warning{{Message: fmt.Sprintf(
			"bundle staging sweep: could not reclaim retired entry %s (%v); left in place", holding, mkErr)}}
	}
	restored := false
	lockErr := withBundleLock(ctx, origin, func() error {
		if _, statErr := os.Lstat(origin); statErr == nil {
			return nil // a commit landed in the slot — the retired tree is superseded
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return fmt.Errorf("stat origin entry: %w", statErr)
		}
		if renameErr := os.Rename(retired, origin); renameErr != nil {
			return fmt.Errorf("restore retired entry: %w", renameErr)
		}
		restored = true
		return nil
	})
	if lockErr != nil {
		return []Warning{{Message: fmt.Sprintf(
			"bundle staging sweep: could not reclaim retired entry %s (%v); left in place", holding, lockErr)}}
	}
	removeStaged(holding)
	if !restored {
		return nil
	}
	return []Warning{{Message: fmt.Sprintf(
		"restored bundle cache entry %s from an interrupted install", origin)}}
}

// entrySlotSegments is the number of path segments a cache entry slot sits
// below the cache root: <namespace>/<name>/<sourceKey>.
const entrySlotSegments = 3

// entrySlotPath validates that a recorded path is a plausible cache entry
// slot — an absolute path exactly <namespace>/<name>/<sourceKey> below the
// cache root, no dot segments — and returns the slot path REBUILT from the
// cache root and the validated segments. The origin sidecar is trusted input
// only to this shape: a record naming anything else must never drive a
// rename.
func entrySlotPath(cacheDir, recorded string) (string, bool) {
	if !filepath.IsAbs(recorded) {
		return "", false
	}
	rel, err := filepath.Rel(cacheDir, recorded)
	if err != nil {
		return "", false
	}
	segments := strings.Split(rel, string(os.PathSeparator))
	if len(segments) != entrySlotSegments {
		return "", false
	}
	for _, s := range segments {
		if s == "" || strings.HasPrefix(s, ".") {
			return "", false
		}
	}
	return filepath.Join(cacheDir, segments[0], segments[1], segments[2]), true
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
