package socketbridge

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestManager creates a Manager with a mock config pointing at the given temp dir.
func newTestManager(t *testing.T, dir string) *Manager {
	t.Helper()
	cfg := config.NewMockConfig()
	t.Setenv(cfg.ConfigDirEnvVar(), dir)
	return NewManager(cfg)
}

func TestReadPIDFile(t *testing.T) {
	t.Run("valid PID file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.pid")
		require.NoError(t, os.WriteFile(path, []byte("12345\n"), 0644))

		pid := readPIDFile(path)
		assert.Equal(t, 12345, pid)
	})

	t.Run("missing file", func(t *testing.T) {
		pid := readPIDFile("/nonexistent/path/test.pid")
		assert.Equal(t, 0, pid)
	})

	t.Run("invalid content", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.pid")
		require.NoError(t, os.WriteFile(path, []byte("not-a-number"), 0644))

		pid := readPIDFile(path)
		assert.Equal(t, 0, pid)
	})

	t.Run("empty file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.pid")
		require.NoError(t, os.WriteFile(path, []byte(""), 0644))

		pid := readPIDFile(path)
		assert.Equal(t, 0, pid)
	})
}

func TestIsProcessAlive(t *testing.T) {
	t.Run("current process is alive", func(t *testing.T) {
		assert.True(t, isProcessAlive(os.Getpid()))
	})

	t.Run("zero PID", func(t *testing.T) {
		assert.False(t, isProcessAlive(0))
	})

	t.Run("negative PID", func(t *testing.T) {
		assert.False(t, isProcessAlive(-1))
	})

	t.Run("very large PID", func(t *testing.T) {
		// A PID this large is extremely unlikely to exist
		assert.False(t, isProcessAlive(999999999))
	})
}

func TestWaitForPIDFile(t *testing.T) {
	t.Run("file already exists", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.pid")
		require.NoError(t, os.WriteFile(path, []byte("1"), 0644))

		err := waitForPIDFile(path, 100*1e6) // 100ms
		assert.NoError(t, err)
	})

	t.Run("timeout when file missing", func(t *testing.T) {
		err := waitForPIDFile("/nonexistent/path/test.pid", 200*1e6) // 200ms
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "timeout")
	})
}

func TestNewManager(t *testing.T) {
	cfg := config.NewMockConfig()
	m := NewManager(cfg)
	assert.NotNil(t, m)
	assert.NotNil(t, m.bridges)
	assert.Empty(t, m.bridges)
}

func TestManagerIsRunning(t *testing.T) {
	t.Run("returns false for unknown container", func(t *testing.T) {
		dir := t.TempDir()
		m := newTestManager(t, dir)
		assert.False(t, m.IsRunning("abc123def456"))
	})

	t.Run("returns true for tracked live process", func(t *testing.T) {
		dir := t.TempDir()
		m := newTestManager(t, dir)
		m.bridges["test-container"] = &bridgeProcess{
			pid:     os.Getpid(), // Current process is alive
			pidFile: filepath.Join(dir, "test-container.pid"),
		}

		assert.True(t, m.IsRunning("test-container"))
	})

	t.Run("returns false for tracked dead process", func(t *testing.T) {
		dir := t.TempDir()
		m := newTestManager(t, dir)
		m.bridges["test-container"] = &bridgeProcess{
			pid:     999999999, // Very unlikely to exist
			pidFile: filepath.Join(dir, "test-container.pid"),
		}

		assert.False(t, m.IsRunning("test-container"))
	})
}

func TestManagerStopBridge(t *testing.T) {
	t.Run("removes PID file and tracking", func(t *testing.T) {
		dir := t.TempDir()
		m := newTestManager(t, dir)

		// Create pids dir and PID file
		pidsDir := filepath.Join(dir, "pids")
		require.NoError(t, os.MkdirAll(pidsDir, 0755))

		containerID := "abc123def456789"
		pidFile := filepath.Join(pidsDir, containerID+".pid")
		require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644))

		m.bridges[containerID] = &bridgeProcess{
			pid:     999999999, // Dead process — won't actually kill anything
			pidFile: pidFile,
		}

		err := m.StopBridge(containerID)
		assert.NoError(t, err)

		// Tracking should be removed
		_, tracked := m.bridges[containerID]
		assert.False(t, tracked)

		// PID file should be removed
		_, err = os.Stat(pidFile)
		assert.True(t, os.IsNotExist(err))
	})
}

func TestManagerStopAll(t *testing.T) {
	t.Run("cleans up all PID files", func(t *testing.T) {
		dir := t.TempDir()
		m := newTestManager(t, dir)

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
		assert.Equal(t, tt.expected, shortID(tt.input), "shortID(%q)", tt.input)
	}
}

func TestManagerEnsureBridge_ShortContainerID(t *testing.T) {
	dir := t.TempDir()
	m := newTestManager(t, dir)

	// Pre-track a bridge with a short container ID and the current PID
	shortContainerID := "short"
	pidFile := filepath.Join(dir, "pids", shortContainerID+".pid")
	m.bridges[shortContainerID] = &bridgeProcess{
		pid:     os.Getpid(),
		pidFile: pidFile,
	}

	// This should NOT panic from containerID[:12] slicing
	assert.NotPanics(t, func() {
		err := m.EnsureBridge(shortContainerID, false)
		assert.NoError(t, err)
	})
}

func TestManagerEnsureBridge_IdempotentWhenTracked(t *testing.T) {
	dir := t.TempDir()
	m := newTestManager(t, dir)

	containerID := "test-container-12345"
	pidFile := filepath.Join(dir, "pids", containerID+".pid")

	// Pre-track a bridge with current PID (alive)
	m.bridges[containerID] = &bridgeProcess{
		pid:     os.Getpid(),
		pidFile: pidFile,
	}

	// EnsureBridge should be a no-op
	err := m.EnsureBridge(containerID, false)
	assert.NoError(t, err)

	// Should still be the same process
	assert.Equal(t, os.Getpid(), m.bridges[containerID].pid)
}
