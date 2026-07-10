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

// tmpDir is the staging subdirectory under the cache root. Clones and copied
// content land here (same filesystem as the final version roots) so the commit
// is an atomic [os.Rename]. Dot-prefixed, so the cache scan skips it.
const tmpDir = ".tmp"

// cacheDirPerm is the permission for created cache directories.
const cacheDirPerm = 0o750

// lockTimeout bounds the per-bundle advisory file lock acquisition, mirroring
// the storage write lock.
const lockTimeout = 10 * time.Second

// lockRetryInterval is the poll interval while waiting for the per-bundle lock.
const lockRetryInterval = 100 * time.Millisecond

// fetchIntoCache clones a remote source into a staging area, validates its
// manifest before any commit, C1-checks it against a previously cached source of
// the same identity, and atomically commits the version content into the host
// cache. It returns the resolved identity and version. A failure at any step
// before the final rename leaves the cache untouched (SourceError/ManifestError
// keep any previously cached version serving).
func (m *Manager) fetchIntoCache(ctx context.Context, s Source) (BundleID, string, error) {
	root, err := cacheRoot()
	if err != nil {
		return BundleID{}, "", err
	}
	stageBase := filepath.Join(root, tmpDir)
	if mkErr := os.MkdirAll(stageBase, cacheDirPerm); mkErr != nil {
		return BundleID{}, "", fmt.Errorf("create staging dir: %w", mkErr)
	}

	cloneDir, err := os.MkdirTemp(stageBase, "clone-")
	if err != nil {
		return BundleID{}, "", fmt.Errorf("stage clone dir: %w", err)
	}
	defer func() {
		// Clone-dir removal failure is unactionable: best-effort cleanup of a
		// staging temp dir; a leftover is invisible to the dot-skipping scan.
		if rmErr := os.RemoveAll(cloneDir); rmErr != nil {
			_ = rmErr
		}
	}()

	resolvedSHA, err := m.fetcher.Clone(ctx, fetch.CloneOptions{
		URL: s.URL, Ref: s.Ref, SHA: s.SHA, Dir: cloneDir,
	})
	if err != nil {
		return BundleID{}, "", &SourceError{Source: s, Err: err}
	}

	bundleRoot, err := subdirRoot(cloneDir, s.Path)
	if err != nil {
		return BundleID{}, "", &SourceError{Source: s, Err: err}
	}

	b, err := LoadBundleDir(os.DirFS(bundleRoot), bundleRoot)
	if err != nil {
		return BundleID{}, "", err
	}

	version, err := resolveVersion(b.Manifest.Version, resolvedSHA)
	if err != nil {
		return BundleID{}, "", &ManifestError{Dir: bundleRoot, Err: err}
	}

	c := commitInputs{
		bundleDir:   filepath.Join(root, b.ID.Namespace, b.ID.Name),
		stageBase:   stageBase,
		bundleRoot:  bundleRoot,
		id:          b.ID,
		version:     version,
		resolvedSHA: resolvedSHA,
		source:      s,
	}
	if commitErr := commit(ctx, c); commitErr != nil {
		return BundleID{}, "", commitErr
	}
	return b.ID, version, nil
}

// commitInputs bundles the fields the cache-commit step needs.
type commitInputs struct {
	bundleDir   string
	stageBase   string
	bundleRoot  string
	id          BundleID
	version     string
	resolvedSHA string
	source      Source
}

// commit takes the per-bundle lock and performs the cache commit under it.
func commit(ctx context.Context, c commitInputs) error {
	if err := os.MkdirAll(filepath.Dir(c.bundleDir), cacheDirPerm); err != nil {
		return fmt.Errorf("create namespace cache dir: %w", err)
	}
	return withBundleLock(ctx, c.bundleDir, func() error {
		return commitLocked(c)
	})
}

// commitLocked enforces C1 against any prior cache entry, copies the validated
// content into the version root, and records the fetch in source.yaml. It runs
// under the per-bundle lock.
func commitLocked(c commitInputs) error {
	if err := os.MkdirAll(c.bundleDir, cacheDirPerm); err != nil {
		return fmt.Errorf("create bundle cache dir: %w", err)
	}
	meta, exists, err := readSourceMeta(c.bundleDir)
	if err != nil {
		return err
	}
	if exists {
		if collErr := checkCacheCollision(c.id, c.bundleDir, meta, c.source); collErr != nil {
			return collErr
		}
	} else {
		meta = newSourceMeta(c.source)
	}
	if ccErr := commitContent(c.stageBase, c.bundleRoot, filepath.Join(c.bundleDir, c.version)); ccErr != nil {
		return ccErr
	}
	meta.Versions[c.version] = versionMeta{SHA: c.resolvedSHA, FetchedAt: time.Now().UTC()}
	return writeSourceMeta(c.bundleDir, meta)
}

// checkCacheCollision is the C1 identity collision at the cache: a previously
// cached bundle of the same identity fetched from a different source coordinate.
func checkCacheCollision(id BundleID, bundleDir string, meta sourceMeta, s Source) error {
	if meta.source().Canonical() == s.Canonical() {
		return nil
	}
	return &CollisionError{
		Identity:   id,
		AFile:      bundleDir,
		BFile:      "(requested source)",
		ACanonical: meta.source().Canonical(),
		BCanonical: s.Canonical(),
	}
}

