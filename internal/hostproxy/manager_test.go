package hostproxy

import (
	"os"
	"path/filepath"
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

func TestNewManager_RequiresConfig(t *testing.T) {
	if _, err := NewManager(nil, logger.Nop()); err == nil {
		t.Fatal("expected an error for a nil config")
	}
}

func TestManagerStopIsNoOp(t *testing.T) {
	m, err := NewManager(configmocks.NewBlankConfig(), logger.Nop())
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	// Stop should not panic when called on a non-running manager
	m.Stop()
}

// TestManagerIsRunning_FollowsPIDLock: the manager reports a daemon only
// while a live process holds the PID file. There is no port to probe.
func TestManagerIsRunning_FollowsPIDLock(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()
	m, err := NewManager(cfg, logger.Nop())
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	if m.IsRunning() {
		t.Fatal("expected no daemon initially")
	}

	pidFile, err := cfg.HostProxyPIDFilePath()
	if err != nil {
		t.Fatalf("PID file path: %v", err)
	}
	lock, err := lockPIDFile(pidFile)
	if err != nil {
		t.Fatalf("lock PID file: %v", err)
	}
	if !m.IsRunning() {
		t.Error("expected a running daemon while the PID file is held")
	}
	if ensureErr := m.EnsureRunning(); ensureErr != nil {
		t.Errorf("EnsureRunning with a live daemon must not spawn another: %v", ensureErr)
	}

	releasePIDFile(lock, pidFile, logger.Nop())
	if m.IsRunning() {
		t.Error("expected no daemon after release")
	}
}

// TestIsDaemonRunningWithStalePIDFile tests that stale PID files are handled correctly.
func TestIsDaemonRunningWithStalePIDFile(t *testing.T) {
	// Create a temp PID file with a non-existent PID
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "hostproxy.pid")

	// Write a PID that doesn't exist (use a very high PID)
	if err := os.WriteFile(pidFile, []byte("999999999"), 0o644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	if IsDaemonRunning(pidFile) {
		t.Error("expected IsDaemonRunning to return false for stale PID file")
	}
}

// TestIsDaemonRunningWithMissingPIDFile tests handling of missing PID file.
func TestIsDaemonRunningWithMissingPIDFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "nonexistent.pid")

	if IsDaemonRunning(pidFile) {
		t.Error("expected IsDaemonRunning to return false for missing PID file")
	}
}

// TestGetDaemonPIDWithMissingFile tests GetDaemonPID with no PID file.
func TestGetDaemonPIDWithMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "nonexistent.pid")

	pid := GetDaemonPID(pidFile)
	if pid != 0 {
		t.Errorf("expected PID 0 for missing file, got %d", pid)
	}
}

// TestStopDaemonWithMissingFile tests StopDaemon with no running daemon.
func TestStopDaemonWithMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "nonexistent.pid")

	err := StopDaemon(pidFile)
	if err != nil {
		t.Errorf("expected no error for missing PID file, got: %v", err)
	}
}

// TestStopDaemonWithStalePIDFile tests StopDaemon cleanup of stale PID file.
func TestStopDaemonWithStalePIDFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "hostproxy.pid")

	// Write a PID that doesn't exist
	if err := os.WriteFile(pidFile, []byte("999999999"), 0o644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	err := StopDaemon(pidFile)
	if err != nil {
		t.Errorf("expected no error for stale PID, got: %v", err)
	}

	// Verify PID file was cleaned up
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("expected stale PID file to be removed")
	}
}

// TestIsProcessAlive tests process liveness detection.
func TestIsProcessAlive(t *testing.T) {
	// Current process should be alive
	if !isProcessAlive(os.Getpid()) {
		t.Error("expected current process to be alive")
	}

	// Non-existent PID should not be alive
	if isProcessAlive(999999999) {
		t.Error("expected non-existent PID to not be alive")
	}
}
