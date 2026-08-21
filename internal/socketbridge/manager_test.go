package socketbridge_test

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/socketbridge"
	sockebridgemocks "github.com/schmitthub/clawker/internal/socketbridge/mocks"
)

func TestReadPIDFile(t *testing.T) {
	t.Run("valid PID file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.pid")
		require.NoError(t, os.WriteFile(path, []byte("12345\n"), 0o644))

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
		require.NoError(t, os.WriteFile(path, []byte("not-a-number"), 0o644))

		pid := socketbridge.ReadPIDFileForTest(path)
		assert.Equal(t, 0, pid)
	})

	t.Run("empty file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.pid")
		require.NoError(t, os.WriteFile(path, []byte(""), 0o644))

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
		require.NoError(t, os.WriteFile(path, []byte("1"), 0o644))

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
	m, _ := sockebridgemocks.NewTestManager(t)
	assert.NotNil(t, m)
	assert.Equal(t, 0, m.BridgeCountForTest())
}

func TestManagerIsRunning(t *testing.T) {
	t.Run("returns false for unknown container", func(t *testing.T) {
		m, _ := sockebridgemocks.NewTestManager(t)
		assert.False(t, m.IsRunning("abc123def456"))
	})

	t.Run("returns true for tracked live process", func(t *testing.T) {
		m, pidsDir := sockebridgemocks.NewTestManager(t)
		m.SetBridgeForTest(
			"test-container",
			os.Getpid(),
			filepath.Join(pidsDir, "test-container.pid"),
			filepath.Join(pidsDir, "test-container.sockets.json"),
		)

		assert.True(t, m.IsRunning("test-container"))
	})

	t.Run("returns false for tracked dead process", func(t *testing.T) {
		m, pidsDir := sockebridgemocks.NewTestManager(t)
		m.SetBridgeForTest(
			"test-container",
			999999999,
			filepath.Join(pidsDir, "test-container.pid"),
			filepath.Join(pidsDir, "test-container.sockets.json"),
		)

		assert.False(t, m.IsRunning("test-container"))
	})
}

func TestManagerStopBridge(t *testing.T) {
	t.Run("removes bridge state files and tracking", func(t *testing.T) {
		m, pidsDir := sockebridgemocks.NewTestManager(t)

		containerID := "abc123def456789"
		pidFile := filepath.Join(pidsDir, containerID+".pid")
		socketsFile := filepath.Join(pidsDir, containerID+".sockets.json")
		require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644))
		require.NoError(t, socketbridge.WriteBridgedSocketsFile(socketsFile, nil))

		m.SetBridgeForTest(containerID, 999999999, pidFile, socketsFile) // Dead process — won't actually kill anything

		err := m.StopBridge(containerID)
		assert.NoError(t, err)

		// Tracking should be removed
		assert.False(t, m.HasBridgeForTest(containerID))

		// Bridge state files should be removed.
		_, err = os.Stat(pidFile)
		assert.True(t, os.IsNotExist(err))
		_, err = os.Stat(socketsFile)
		assert.True(t, os.IsNotExist(err))
	})
}

func TestManagerStopAll(t *testing.T) {
	t.Run("cleans up all bridge state files", func(t *testing.T) {
		m, pidsDir := sockebridgemocks.NewTestManager(t)

		// Create bridge state files with dead PIDs.
		for _, id := range []string{"container-a", "container-b"} {
			pidFile := filepath.Join(pidsDir, id+".pid")
			require.NoError(t, os.WriteFile(pidFile, []byte("999999999"), 0o644))
			socketsFile := filepath.Join(pidsDir, id+".sockets.json")
			require.NoError(t, socketbridge.WriteBridgedSocketsFile(socketsFile, nil))
		}

		err := m.StopAll()
		assert.NoError(t, err)

		// All bridge state files should be removed.
		entries, err := os.ReadDir(pidsDir)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
}

func TestRemoveOwnedBridgeStateFilesPreservesReplacementState(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "container.pid")
	socketsFile := filepath.Join(dir, "container.sockets.json")
	require.NoError(t, os.WriteFile(pidFile, []byte("200"), 0o600))
	require.NoError(t, socketbridge.WriteBridgedSocketsFile(socketsFile, nil))

	require.NoError(t, socketbridge.RemoveOwnedBridgeStateFiles(pidFile, socketsFile, 100))
	assert.FileExists(t, pidFile)
	assert.FileExists(t, socketsFile)

	require.NoError(t, socketbridge.RemoveOwnedBridgeStateFiles(pidFile, socketsFile, 200))
	assert.NoFileExists(t, pidFile)
	assert.NoFileExists(t, socketsFile)
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
		assert.Equal(t, tt.expected, socketbridge.ShortID(tt.input), "ShortID(%q)", tt.input)
	}
}

