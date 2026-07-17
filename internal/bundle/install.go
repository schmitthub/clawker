package bundle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/schmitthub/clawker/internal/bundle/fetch"
)

// tmpDir is the staging subdirectory under the cache root. Clones, staged
// content, and entries retired by a replace land here — the same filesystem as
// the entry roots, so the commit swap is [os.Rename] rather than a copy.
// Dot-prefixed, so the cache scan skips it.
const tmpDir = ".tmp"

// cloneStagePrefix names the staging tree a source is cloned into.
const cloneStagePrefix = "clone-"

// contentStagePrefix names the staging tree that holds an entry's content while
// it is copied, validated, and receipted — the exact tree the commit renames
// onto the entry.
const contentStagePrefix = "content-"

// retireStagePrefix names the holding tree a replaced entry is renamed into.
// The old entry moves aside (rather than being deleted in place) so a commit
// that cannot complete can put it back.
const retireStagePrefix = "retire-"

// retiredName is the fixed name a retired entry takes inside its holding tree.
const retiredName = "entry"

// retiredOriginFile is the sidecar inside a retire holding dir recording the
// entry path the retired tree came from. The holding dir's name is a random
// MkdirTemp suffix, so without this record a stranded retired tree (a crash or
// double fault mid-swap) could never be traced back to — or restored into —
// its entry slot.
const retiredOriginFile = "origin"

// cacheDirPerm is the permission for created cache directories.
const cacheDirPerm = 0o750

// lockTimeout bounds the per-bundle advisory file lock acquisition, mirroring
// the storage write lock.
const lockTimeout = 10 * time.Second

// lockRetryInterval is the poll interval while waiting for the per-bundle lock.
const lockRetryInterval = 100 * time.Millisecond

// lockSuffix is appended to an entry directory path to name its advisory lock
// file — shared by the commit path (withBundleLock) and the GC removal path,
// which must address the same file.
const lockSuffix = ".lock"

// fetchIntoCache clones a remote source into a staging area, materializes the
// exact tree the cache will hold, validates that tree's manifest and every
// component, and swaps it onto the value-keyed cache entry for the declared
// source (<cacheRoot>/<ns>/<name>/<sourceKey>/). It returns the resolved
// identity, the display version, and the advisory warnings a successful fetch
// accumulated (symlinks the entry could not carry).
//
// The authoritative validation runs on the EXACT BYTES the swap publishes:
// never the clone (the copy drops .git, the reserved receipt name, and any
// symlink the entry cannot carry — validating the clone would bless content
// the entry does not hold), and never a pre-receipt tree (the receipt write
// could change what an already-validated path resolves to — see copySymlink's
// receipt-alias note). So the pipeline is: copy → one manifest read for the
// display version the receipt must embed → exclusive-create the receipt →
// full validation of the final tree → swap. Between validation and the swap
// the tree is frozen — nothing writes into it, full stop.
//
// A failure before the swap leaves the cache untouched, and a failure of the
// swap itself restores the entry it was replacing (SourceError/ManifestError
// keep any previously cached entry serving).
func (m *Manager) fetchIntoCache(ctx context.Context, s Source) (BundleID, string, []Warning, error) {
	root, err := cacheRoot()
	if err != nil {
		return BundleID{}, "", nil, err
	}
	stageBase := filepath.Join(root, tmpDir)
	if mkErr := os.MkdirAll(stageBase, cacheDirPerm); mkErr != nil {
		return BundleID{}, "", nil, fmt.Errorf("create staging dir: %w", mkErr)
	}

	cloneDir, err := os.MkdirTemp(stageBase, cloneStagePrefix)
	if err != nil {
		return BundleID{}, "", nil, fmt.Errorf("stage clone dir: %w", err)
	}
	defer removeStaged(cloneDir)

	bundleRoot, resolvedSHA, err := m.stageClone(ctx, s, cloneDir)
	if err != nil {
		return BundleID{}, "", nil, err
	}

	contentStage, err := os.MkdirTemp(stageBase, contentStagePrefix)
	if err != nil {
		return BundleID{}, "", nil, fmt.Errorf("stage content dir: %w", err)
	}
	// Best-effort cleanup of the staged tree; a successful commit has already
	// renamed it away, leaving nothing to remove.
	defer removeStaged(contentStage)

	dropped, err := copyTree(bundleRoot, contentStage)
	if err != nil {
		return BundleID{}, "", nil, fmt.Errorf("copy bundle content: %w", err)
	}
	unsafe, err := sanitizeStagedLinks(contentStage)
	if err != nil {
		return BundleID{}, "", nil, fmt.Errorf("check staged symlinks: %w", err)
	}
	dropped = append(dropped, unsafe...)

	version, err := stagedVersion(contentStage, bundleRoot, resolvedSHA, dropped)
	if err != nil {
		return BundleID{}, "", nil, err
	}

	if receiptErr := stageReceipt(contentStage, s, resolvedSHA, version); receiptErr != nil {
		return BundleID{}, "", nil, receiptErr
	}

	b, err := m.validateStaged(contentStage, bundleRoot, dropped)
	if err != nil {
		return BundleID{}, "", nil, err
	}

	c := commitInputs{
		entryDir:     filepath.Join(root, b.ID.Namespace, b.ID.Name, s.Key()),
		stageBase:    stageBase,
		contentStage: contentStage,
	}
	if commitErr := commit(ctx, c); commitErr != nil {
		return BundleID{}, "", nil, commitErr
	}
	return b.ID, version, droppedWarnings(b.ID, dropped), nil
}

