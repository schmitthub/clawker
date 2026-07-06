package bundler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BaseContentHash computes the SHA-256 freshness key for the per-project
// base image: the rendered base Dockerfile bytes plus the contents of every
// file referenced by the project's copy instructions (their srcs live in
// the project build context, which is the base image's build context).
//
// Deliberately NOT a hash of the whole context directory — that would
// rebuild the base on every source edit. Glob expansion here is Go's
// [filepath.Glob], which is not an exact match for Docker's COPY pattern
// semantics; the imprecision can only cause a spurious base rebuild or a
// stale-skip whose COPY layers Docker itself still cache-validates — never
// a wrong image.
func (g *ProjectGenerator) BaseContentHash(baseDockerfile []byte) (string, error) {
	h := sha256.New()
	h.Write(baseDockerfile)

	if err := g.hashCopySources(h); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashCopySources feeds the contents of every copy-instruction src (files,
// directories, globs) into h, in a deterministic order.
func (g *ProjectGenerator) hashCopySources(h hash.Hash) error {
	instructions := g.cfg.Project().Build.Instructions
	if instructions == nil || len(instructions.Copy) == 0 {
		return nil
	}

	contextDir := g.GetBuildContext()

	srcs := make([]string, 0, len(instructions.Copy))
	for _, c := range instructions.Copy {
		srcs = append(srcs, c.Src)
	}
	sort.Strings(srcs)

	for _, src := range srcs {
		if err := hashCopySrc(h, contextDir, src); err != nil {
			return err
		}
	}

	return nil
}

// hashCopySrc expands one copy src (glob or literal path, relative to
// contextDir) and hashes every match. A missing src hashes a stable
// marker so its later appearance flips the hash; the build itself
// surfaces the missing file.
func hashCopySrc(h hash.Hash, contextDir, src string) error {
	resolved := src
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(contextDir, resolved)
	}

	matches, err := filepath.Glob(resolved)
	if err != nil {
		return fmt.Errorf("expand copy src %q: %w", src, err)
	}
	if len(matches) == 0 {
		fmt.Fprintf(h, "missing:%s\x00", src)
		return nil
	}
	sort.Strings(matches)

	for _, match := range matches {
		if hashErr := hashPath(h, contextDir, match); hashErr != nil {
			return hashErr
		}
	}
	return nil
}

// hashPath hashes a single file, or every regular file under a directory,
// as "relpath\x00content" records. Symlinks and .git are skipped with the
// same rules as the build-context tar walk.
func hashPath(h hash.Hash, contextDir, path string) error {
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("hash copy src %s: %w", p, walkErr)
		}

		rel, relErr := filepath.Rel(contextDir, p)
		if relErr != nil {
			rel = p
		}
		switch hashEntryAction(rel, d) {
		case hashEntrySkip:
			return nil
		case hashEntrySkipDir:
			return filepath.SkipDir
		default:
			return hashFileRecord(h, rel, p)
		}
	})
	if err != nil {
		return fmt.Errorf("hash copy sources: %w", err)
	}
	return nil
}

// hashEntryAction verdicts for a walked copy-src entry.
const (
	hashEntryRecord = iota
	hashEntrySkip
	hashEntrySkipDir
)

// hashEntryAction prunes .git and skips symlinks/directories — the same
// rules as the build-context tar walk.
func hashEntryAction(rel string, d fs.DirEntry) int {
	if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
		if d.IsDir() {
			return hashEntrySkipDir
		}
		return hashEntrySkip
	}
	if d.Type()&fs.ModeSymlink != 0 || d.IsDir() {
		return hashEntrySkip
	}
	return hashEntryRecord
}

// hashFileRecord writes one "relpath\x00content\x00" record into h.
func hashFileRecord(h hash.Hash, rel, path string) error {
	fmt.Fprintf(h, "%s\x00", filepath.ToSlash(rel))
	// Read-only hashing input under a walk that already skips symlinks; a
	// race here only skews the freshness hash (spurious rebuild at worst),
	// never what gets built.
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("hash copy src %s: %w", path, err)
	}
	defer f.Close()
	if _, copyErr := io.Copy(h, f); copyErr != nil {
		return fmt.Errorf("hash copy src %s: %w", path, copyErr)
	}
	fmt.Fprint(h, "\x00")
	return nil
}
