package sudo

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// TestStageHelper_ModeAndCleanup pins the staging contract. The 0755 file
// mode is load-bearing, not a default to tighten: idmap-mount re-execs
// itself inside a fresh user namespace before any uid_map is written, its
// owner is unmapped there, and only the other-execute bit lets that execve
// succeed. A well-meaning regression to 0700 fails only at live rootless
// UAT — the worst possible detection point.
func TestStageHelper_ModeAndCleanup(t *testing.T) {
	t.Parallel()

	path, cleanup, err := stageHelper(ElevatedHelper{
		Name:   "clawker-test-helper",
		Binary: []byte("#!/bin/sh\n"),
		Args:   nil,
	})
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(),
		"other-execute is required for a helper that re-execs in a fresh userns")

	dirInfo, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm(),
		"the staging directory is what keeps the path private")

	cleanup()
	_, err = os.Stat(filepath.Dir(path))
	assert.True(t, os.IsNotExist(err), "cleanup must remove the staging directory")
}

// TestStageHelper_RejectsNonBaseName pins the front-door invariant: Name is
// a path component under the private staging directory, and anything that
// could resolve outside it (traversal, separators, dot names) must error
// before a byte is written — the 0700 directory is the security boundary,
// and "../evil" would stage the root-executed binary at a predictable name
// in the world-writable temp root instead.
func TestStageHelper_RejectsNonBaseName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", ".", "..", "../evil", "sub/name", "/abs/name", "/"} {
		_, _, err := stageHelper(ElevatedHelper{
			Name:   name,
			Binary: []byte("#!/bin/sh\n"),
			Args:   nil,
		})
		require.Error(t, err, "name %q must be rejected", name)
	}
}

// TestRunElevated_SudoUnavailable: without sudo on PATH the step cannot even
// be attempted, and callers distinguish that from a helper failure via the
// sentinel.
func TestRunElevated_SudoUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	ios, _, _, _ := iostreams.Test() //nolint:dogsled // iostreams.Test returns three buffers this test does not assert on
	err := RunElevated(context.Background(), ios, ElevatedHelper{
		Name:   "clawker-test-helper",
		Binary: []byte("#!/bin/sh\n"),
		Args:   nil,
	})
	require.ErrorIs(t, err, ErrSudoUnavailable)
}