// sanitizeStagedLinks drops every symlink in the staged tree that cannot be
// proven safe by RESOLVING it — not by reading its target text. It returns the
// stage-relative paths it dropped.
//
// The copy's target checks are lexical: they cannot follow an intermediate
// symlink, so they see only what a link's own spelling says. That is not enough
// on its own. A link to an in-tree DIRECTORY is legitimate content and survives
// every text check (one resolving to the bundle root does not escape it), and a
// second link can then reach THROUGH it to somewhere its own spelling never
// names — out of the tree, or onto a path the pipeline writes. Enumerating the
// spellings that do this is a losing game; resolving the link answers it
// outright.
//
// This runs after the whole tree is staged (so every in-tree target exists to
// resolve against) and BEFORE the receipt is written — which is what makes the
// receipt unreachable rather than merely un-spellable: a link aiming at the
// receipt, however indirectly, resolves to nothing while the receipt does not
// exist and is dropped as unresolvable. Links that survive resolve to a real
// file that is not the receipt, and a link's target is fixed, so writing the
// receipt afterwards cannot bring any of them onto it.
//
// A link that cannot be resolved is dropped rather than kept: a bundle's
// dangling link is broken content already, and "cannot prove it safe" must
// never mean "carry it anyway".
func sanitizeStagedLinks(contentStage string) ([]string, error) {
	realStage, err := filepath.EvalSymlinks(contentStage)
	if err != nil {
		return nil, fmt.Errorf("resolve staged tree: %w", err)
	}
	// Removals go through a root-scoped handle: a walk callback acting on the
	// walked path is a TOCTOU shape, and dropping a link is exactly the moment
	// not to traverse one.
	stageRoot, err := os.OpenRoot(contentStage)
	if err != nil {
		return nil, fmt.Errorf("open staged tree: %w", err)
	}
	defer func() {
		// Close failure is unactionable: the handle is read/unlink-only and the
		// staging tree's fate is already decided by the caller's next step.
		if closeErr := stageRoot.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	var dropped []string
	if walkErr := filepath.WalkDir(contentStage, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		rel, dropErr := dropIfUnsafe(stageRoot, contentStage, path, realStage)
		if rel != "" {
			dropped = append(dropped, rel)
		}
		return dropErr
	}); walkErr != nil {
		return nil, fmt.Errorf("walk staged tree %s: %w", contentStage, walkErr)
	}
	return dropped, nil
}