// commitContent copies bundleRoot (excluding .git and escaping symlinks) into a
// fresh staging tree on the cache filesystem, then atomically renames it into
// the final version root. An already-present version is idempotently refreshed.
func commitContent(stageBase, bundleRoot, finalDir string) error {
	contentStage, err := os.MkdirTemp(stageBase, "content-")
	if err != nil {
		return fmt.Errorf("stage content dir: %w", err)
	}
	defer func() {
		// Staging-dir removal failure is unactionable: best-effort cleanup; on
		// the success path the rename already moved the tree away.
		if rmErr := os.RemoveAll(contentStage); rmErr != nil {
			_ = rmErr
		}
	}()

	if copyErr := copyTree(bundleRoot, contentStage); copyErr != nil {
		return fmt.Errorf("copy bundle content: %w", copyErr)
	}
	// Remove any prior (possibly partial) version root, then rename atomically.
	if rmErr := os.RemoveAll(finalDir); rmErr != nil {
		return fmt.Errorf("clear version root: %w", rmErr)
	}
	if renameErr := os.Rename(contentStage, finalDir); renameErr != nil {
		return fmt.Errorf("commit version root: %w", renameErr)
	}
	return nil
}

// subdirRoot resolves the bundle root within a clone, guarding a declared subdir
// against path escape — both a spelled `..` (filepath.IsLocal) and symlink
// indirection (a repo shipping its subdir as a symlink out of the clone), so a
// malicious repository can never point the load-and-copy pipeline at a host
// path outside the clone.
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

// resolveVersion picks the version directory name: the manifest version when
// present, else the full resolved commit SHA. The name must be a single safe
// path segment (it becomes a directory and an image-tag component downstream).
func resolveVersion(manifestVersion, resolvedSHA string) (string, error) {
	version := manifestVersion
	if version == "" {
		version = resolvedSHA
	}
	if version == "" {
		return "", errors.New("bundle has no version and no resolved commit")
	}
	if strings.ContainsAny(version, `/\`) || !filepath.IsLocal(version) {
		return "", fmt.Errorf("bundle version %q is not a valid path segment", version)
	}
	// A dot-prefixed version would be invisible to the cache scan (dot entries
	// are skipped), and a version named after the metadata file would clobber
	// source.yaml on commit — reject both rather than corrupt the cache entry.
	if strings.HasPrefix(version, ".") || version == sourceMetaFile {
		return "", fmt.Errorf("bundle version %q is a reserved cache name", version)
	}
	return version, nil
}

// cachedCanonicals scans the cache once and returns the canonical source
// coordinate of every cached bundle that carries source metadata, so a batch of
// declarations can be tested for cache presence without re-scanning per entry.
func cachedCanonicals() (map[string]bool, error) {
	root, err := cacheRoot()
	if err != nil {
		return nil, err
	}
	installed, err := scanInstalled(root)
	if err != nil {
		return nil, err
	}
	cached := make(map[string]bool, len(installed))
	for _, ib := range installed {
		meta, ok, metaErr := readSourceMeta(ib.Root)
		if metaErr != nil {
			return nil, metaErr
		}
		if ok {
			cached[meta.source().Canonical()] = true
		}
	}
	return cached, nil
}

// withBundleLock runs fn under an advisory file lock scoped to one cached
// bundle, mirroring the storage write lock so concurrent installs of the same
// identity serialize.
func withBundleLock(ctx context.Context, bundleDir string, fn func() error) error {
	fl := flock.New(bundleDir + ".lock")
	lockCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	locked, err := fl.TryLockContext(lockCtx, lockRetryInterval)
	if err != nil {
		return fmt.Errorf("acquire bundle lock for %s: %w", bundleDir, err)
	}
	if !locked {
		return fmt.Errorf("timed out acquiring bundle lock for %s", bundleDir)
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

// copyTree copies src into dst, skipping the .git directory and any symlink
// whose target escapes src. Regular files are copied by content; in-tree
// symlinks are recreated.
func copyTree(src, dst string) error {
	if err := filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return copyTreeEntry(src, dst, path, d)
	}); err != nil {
		return fmt.Errorf("walk %s: %w", src, err)
	}
	return nil
}

// copyTreeEntry copies one walked entry into the destination tree.
func copyTreeEntry(src, dst, path string, d fs.DirEntry) error {
	rel, err := filepath.Rel(src, path)
	if err != nil {
		return fmt.Errorf("relativize %s: %w", path, err)
	}
	if rel == "." {
		return nil
	}
	target := filepath.Join(dst, rel)
	switch {
	case d.IsDir():
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if mkErr := os.MkdirAll(target, cacheDirPerm); mkErr != nil {
			return fmt.Errorf("mkdir %s: %w", rel, mkErr)
		}
		return nil
	case d.Type()&fs.ModeSymlink != 0:
		return copySymlink(src, path, target)
	case d.Type().IsRegular():
		return copyFile(path, target)
	default:
		return nil
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

// copySymlink recreates a symlink only when its target stays within src;
// escaping symlinks are dropped so a malicious bundle cannot plant a link out of
// the cache.
func copySymlink(src, path, dst string) error {
	target, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("readlink %s: %w", path, err)
	}
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(path), target)
	}
	if escapesRoot(src, resolved) {
		return nil // escaping symlink: drop it rather than plant a link out of the cache
	}
	if linkErr := os.Symlink(target, dst); linkErr != nil {
		return fmt.Errorf("symlink %s: %w", dst, linkErr)
	}
	return nil
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
