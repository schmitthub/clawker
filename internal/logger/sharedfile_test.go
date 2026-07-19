package logger_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/logger"
)

func TestRotateAtCap(t *testing.T) {
	const capBytes = int64(64)

	t.Run("over cap rotates to backup, clobbering the previous one", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "shared.log")
		backupPath := filepath.Join(dir, "shared.log.1")
		require.NoError(t, os.WriteFile(backupPath, []byte("old backup"), 0o600))
		big := make([]byte, capBytes+1)
		require.NoError(t, os.WriteFile(logPath, big, 0o600))

		logger.RotateAtCap(logPath, backupPath, capBytes)

		_, err := os.Stat(logPath)
		assert.True(t, os.IsNotExist(err), "active log should have been renamed away")
		info, err := os.Stat(backupPath)
		require.NoError(t, err)
		assert.Equal(t, int64(len(big)), info.Size(), "previous backup should be clobbered")
	})

	t.Run("at cap untouched", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "shared.log")
		backupPath := filepath.Join(dir, "shared.log.1")
		require.NoError(t, os.WriteFile(logPath, make([]byte, capBytes), 0o600))

		logger.RotateAtCap(logPath, backupPath, capBytes)

		_, err := os.Stat(logPath)
		require.NoError(t, err, "active log should remain in place")
		_, err = os.Stat(backupPath)
		assert.True(t, os.IsNotExist(err), "no backup should be created")
	})

	t.Run("missing file is a no-op", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "shared.log")
		backupPath := filepath.Join(dir, "shared.log.1")

		logger.RotateAtCap(logPath, backupPath, capBytes)

		_, err := os.Stat(backupPath)
		assert.True(t, os.IsNotExist(err), "no backup should appear")
	})
}

func TestOpenAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.log")

	// Two handles open concurrently, both append — neither truncates.
	f1, err := logger.OpenAppend(path)
	require.NoError(t, err)
	f2, err := logger.OpenAppend(path)
	require.NoError(t, err)

	_, err = f1.WriteString("one\n")
	require.NoError(t, err)
	_, err = f2.WriteString("two\n")
	require.NoError(t, err)
	require.NoError(t, f1.Close())
	require.NoError(t, f2.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\n", string(data))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