// dropIfUnsafe removes one staged symlink that cannot be proven safe,
// returning its stage-relative path when it dropped it (empty when the link
// resolves safely and stays).
func dropIfUnsafe(stageRoot *os.Root, contentStage, path, realStage string) (string, error) {
	rel, err := filepath.Rel(contentStage, path)
	if err != nil {
		return "", fmt.Errorf("relativize %s: %w", path, err)
	}
	if stagedLinkResolvesSafely(path, realStage) {
		return "", nil
	}
	if rmErr := stageRoot.Remove(rel); rmErr != nil {
		return "", fmt.Errorf("drop unsafe symlink %s: %w", rel, rmErr)
	}
	return rel, nil
}

// stagedLinkResolvesSafely reports whether a staged symlink resolves — through
// any chain of intermediate links — to a real path inside the staged tree.
// Anything else (unresolvable, or landing outside) is not carryable content.
func stagedLinkResolvesSafely(path, realStage string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false // dangling or unresolvable: cannot be proven safe
	}
	return !escapesRoot(realStage, resolved)
}

// stageClone clones the declared source into cloneDir and resolves the bundle
// root within it (applying the declared-subdir escape guard). It returns the
// bundle root and the commit the fetch resolved to; failures are SourceErrors.
func (m *Manager) stageClone(ctx context.Context, s Source, cloneDir string) (string, string, error) {
	resolvedSHA, err := m.fetcher.Clone(ctx, fetch.CloneOptions{
		URL: s.URL, Ref: s.Ref, SHA: s.SHA, Dir: cloneDir,
	})
	if err != nil {
		return "", "", &SourceError{Source: s, Err: err}
	}
	bundleRoot, err := subdirRoot(cloneDir, s.Path)
	if err != nil {
		return "", "", &SourceError{Source: s, Err: err}
	}
	return bundleRoot, resolvedSHA, nil
}

// stageReceipt records the fetch receipt into the staged tree, so it is
// committed by the same swap as the content — an entry can never exist without
// its receipt.
//
// The write is the single mutation the staged tree receives after validation,
// and it must be the creation of a fresh file: writing over pre-existing state
// (a bundle-shipped file, or worse a bundle-shipped SYMLINK the write would
// follow into validated content) would change the tree validation blessed. The
// copy refuses to carry anything at the reserved name; this re-checks and fails
// closed rather than trust that. The stage is a private MkdirTemp directory
// only this call writes into, so check-then-write cannot race.
func stageReceipt(contentStage string, s Source, resolvedSHA, version string) error {
	if _, err := os.Lstat(filepath.Join(contentStage, ReceiptFile)); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return fmt.Errorf("stage %s: %w", ReceiptFile, err)
		}
		return fmt.Errorf("stage %s: reserved receipt name already present in the staged tree", ReceiptFile)
	}
	return writeReceipt(contentStage, fetchReceipt{
		Canonical: s.Canonical(),
		SHA:       resolvedSHA,
		FetchedAt: time.Now().UTC(),
		Version:   version,
	})
}

// stagedVersion resolves the display version from the staged manifest — the
// one read that must precede the receipt write, because the receipt embeds the
// version. The authoritative validation pass re-reads the manifest afterwards
// as part of the final tree. displayDir names the clone path in errors.
func stagedVersion(contentStage, displayDir, resolvedSHA string, dropped []string) (string, error) {
	pre, err := LoadBundleDir(os.DirFS(contentStage), displayDir)
	if err != nil {
		return "", annotateDropped(err, dropped)
	}
	version, err := resolveVersion(pre.Manifest.Version, resolvedSHA)
	if err != nil {
		return "", &ManifestError{Dir: displayDir, Err: err}
	}
	return version, nil
}

