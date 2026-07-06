package harness

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Materialize copies a shipped bundle (src rooted at the bundle directory)
// into destDir. Files that already exist in destDir are never overwritten —
// materialized bundles are user-owned and editable in place; upgrades only
// fill in files the user does not have yet.
func Materialize(src fs.FS, destDir string) error {
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
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
