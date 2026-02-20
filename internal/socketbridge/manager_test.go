package socketbridge_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/schmitthub/clawker/internal/socketbridge"
	sockebridgemocks "github.com/schmitthub/clawker/internal/socketbridge/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadPIDFile(t *testing.T) {
	t.Run("valid PID file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.pid")
		require.NoError(t, os.WriteFile(path, []byte("12345\n"), 0644))

		pid := socketbridge.ReadPIDFileForTest(path)
		assert.Equal(t, 12345, pid)
	})

	t.Run("missing file", func(t *testing.T) {
		pid := socketbridge.ReadPIDFileForTest("/nonexistent/path/test.pid")
		assert.Equal(t, 0, pid)
	})

	t.Run("invalid content", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.pid")
		require.NoError(t, os.WriteFile(path, []byte("not-a-number"), 0644))

		pid := socketbridge.ReadPIDFileForTest(path)
		assert.Equal(t, 0, pid)
	})

	t.Run("empty file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.pid")
		require.NoError(t, os.WriteFile(path, []byte(""), 0644))

		pid := socketbridge.ReadPIDFileForTest(path)
		assert.Equal(t, 0, pid)
	})
}

func TestIsProcessAlive(t *testing.T) {
	t.Run("current process is alive", func(t *testing.T) {
		assert.True(t, socketbridge.IsProcessAliveForTest(os.Getpid()))
	})

	t.Run("zero PID", func(t *testing.T) {
		assert.False(t, socketbridge.IsProcessAliveForTest(0))
	})

	t.Run("negative PID", func(t *testing.T) {
		assert.False(t, socketbridge.IsProcessAliveForTest(-1))
	})

	t.Run("very large PID", func(t *testing.T) {
		// A PID this large is extremely unlikely to exist
		assert.False(t, socketbridge.IsProcessAliveForTest(999999999))
	})
}

func TestWaitForPIDFile(t *testing.T) {
	t.Run("file already exists", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.pid")
		require.NoError(t, os.WriteFile(path, []byte("1"), 0644))

		err := socketbridge.WaitForPIDFileForTest(path, 100*1e6) // 100ms
		assert.NoError(t, err)
	})

	t.Run("timeout when file missing", func(t *testing.T) {
		err := socketbridge.WaitForPIDFileForTest("/nonexistent/path/test.pid", 200*1e6) // 200ms
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "timeout")
	})
}

func TestNewManager(t *testing.T) {
	dir := t.TempDir()
	m := sockebridgemocks.NewTestManager(t, dir)
	assert.NotNil(t, m)
	assert.Equal(t, 0, m.BridgeCountForTest())
}

func TestManagerIsRunning(t *testing.T) {
	t.Run("returns false for unknown container", func(t *testing.T) {
		dir := t.TempDir()
		m := sockebridgemocks.NewTestManager(t, dir)
		assert.False(t, m.IsRunning("abc123def456"))
	})

	t.Run("returns true for tracked live process", func(t *testing.T) {
		dir := t.TempDir()
		m := sockebridgemocks.NewTestManager(t, dir)
		m.SetBridgeForTest("test-container", os.Getpid(), filepath.Join(dir, "test-container.pid"))

		assert.True(t, m.IsRunning("test-container"))
	})

	t.Run("returns false for tracked dead process", func(t *testing.T) {
		dir := t.TempDir()
		m := sockebridgemocks.NewTestManager(t, dir)
		m.SetBridgeForTest("test-container", 999999999, filepath.Join(dir, "test-container.pid"))

		assert.False(t, m.IsRunning("test-container"))
	})
}

func TestManagerStopBridge(t *testing.T) {
	t.Run("removes PID file and tracking", func(t *testing.T) {
		dir := t.TempDir()
		m := sockebridgemocks.NewTestManager(t, dir)

		// Create pids dir and PID file
		pidsDir := filepath.Join(dir, "pids")
		require.NoError(t, os.MkdirAll(pidsDir, 0755))

		containerID := "abc123def456789"
		pidFile := filepath.Join(pidsDir, containerID+".pid")
		require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644))

		m.SetBridgeForTest(containerID, 999999999, pidFile) // Dead process — won't actually kill anything

		err := m.StopBridge(containerID)
		assert.NoError(t, err)

		// Tracking should be removed
		assert.False(t, m.HasBridgeForTest(containerID))

		// PID file should be removed
		_, err = os.Stat(pidFile)
		assert.True(t, os.IsNotExist(err))
	})
}

func TestManagerStopAll(t *testing.T) {
	t.Run("cleans up all PID files", func(t *testing.T) {
		dir := t.TempDir()
		m := sockebridgemocks.NewTestManager(t, dir)

		pidsDir := filepath.Join(dir, "pids")
		require.NoError(t, os.MkdirAll(pidsDir, 0755))

		// Create some PID files with dead PIDs
		for _, id := range []string{"container-a", "container-b"} {
			pidFile := filepath.Join(pidsDir, id+".pid")
			require.NoError(t, os.WriteFile(pidFile, []byte("999999999"), 0644))
		}

		err := m.StopAll()
		assert.NoError(t, err)

		// All PID files should be removed
		entries, err := os.ReadDir(pidsDir)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
}

func TestShortID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"abcdefghijklmnop", "abcdefghijkl"},
		{"abc", "abc"},
		{"", ""},
		{"exactly12ch", "exactly12ch"},
		{"1234567890ab", "1234567890ab"},
		{"1234567890abc", "1234567890ab"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, socketbridge.ShortIDForTest(tt.input), "shortID(%q)", tt.input)
	}
}

func TestManagerEnsureBridge_ShortContainerID(t *testing.T) {
	dir := t.TempDir()
	m := sockebridgemocks.NewTestManager(t, dir)

	// Pre-track a bridge with a short container ID and the current PID
	shortContainerID := "short"
	pidFile := filepath.Join(dir, "pids", shortContainerID+".pid")
	m.SetBridgeForTest(shortContainerID, os.Getpid(), pidFile)

	// This should NOT panic from containerID[:12] slicing
	assert.NotPanics(t, func() {
		err := m.EnsureBridge(shortContainerID, false)
		assert.NoError(t, err)
	})
}

func TestManagerEnsureBridge_IdempotentWhenTracked(t *testing.T) {
	dir := t.TempDir()
	m := sockebridgemocks.NewTestManager(t, dir)

	containerID := "test-container-12345"
	pidFile := filepath.Join(dir, "pids", containerID+".pid")

	// Pre-track a bridge with current PID (alive)
	m.SetBridgeForTest(containerID, os.Getpid(), pidFile)

	// EnsureBridge should be a no-op
	err := m.EnsureBridge(containerID, false)
	assert.NoError(t, err)

	// Should still be the same process
	pid, ok := m.BridgePIDForTest(containerID)
	assert.True(t, ok)
	assert.Equal(t, os.Getpid(), pid)
}
