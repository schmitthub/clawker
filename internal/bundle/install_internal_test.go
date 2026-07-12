package bundle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveVersion(t *testing.T) {
	t.Run("manifest version wins over sha", func(t *testing.T) {
		v, err := resolveVersion("1.2.0", "abc123")
		require.NoError(t, err)
		assert.Equal(t, "1.2.0", v)
	})

	t.Run("falls back to resolved sha", func(t *testing.T) {
		v, err := resolveVersion("", "abc123")
		require.NoError(t, err)
		assert.Equal(t, "abc123", v)
	})

	t.Run("no version and no sha errors", func(t *testing.T) {
		_, err := resolveVersion("", "")
		require.Error(t, err)
	})

	// A hostile manifest version must never become a path traversal or a
	// dot-entry — it flows into provenance labels and image-tag components.
	rejects := []string{
		"1.0/../../x", // separator
		`a\b`,         // windows separator
		"..",          // traversal
		".tmp",        // dot-prefixed
		".hidden",     // dot-prefixed
	}
	for _, bad := range rejects {
		t.Run("rejects/"+bad, func(t *testing.T) {
			_, err := resolveVersion(bad, "abc123")
			assert.Error(t, err)
		})
	}
}

func TestSubdirRoot(t *testing.T) {
	clone := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(clone, "pkg", "bundle"), 0o755))

	t.Run("empty subdir is the clone root", func(t *testing.T) {
		root, err := subdirRoot(clone, "")
		require.NoError(t, err)
		assert.Equal(t, clone, root)
	})

	t.Run("nested subdir resolves", func(t *testing.T) {
		root, err := subdirRoot(clone, "pkg/bundle")
		require.NoError(t, err)
		resolved, evalErr := filepath.EvalSymlinks(filepath.Join(clone, "pkg", "bundle"))
		require.NoError(t, evalErr)
		assert.Equal(t, resolved, root)
	})

	t.Run("spelled traversal rejected", func(t *testing.T) {
		_, err := subdirRoot(clone, "../outside")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "escapes")
	})

	// A repo shipping its declared subdir as a symlink pointing out of the
	// clone must be refused: IsLocal only checks the spelled path, so the
	// symlink resolution guard is what closes the escape.
	t.Run("symlink escape rejected", func(t *testing.T) {
		outside := t.TempDir()
		linkClone := t.TempDir()
		require.NoError(t, os.Symlink(outside, filepath.Join(linkClone, "evil")))

		_, err := subdirRoot(linkClone, "evil")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "symlink")
	})
}
