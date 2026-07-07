package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Materialize copies a shipped bundle (src rooted at the bundle directory)
// into destDir. Files that already exist in destDir are never overwritten —
// materialized bundles are user-owned and editable in place; upgrades only
// fill in files the user does not have yet.
//
// When destDir did not exist (or was empty) beforehand, every file comes from
// src, so the copy is stamped with src's content hash (ShippedStampFile) for
// later staleness detection. A pre-existing copy is never stamped — it may
// have been seeded from an older shipped tree, and stamping it with the
// current hash would silence exactly the staleness the stamp exists to catch.
func Materialize(src fs.FS, destDir string) error {
	fresh, err := dirMissingOrEmpty(destDir)
	if err != nil {
		return fmt.Errorf("materialize bundle into %s: %w", destDir, err)
	}
	err = fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(destDir, filepath.FromSlash(p))
		if d.IsDir() {
			if mkErr := os.MkdirAll(dest, bundleDirMode); mkErr != nil {
				return fmt.Errorf("create dir %s: %w", dest, mkErr)
			}
			return nil
		}
		return copyIfMissing(src, p, dest)
	})
	if err != nil {
		return fmt.Errorf("materialize bundle into %s: %w", destDir, err)
	}
	if fresh {
		if stampErr := writeShippedStamp(src, destDir); stampErr != nil {
			return fmt.Errorf("materialize bundle into %s: %w", destDir, stampErr)
		}
	}
	return nil
}

// copyIfMissing writes src's file p to dest unless dest already exists —
// the user-owned copy always wins.
func copyIfMissing(src fs.FS, p, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", dest, err)
	}

	content, err := fs.ReadFile(src, p)
	if err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	if writeErr := os.WriteFile(dest, content, FileMode(p)); writeErr != nil {
		return fmt.Errorf("write %s: %w", dest, writeErr)
	}
	return nil
}

// dirMissingOrEmpty reports whether dir does not exist yet or contains no
// entries — the two states in which a materialize seeds every file from the
// shipped tree.
func dirMissingOrEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("read dir %s: %w", dir, err)
	}
	return len(entries) == 0, nil
}

// ContentHash returns the deterministic SHA-256 of a bundle tree: every
// regular file as a "path\x00content\x00" record, in path order (fs.WalkDir
// walks lexically, so the sequence is stable across runs and platforms). The
// stamp file itself is excluded so hashing a materialized copy would yield
// the same value as hashing its source tree.
func ContentHash(src fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || p == ShippedStampFile {
			return nil
		}
		content, readErr := fs.ReadFile(src, p)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", p, readErr)
		}
		fmt.Fprintf(h, "%s\x00", p)
		h.Write(content)
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash bundle tree: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeShippedStamp records src's content hash into destDir's stamp file.
func writeShippedStamp(src fs.FS, destDir string) error {
	sum, err := ContentHash(src)
	if err != nil {
		return err
	}
	stamp := filepath.Join(destDir, ShippedStampFile)
	if writeErr := os.WriteFile(stamp, []byte(sum+"\n"), plainFileMode); writeErr != nil {
		return fmt.Errorf("write %s: %w", stamp, writeErr)
	}
	return nil
}

// MaterializedStale reports whether the materialized copy in dir was seeded
// from a shipped tree other than src: its stamp file is missing or records a
// different content hash. Only meaningful for copies of SHIPPED bundles —
// custom bundle directories have no shipped counterpart and no stamp.
func MaterializedStale(src fs.FS, dir string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ShippedStampFile))
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("read shipped stamp in %s: %w", dir, err)
	}
	want, err := ContentHash(src)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(raw)) != want, nil
}