// validateStaged loads and validates the staged tree — the exact bytes the
// commit will publish, receipt included; it runs after the last write into the
// stage. displayDir is the clone path the content came from, so errors name a
// path the bundle author recognizes rather than the staging temp dir. dropped
// lists the symlinks the copy refused to carry, which is the usual reason a
// bundle that is fine in its own repository has content missing here.
func (m *Manager) validateStaged(contentStage, displayDir string, dropped []string) (*Bundle, error) {
	b, err := LoadBundleDir(os.DirFS(contentStage), displayDir)
	if err != nil {
		return nil, annotateDropped(err, dropped)
	}
	if compErrs := m.validateComponents(b); len(compErrs) > 0 {
		return nil, annotateDropped(&ManifestError{Dir: displayDir, Err: errors.Join(compErrs...)}, dropped)
	}
	return b, nil
}

// droppedCause is the shared explanation for a symlink the copy refused: the
// two refusal reasons copySymlink implements, spelled once so the error and the
// warning cannot drift into claiming a cause the code did not check.
const droppedCause = "bundle content must be self-contained — these symlinks cannot be carried " +
	"into the cache entry (absolute target, or a target outside the bundle root) and were not installed"

// annotateDropped explains a staged-tree validation failure in terms of the
// symlinks the copy dropped. A bundle reaching outside its own root loads in
// the repository it was authored in and nowhere else — without this the author
// sees only "no such file" for a file that is plainly in their tree.
func annotateDropped(err error, dropped []string) error {
	if len(dropped) == 0 {
		return err
	}
	return fmt.Errorf("%w; %s: %s", err, droppedCause, strings.Join(dropped, ", "))
}

// droppedWarnings renders the dropped symlinks of a SUCCESSFUL fetch as
// advisory warnings. Validation only reads what the component loaders need, so
// a dropped asset a template references installs green and breaks later at
// build time — the drop must be reported when it happens, not discovered as an
// opaque missing file downstream.
func droppedWarnings(id BundleID, dropped []string) []Warning {
	if len(dropped) == 0 {
		return nil
	}
	return []Warning{{Message: fmt.Sprintf(
		"bundle %s: %s: %s", id, droppedCause, strings.Join(dropped, ", "))}}
}

// removeStaged discards a staging tree. The failure is unactionable: it is
// best-effort cleanup of a dot-prefixed temp dir the cache scan never sees, and
// on the success path the tree has already been renamed away.
func removeStaged(dir string) {
	if rmErr := os.RemoveAll(dir); rmErr != nil {
		_ = rmErr
	}
}

// commitInputs bundles the fields the cache-commit step needs: the staged tree
// to publish, the entry it becomes, and the staging base the replaced entry is
// retired into.
type commitInputs struct {
	entryDir     string
	stageBase    string
	contentStage string
}

// commit takes the per-entry lock and performs the cache commit under it.
// A concurrent GC of a SIBLING key can empty and remove the shared identity
// directory between the MkdirAll and the lock/rename (cleanEmptyIdentityDirs
// holds no identity-level lock), surfacing as a not-exist error — one retry
// recreates the parent and commits cleanly.
func commit(ctx context.Context, c commitInputs) error {
	err := commitAttempt(ctx, c)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return commitAttempt(ctx, c)
	}
	return err
}

// commitAttempt is one parent-mkdir + lock + commit pass.
func commitAttempt(ctx context.Context, c commitInputs) error {
	if err := os.MkdirAll(filepath.Dir(c.entryDir), cacheDirPerm); err != nil {
		return fmt.Errorf("create bundle cache dir: %w", err)
	}
	return withBundleLock(ctx, c.entryDir, func() error {
		return commitLocked(c)
	})
}

// commitLocked publishes the staged tree onto the entry directory. It runs
// under the per-entry lock.
func commitLocked(c commitInputs) error {
	return commitContent(c.stageBase, c.contentStage, c.entryDir)
}

