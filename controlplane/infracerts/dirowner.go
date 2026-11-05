package infracerts

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// OwnLikeParent gives dir the owning UID of its parent directory, keeping
// its group. The client-cert dirs sit in the host user's bind-mounted data
// dir. On a rootful Linux daemon the CP runs as real root, so a dir it
// creates is root-owned and the host user cannot delete it or its files.
// The parent is the host user's dir, so its UID is the right owner with no
// need to know the daemon mode: on a rootless daemon or Docker Desktop the
// parent already shows as the CP's own UID and this is a no-op.
//
// Lchown never follows a symlink planted at dir.
func OwnLikeParent(dir string) error {
	parent, err := os.Stat(filepath.Dir(dir))
	if err != nil {
		return fmt.Errorf("stat parent of %s: %w", dir, err)
	}
	self, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	pst, pok := parent.Sys().(*syscall.Stat_t)
	sst, sok := self.Sys().(*syscall.Stat_t)
	if !pok || !sok || pst.Uid == sst.Uid {
		return nil
	}
	if chownErr := os.Lchown(dir, int(pst.Uid), -1); chownErr != nil {
		return fmt.Errorf("chown %s to parent owner: %w", dir, chownErr)
	}
	return nil
}
