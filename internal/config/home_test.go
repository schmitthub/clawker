package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShareDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClawkerHomeEnv, tmpDir)

	dir, err := ShareDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tmpDir, ShareSubdir), dir)
}

func TestShareDir_EnsureCreatesDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClawkerHomeEnv, tmpDir)

	dir, err := ShareDir()
	require.NoError(t, err)

	err = EnsureDir(dir)
	require.NoError(t, err)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}