// commitContent swaps the staged tree onto the entry directory, replacing any
// entry already there — a re-fetch of the same declared value (a moved ref, a
// forced update) replaces the entry in place.
//
// The entry being replaced may be SERVING: readers take no lock, and a declared
// entry that vanishes resolves ErrNotCached and fails every build that declares
// it. So the old entry is renamed aside rather than deleted in place, and it is
// only discarded once the replacement is committed; a swap that cannot complete
// puts it back. [os.Rename] cannot overwrite a non-empty directory, so the
// replace is two renames rather than one — the entry is unreadable only for the
// instant between them, never for the length of a tree walk.
func commitContent(stageBase, contentStage, entryDir string) error {
	retired, err := retireEntry(stageBase, entryDir)
	if err != nil {
		return err
	}
	if renameErr := os.Rename(contentStage, entryDir); renameErr != nil {
		restoreEntry(retired, entryDir)
		return fmt.Errorf("commit cache entry: %w", renameErr)
	}
	if retired != "" {
		removeStaged(filepath.Dir(retired))
	}
	return nil
}

// retireEntry moves an existing entry out of the way into a holding tree on the
// same filesystem, returning the retired path (empty when there was no entry to
// replace — the first fetch of a value). The origin sidecar is written BEFORE
// the entry moves aside, so a retired tree is traceable to its entry slot from
// the moment it exists — a sidecar failure aborts cleanly with the entry still
// in place.
func retireEntry(stageBase, entryDir string) (string, error) {
	if _, err := os.Lstat(entryDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("stat cache entry: %w", err)
	}
	holding, err := os.MkdirTemp(stageBase, retireStagePrefix)
	if err != nil {
		return "", fmt.Errorf("stage retired entry dir: %w", err)
	}
	if writeErr := os.WriteFile(
		filepath.Join(holding, retiredOriginFile), []byte(entryDir), 0o600,
	); writeErr != nil {
		removeStaged(holding)
		return "", fmt.Errorf("record retired entry origin: %w", writeErr)
	}
	retired := filepath.Join(holding, retiredName)
	if renameErr := os.Rename(entryDir, retired); renameErr != nil {
		removeStaged(holding)
		return "", fmt.Errorf("retire cache entry: %w", renameErr)
	}
	return retired, nil
}

// restoreEntry puts a retired entry back after a failed swap, so the content
// that was serving before the fetch keeps serving. The entry's parent may have
// vanished between retire and swap (a sibling-key GC removes an emptied
// identity directory), so a failed restore recreates the parent and tries once
// more — otherwise the commit retry would land in the empty slot and orphan the
// retired tree in staging. If the restore still fails the retired tree is
// deliberately LEFT in staging: it may be the only copy of the entry, and the
// caller's own swap error already reports the failed install.
func restoreEntry(retired, entryDir string) {
	if retired == "" {
		return
	}
	if err := os.Rename(retired, entryDir); err != nil {
		if mkErr := os.MkdirAll(filepath.Dir(entryDir), cacheDirPerm); mkErr != nil {
			return
		}
		if retryErr := os.Rename(retired, entryDir); retryErr != nil {
			return
		}
	}
	removeStaged(filepath.Dir(retired))
}

