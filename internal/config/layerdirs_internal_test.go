package config

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The verdict table IS the tolerance policy for the GC-roots layer walk: every
// walk error flows through classifyLayerWalkError, and projectLayerDirs's
// plumbing of its (skip, verdict) pair is exercised end-to-end by the
// permission-denied prune tests. A mid-walk deletion cannot be driven
// deterministically through the public API without a test seam (forbidden in
// prod signatures), so the ENOENT row is pinned here, at the choke point.
func TestClassifyLayerWalkError(t *testing.T) {
	const root = "/proj"

	t.Run("vanished subdirectory is skipped silently", func(t *testing.T) {
		// Build churn (npm ci, rm -rf dist/) deletes non-dot dirs mid-walk all
		// the time; a directory that no longer exists holds no declarations by
		// definition, so it is not reportable — no skip record, keep walking.
		skip, verdict := classifyLayerWalkError(root, filepath.Join(root, "dist"), fs.ErrNotExist)
		assert.Empty(t, skip)
		assert.Equal(t, filepath.SkipDir, verdict)
	})

	t.Run("permission-denied subdirectory is skipped and reported", func(t *testing.T) {
		locked := filepath.Join(root, "locked")
		skip, verdict := classifyLayerWalkError(root, locked, fs.ErrPermission)
		assert.Equal(t, locked, skip)
		assert.Equal(t, filepath.SkipDir, verdict)
	})

	t.Run("missing root stops the walk cleanly", func(t *testing.T) {
		skip, verdict := classifyLayerWalkError(root, root, fs.ErrNotExist)
		assert.Empty(t, skip)
		assert.Equal(t, filepath.SkipAll, verdict)
	})

	t.Run("unreadable root is fatal", func(t *testing.T) {
		skip, verdict := classifyLayerWalkError(root, root, fs.ErrPermission)
		assert.Empty(t, skip)
		assert.ErrorIs(t, verdict, fs.ErrPermission)
	})

	t.Run("any other subdirectory error is fatal", func(t *testing.T) {
		// EIO/ESTALE mean the tree genuinely could not be assessed — loud and
		// retriable beats a silently incomplete roots union.
		ioErr := errors.New("input/output error")
		skip, verdict := classifyLayerWalkError(root, filepath.Join(root, "flaky"), ioErr)
		assert.Empty(t, skip)
		assert.ErrorIs(t, verdict, ioErr)
	})
}