func TestManagerEnsureBridge_ShortContainerID(t *testing.T) {
	m, pidsDir := sockebridgemocks.NewTestManager(t)

	// Pre-track a bridge with a short container ID and the current PID
	shortContainerID := "short"
	pidFile := filepath.Join(pidsDir, shortContainerID+".pid")
	socketsFile := filepath.Join(pidsDir, shortContainerID+".sockets.json")
	require.NoError(t, socketbridge.WriteBridgedSocketsFile(socketsFile, nil))
	m.SetBridgeForTest(shortContainerID, os.Getpid(), pidFile, socketsFile)

	// This should NOT panic from containerID[:12] slicing
	assert.NotPanics(t, func() {
		err := m.EnsureBridge(socketbridge.EnsureBridgeOpts{
			ContainerID: shortContainerID,
			GPGEnabled:  false,
			Sockets:     nil,
		})
		assert.NoError(t, err)
	})
}

func TestManagerEnsureBridge_IdempotentWhenTracked(t *testing.T) {
	m, pidsDir := sockebridgemocks.NewTestManager(t)

	containerID := "test-container-12345"
	pidFile := filepath.Join(pidsDir, containerID+".pid")
	socketsFile := filepath.Join(pidsDir, containerID+".sockets.json")
	sockets := []socketbridge.BridgedSocket{{
		HostPath: "/host/service.sock",
		Target:   "/run/service.sock",
		Identity: socketbridge.ListenerIdentity{UID: 1000, GID: 1000, Owner: "", Group: ""},
		Group:    "",
		Mode:     "",
	}}
	require.NoError(t, socketbridge.WriteBridgedSocketsFile(socketsFile, sockets))

	// Pre-track a bridge with current PID (alive)
	m.SetBridgeForTest(containerID, os.Getpid(), pidFile, socketsFile)

	// EnsureBridge should be a no-op
	err := m.EnsureBridge(socketbridge.EnsureBridgeOpts{
		ContainerID: containerID,
		GPGEnabled:  true,
		Sockets:     sockets,
	})
	assert.NoError(t, err)

	// Should still be the same process
	pid, ok := m.BridgePIDForTest(containerID)
	assert.True(t, ok)
	assert.Equal(t, os.Getpid(), pid)
}

func TestManagerEnsureBridge_RespawnsAdoptedBridgeOnSocketDrift(t *testing.T) {
	m, bridgesDir := sockebridgemocks.NewTestManager(t)
	containerID := "test-container-12345"
	pidFile := filepath.Join(bridgesDir, containerID+".pid")
	socketsFile := filepath.Join(bridgesDir, containerID+".sockets.json")

	oldProcess := exec.Command("sleep", "30")
	require.NoError(t, oldProcess.Start())
	t.Cleanup(func() {
		if socketbridge.IsProcessAliveForTest(oldProcess.Process.Pid) {
			_ = oldProcess.Process.Kill() // Cleanup permits an already-exited process.
		}
	})
	require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(oldProcess.Process.Pid)), 0o600))

	staleSockets := []socketbridge.BridgedSocket{{
		HostPath: "/host/service.sock",
		Target:   "/run/service.sock",
		Identity: socketbridge.ListenerIdentity{UID: 1000, GID: 1000, Owner: "owner", Group: "primary"},
		Group:    "workers",
		Mode:     "0600",
	}}
	require.NoError(t, socketbridge.WriteBridgedSocketsFile(socketsFile, staleSockets))

	desiredSockets := []socketbridge.BridgedSocket{{
		HostPath: "/host/service.sock",
		Target:   "/run/service.sock",
		Identity: socketbridge.ListenerIdentity{UID: 1000, GID: 1000, Owner: "owner", Group: "primary"},
		Group:    "workers",
		Mode:     "0660",
	}}
	t.Setenv(consts.EnvExecutable, writeFakeBridgeExecutable(t))
	t.Cleanup(func() {
		require.NoError(t, m.StopBridge(containerID))
	})

	require.NoError(t, m.EnsureBridge(socketbridge.EnsureBridgeOpts{
		ContainerID: containerID,
		GPGEnabled:  false,
		Sockets:     desiredSockets,
	}))
	waitForProcessExit(t, oldProcess, time.Second)

	newPID := socketbridge.ReadPIDFileForTest(pidFile)
	assert.Positive(t, newPID)
	assert.NotEqual(t, oldProcess.Process.Pid, newPID)
	registrations, err := socketbridge.ReadBridgedSocketsFile(socketsFile)
	require.NoError(t, err)
	require.Len(t, registrations, 1)
	assert.Equal(t, desiredSockets[0].HostPath, registrations[0].HostPath)
	assert.Equal(t, desiredSockets[0].Target, registrations[0].Target)
	assert.Equal(t, desiredSockets[0].Identity.UID, registrations[0].Identity.UID)
	assert.Equal(t, desiredSockets[0].Identity.GID, registrations[0].Identity.GID)
	assert.Equal(t, desiredSockets[0].Group, registrations[0].Group)
	assert.Equal(t, desiredSockets[0].Mode, registrations[0].Mode)

	require.NoError(t, m.EnsureBridge(socketbridge.EnsureBridgeOpts{
		ContainerID: containerID,
		GPGEnabled:  false,
		Sockets:     desiredSockets,
	}))
	assert.Equal(t, newPID, socketbridge.ReadPIDFileForTest(pidFile))
}