// subdirRoot resolves the bundle root within a clone, guarding the declared
// subdir against path escape — both a spelled `..` (filepath.IsLocal) and
// symlink indirection (a repo shipping its subdir as a symlink out of the
// clone). The guard covers this one path: it anchors the bundle ROOT inside the
// clone, and says nothing about the paths beneath it. Content escaping the
// bundle root is caught downstream, where the copy refuses to carry such a link
// and validation runs against the copied tree.
func subdirRoot(cloneDir, subdir string) (string, error) {
	if subdir == "" {
		return cloneDir, nil
	}
	if !filepath.IsLocal(subdir) {
		return "", fmt.Errorf("bundle subdir %q escapes the repository", subdir)
	}
	root := filepath.Join(cloneDir, filepath.FromSlash(subdir))
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("bundle subdir %q not found in repository: %w", subdir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("bundle subdir %q is not a directory", subdir)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve bundle subdir %q: %w", subdir, err)
	}
	resolvedClone, err := filepath.EvalSymlinks(cloneDir)
	if err != nil {
		return "", fmt.Errorf("resolve clone dir: %w", err)
	}
	if resolvedRoot != resolvedClone && escapesRoot(resolvedClone, resolvedRoot) {
		return "", fmt.Errorf("bundle subdir %q escapes the repository via a symlink", subdir)
	}
	return resolvedRoot, nil
}

