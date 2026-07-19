package logger

import (
	"fmt"
	"os"
)

// OpenAppend opens path for appending, creating it (0600) if absent.
// Safe for concurrent appenders across processes: writers that emit each
// record as a single write() on the returned O_APPEND descriptor (as
// NewWriter does) never shear each other's lines — the kernel makes each
// append atomic. Rotation must be coordinated externally by a single
// owner; see RotateAtCap.
func OpenAppend(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("logger: open append log: %w", err)
	}
	return f, nil
}

// RotateAtCap renames path to backupPath when it exceeds maxBytes,
// clobbering any previous backup. Best-effort by contract — it must only
// be called by the file's single rotation owner, and every failure mode
// is benign for an append log: a missing file means nothing to rotate,
// and a lost rename race with a concurrent owner means the other rotation
// already happened (at worst the winner's fresh, near-empty file is
// rotated over the backup, losing old diagnostic lines only). Processes
// still appending to a renamed file keep their descriptors on the old
// inode and finish logging there; new opens get a fresh file.
func RotateAtCap(path, backupPath string, maxBytes int64) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxBytes {
		return
	}
	os.Rename(path, backupPath) //nolint:errcheck,gosec // benign by contract: rotation is best-effort, see doc comment
}