func waitForProcessExit(t *testing.T, cmd *exec.Cmd, timeout time.Duration) {
	t.Helper()

	wait := make(chan error, 1)
	go func() {
		wait <- cmd.Wait()
	}()
	select {
	case err := <-wait:
		require.Error(t, err)
	case <-time.After(timeout):
		require.FailNow(t, "process did not exit")
	}
}

func writeFakeBridgeExecutable(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "bridge")
	script := `#!/bin/sh
pid_file=""
while [ "$#" -gt 0 ]; do
	if [ "$1" = "--pid-file" ]; then
		shift
		pid_file="$1"
	fi
	shift
done
printf '%s\n' "$$" > "$pid_file"
trap 'rm -f "$pid_file"; exit 0' TERM INT
while true; do
	sleep 1
done
`
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	return path
}

func TestBridgeExecutable(t *testing.T) {
	t.Run("configured override", func(t *testing.T) {
		const override = "/test/bin/clawker"
		t.Setenv(consts.EnvExecutable, override)

		got, err := socketbridge.BridgeExecutableForTest()

		require.NoError(t, err)
		assert.Equal(t, override, got)
	})

	t.Run("current executable fallback", func(t *testing.T) {
		t.Setenv(consts.EnvExecutable, "")
		want, err := os.Executable()
		require.NoError(t, err)

		got, err := socketbridge.BridgeExecutableForTest()

		require.NoError(t, err)
		assert.Equal(t, want, got)
	})
}

// TestCheckHostSSHAgent pins the SSH lane precheck contract: every failure
// form wraps ErrSSHAgentUnavailable, and a live agent socket passes.
// t.Setenv forbids t.Parallel here.
func TestCheckHostSSHAgent(t *testing.T) {
	t.Run("unset SSH_AUTH_SOCK", func(t *testing.T) {
		t.Setenv("SSH_AUTH_SOCK", "")
		err := socketbridge.CheckHostSSHAgentForTest(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, socketbridge.ErrSSHAgentUnavailable)
	})

	t.Run("dead socket path", func(t *testing.T) {
		t.Setenv("SSH_AUTH_SOCK", filepath.Join(t.TempDir(), "nope.sock"))
		err := socketbridge.CheckHostSSHAgentForTest(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, socketbridge.ErrSSHAgentUnavailable)
	})

	t.Run("live agent socket", func(t *testing.T) {
		sockPath := filepath.Join(t.TempDir(), "agent.sock")
		ln, err := net.Listen("unix", sockPath)
		require.NoError(t, err)
		t.Cleanup(func() {
			if closeErr := ln.Close(); closeErr != nil {
				t.Logf("closing listener: %v", closeErr)
			}
		})
		t.Setenv("SSH_AUTH_SOCK", sockPath)
		assert.NoError(t, socketbridge.CheckHostSSHAgentForTest(context.Background()))
	})
}
