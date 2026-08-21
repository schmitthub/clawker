package cmdutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
)

func TestResolveHostPath(t *testing.T) {
	t.Run("resolves symlinks", func(t *testing.T) {
		root := t.TempDir()
		realPath := filepath.Join(root, "real.sock")
		linkPath := filepath.Join(root, "link.sock")
		require.NoError(t, os.WriteFile(realPath, nil, 0o600))
		require.NoError(t, os.Symlink(realPath, linkPath))

		resolved, err := cmdutil.ResolveHostPath(linkPath)

		require.NoError(t, err)
		assert.Equal(t, realPath, resolved)
	})

	t.Run("missing path names the path", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing.sock")

		_, err := cmdutil.ResolveHostPath(missing)

		require.Error(t, err)
		assert.ErrorContains(t, err, missing)
	})

	t.Run("rejects an unexpanded expression", func(t *testing.T) {
		_, err := cmdutil.ResolveHostPath("$CLAWKER_TEST_MISSING_SOCKET")

		require.Error(t, err)
		assert.ErrorContains(t, err, "absolute")
	})
}
