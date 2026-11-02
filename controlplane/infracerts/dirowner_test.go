package infracerts_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/infracerts"
)

func TestOwnLikeParent_SameOwnerIsNoop(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "clients")
	require.NoError(t, os.Mkdir(dir, 0o750))
	require.NoError(t, infracerts.OwnLikeParent(dir))
	fi, err := os.Stat(dir)
	require.NoError(t, err)
	st, ok := fi.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	require.Equal(t, uint32(os.Getuid()), st.Uid)
}

func TestOwnLikeParent_ChownsToParentOwner(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("chown to another uid needs root")
	}
	parent := t.TempDir()
	require.NoError(t, os.Chown(parent, 4242, 4242))
	dir := filepath.Join(parent, "clients")
	require.NoError(t, os.Mkdir(dir, 0o750))
	require.NoError(t, os.Chown(dir, 0, 7777))
	require.NoError(t, infracerts.OwnLikeParent(dir))
	fi, err := os.Stat(dir)
	require.NoError(t, err)
	st, ok := fi.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	require.Equal(t, uint32(4242), st.Uid)
	require.Equal(t, uint32(7777), st.Gid, "group is kept")
}

func TestOwnLikeParent_MissingDir(t *testing.T) {
	require.Error(t, infracerts.OwnLikeParent(filepath.Join(t.TempDir(), "nope")))
}