// resolveVersion picks the display version: the manifest version when present,
// else the full resolved commit SHA.
//
// The value is bundle-authored and lands in the fetch receipt, which is a YAML
// document inside the entry the bundle itself ships — so it is constrained to
// one plain path segment on a single line. Both halves are load-bearing:
//   - A control character (a newline above all) makes yaml.Marshal emit a block
//     scalar, letting the author write raw lines into the receipt; those bytes
//     are content in a file a component could be made to read, and they reach
//     any surface that prints a version.
//   - The path-segment rule is a floor kept ahead of its consumers: nothing
//     currently builds a path from the version (the cache is value-keyed), but
//     a later consumer that names a file or an image tag after one must not be
//     where a hostile manifest first bites.
//
// This bounds the value's SHAPE, not its blast radius: a version is still
// bundle-authored text that reaches other surfaces, and each of those is
// responsible for its own escaping.
func resolveVersion(manifestVersion, resolvedSHA string) (string, error) {
	version := manifestVersion
	if version == "" {
		version = resolvedSHA
	}
	if version == "" {
		return "", errors.New("bundle has no version and no resolved commit")
	}
	if strings.ContainsAny(version, `/\`) || !filepath.IsLocal(version) || strings.HasPrefix(version, ".") {
		return "", fmt.Errorf("bundle version %q is not a valid path segment", version)
	}
	if i := strings.IndexFunc(version, isControlRune); i >= 0 {
		return "", fmt.Errorf("bundle version contains a control character at offset %d", i)
	}
	return version, nil
}

// isControlRune reports whether r is a C0/C7 control character — the class that
// turns a one-line scalar into multi-line or terminal-active output.
func isControlRune(r rune) bool {
	const del = 0x7f
	return r < ' ' || r == del
}

// withBundleLock runs fn under an advisory file lock scoped to one cache
// entry, mirroring the storage write lock so concurrent installs of the same
// declared value serialize.
func withBundleLock(ctx context.Context, entryDir string, fn func() error) error {
	fl := flock.New(entryDir + lockSuffix)
	lockCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	locked, err := fl.TryLockContext(lockCtx, lockRetryInterval)
	if err != nil {
		return fmt.Errorf("acquire bundle lock for %s: %w", entryDir, err)
	}
	if !locked {
		return fmt.Errorf("timed out acquiring bundle lock for %s", entryDir)
	}
	defer func() {
		// Unlock error is unactionable in deferred cleanup: the OS releases the
		// flock on process exit and the write outcome is already decided.
		if unlockErr := fl.Unlock(); unlockErr != nil {
			_ = unlockErr
		}
	}()
	return fn()
}

// copyTree copies src into dst, skipping the .git directory and any symlink the
// destination tree cannot carry (see copySymlink). Regular files are copied by
// content; portable in-tree symlinks are recreated. It returns the
// src-relative paths of the symlinks it dropped, so a caller can explain
// content that went missing between the repository and the cache entry.
func copyTree(src, dst string) ([]string, error) {
	var dropped []string
	if err := filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		skipped, err := copyTreeEntry(src, dst, path, d)
		if skipped != "" {
			dropped = append(dropped, skipped)
		}
		return err
	}); err != nil {
		return nil, fmt.Errorf("walk %s: %w", src, err)
	}
	return dropped, nil
}

// copyTreeEntry copies one walked entry into the destination tree, returning
// the src-relative path of a symlink it dropped (empty when nothing was
// dropped).
func copyTreeEntry(src, dst, path string, d fs.DirEntry) (string, error) {
	rel, err := filepath.Rel(src, path)
	if err != nil {
		return "", fmt.Errorf("relativize %s: %w", path, err)
	}
	if rel == "." {
		return "", nil
	}
	// The receipt name at the bundle root is clawker-owned: the install writes
	// the authoritative receipt there after validation, as an exclusive create.
	// Carrying bundle-authored content at that name — a file, a symlink the
	// write would follow, even a directory — would either be silently replaced
	// or turn the post-validation receipt write into a write through validated
	// content. Nothing legitimate lives there; drop it whatever its type.
	if rel == ReceiptFile {
		if d.IsDir() {
			return "", filepath.SkipDir
		}
		return "", nil
	}
	target := filepath.Join(dst, rel)
	switch {
	case d.IsDir():
		if d.Name() == ".git" {
			return "", filepath.SkipDir
		}
		if mkErr := os.MkdirAll(target, cacheDirPerm); mkErr != nil {
			return "", fmt.Errorf("mkdir %s: %w", rel, mkErr)
		}
		return "", nil
	case d.Type()&fs.ModeSymlink != 0:
		return copySymlink(src, rel, path, target)
	case d.Type().IsRegular():
		return "", copyFile(path, target)
	default:
		return "", nil
	}
}

// copyFile copies a regular file, preserving its mode.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() {
		// Close error on the read side is unactionable: the copy outcome is
		// already decided by io.Copy and the write-side close below.
		if closeErr := in.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, copyErr := io.Copy(out, in); copyErr != nil {
		// Close error is unactionable here: the copy already failed and its
		// error is the one being returned; the staging tree is discarded.
		_ = out.Close()
		return fmt.Errorf("copy %s: %w", dst, copyErr)
	}
	if closeErr := out.Close(); closeErr != nil {
		return fmt.Errorf("close %s: %w", dst, closeErr)
	}
	return nil
}

// copySymlink recreates a symlink whose target text is plausibly carryable —
// relative, and not spelling its way out of src. It returns the dropped link's
// rel path when it refuses one.
//
// A relative in-tree link is content — a bundle sharing one template between
// two of its components is an expected authoring shape, and it keeps working
// because the link and its target are copied together. An absolute target is
// meaningless anywhere but the machine that authored it (even one pointing
// inside the clone, which the clone's removal leaves dangling), and a target
// spelling its way out would plant a link out of the cache.
//
// These checks are LEXICAL and therefore only a first pass: mid-walk the tree
// is incomplete, so a target cannot be resolved for real here, and reading the
// text cannot see through an intermediate directory link. They are a cheap
// filter that keeps the obviously-bad from ever being materialized —
// sanitizeStagedLinks re-checks every surviving link by resolution once the
// tree is whole, and IT is the authority on what the entry may carry.
func copySymlink(src, rel, path, dst string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil {
		return "", fmt.Errorf("readlink %s: %w", path, err)
	}
	if filepath.IsAbs(target) || escapesRoot(src, filepath.Join(filepath.Dir(path), target)) {
		return rel, nil
	}
	if linkErr := os.Symlink(target, dst); linkErr != nil {
		return "", fmt.Errorf("symlink %s: %w", dst, linkErr)
	}
	return "", nil
}

// escapesRoot reports whether an absolute resolved path lands outside the root
// directory tree.
func escapesRoot(root, resolved string) bool {
	root = filepath.Clean(root)
	resolved = filepath.Clean(resolved)
	if resolved == root {
		return false
	}
	return !strings.HasPrefix(resolved, root+string(os.PathSeparator))
}
