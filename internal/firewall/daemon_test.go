package firewall

// White-box tests for daemon.go. These live in package firewall (not
// firewall_test) so they can exercise the unexported dependency-injection
// seam (daemonDeps / ensureDaemonWith) and private helpers like
// readPIDFile / isProcessAlive.
//
// The tests here lock in two classes of regression:
//
//  1. The EnsureDaemon competing-daemon guard, which prevents a second
//     firewall daemon from being spawned while the first is still winding
//     down. The startup path variant of this bug was fixed in commit
//     e770cf25; the tests in this file cover the related paths on the
//     "daemon alive but stack unhealthy" branch.
//
//  2. WaitForDaemonExitReport — the new non-breaking helper that lets
//     callers distinguish "process exited cleanly" from "PID file missing"
//     from "still alive after timeout". The existing WaitForDaemonExit shim
//     is retained for backward compatibility with the firewall down command.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// --- fakeDeps: a recording implementation of daemonDeps for ensureDaemonWith ---

type fakeDeps struct {
	// Behavior knobs — tests flip these before calling ensureDaemonWith.
	running        bool
	runningAfter   bool // if set, isDaemonRunning flips to this after stopDaemon is called
	stackHealthy   bool
	stopErr        error
	waitExited     bool
	waitErr        error
	startErr       error
	startCalled    atomic.Bool
	stopCalled     atomic.Bool
	isRunningCalls atomic.Int32
	waitCalls      atomic.Int32
}

func (f *fakeDeps) toDeps() daemonDeps {
	return daemonDeps{
		isDaemonRunning: func(pidFile string) bool {
			f.isRunningCalls.Add(1)
			// Model the behavior of PID-file-based alive checks: the first
			// call observes "running", subsequent calls (after StopDaemon +
			// WaitForDaemonExit) observe whatever the test wants.
			if f.isRunningCalls.Load() == 1 {
				return f.running
			}
			return f.runningAfter
		},
		isStackHealthy: func(cfg config.Config) bool {
			return f.stackHealthy
		},
		stopDaemon: func(pidFile string) error {
			f.stopCalled.Store(true)
			return f.stopErr
		},
		waitForDaemonExit: func(pidFile string, timeout time.Duration) (bool, error) {
			f.waitCalls.Add(1)
			return f.waitExited, f.waitErr
		},
		startDaemonProcess: func(cfg config.Config, log *logger.Logger) error {
			f.startCalled.Store(true)
			return f.startErr
		},
	}
}

func newTestCfg(t *testing.T) config.Config {
	t.Helper()
	return configmocks.NewBlankConfig()
}

// --- ensureDaemonWith: happy paths ---

func TestEnsureDaemonWith_NoExistingDaemon_Spawns(t *testing.T) {
	cfg := newTestCfg(t)
	f := &fakeDeps{
		running: false, // nothing running → go straight to startDaemonProcess
	}

	err := ensureDaemonWith(cfg, logger.Nop(), f.toDeps())
	require.NoError(t, err)
	assert.True(t, f.startCalled.Load(), "startDaemonProcess must be called when no daemon is running")
	assert.False(t, f.stopCalled.Load(), "stopDaemon must not be called when no daemon is running")
}

func TestEnsureDaemonWith_RunningAndHealthy_NoOp(t *testing.T) {
	cfg := newTestCfg(t)
	f := &fakeDeps{
		running:      true,
		stackHealthy: true,
	}

	err := ensureDaemonWith(cfg, logger.Nop(), f.toDeps())
	require.NoError(t, err)
	assert.False(t, f.startCalled.Load(), "startDaemonProcess must not be called when daemon is running and healthy")
	assert.False(t, f.stopCalled.Load(), "stopDaemon must not be called when daemon is running and healthy")
}

// --- ensureDaemonWith: regression tests for the competing-daemon guard ---

// TestEnsureDaemonWith_StopDaemonError_NoSpawn locks in the e770cf25 contract
// on the "alive but unhealthy" branch: if StopDaemon fails, EnsureDaemon must
// propagate the error and MUST NOT spawn a second daemon. Without this guard,
// a stale firewall daemon that refuses to die would be joined by a fresh one,
// both trying to manage the same envoy/coredns containers.
func TestEnsureDaemonWith_StopDaemonError_NoSpawn(t *testing.T) {
	cfg := newTestCfg(t)
	wantErr := errors.New("kill failed: EPERM")
	f := &fakeDeps{
		running:      true,
		stackHealthy: false,
		stopErr:      wantErr,
	}

	err := ensureDaemonWith(cfg, logger.Nop(), f.toDeps())
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr, "the original StopDaemon error must be wrapped and returned")
	assert.True(t, f.stopCalled.Load(), "stopDaemon must have been attempted")
	assert.False(t, f.startCalled.Load(), "startDaemonProcess must NOT be called when StopDaemon fails — this is the regression guard from e770cf25")
	assert.Equal(t, int32(0), f.waitCalls.Load(), "waitForDaemonExit must not be reached after StopDaemon fails")
}

// TestEnsureDaemonWith_HungDaemon_NoSpawn locks in the second half of the
// competing-daemon guard: if the old daemon did not exit within the wait
// window (IsDaemonRunning still returns true OR waitForDaemonExit reports
// not-exited), EnsureDaemon must refuse to spawn a second daemon.
func TestEnsureDaemonWith_HungDaemon_NoSpawn_WaitReportsNotExited(t *testing.T) {
	cfg := newTestCfg(t)
	f := &fakeDeps{
		running:      true,
		runningAfter: false, // PID file check is secondary — wait is authoritative
		stackHealthy: false,
		stopErr:      nil,
		waitExited:   false, // the process is still alive after the deadline
	}

	err := ensureDaemonWith(cfg, logger.Nop(), f.toDeps())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to spawn a second daemon")
	assert.True(t, f.stopCalled.Load(), "stopDaemon must have been attempted")
	assert.False(t, f.startCalled.Load(), "startDaemonProcess must NOT be called when the old daemon is still alive")
}

// TestEnsureDaemonWith_HungDaemon_NoSpawn_IsRunningStillTrue covers the
// defense-in-depth path: even if waitForDaemonExit claims the process exited,
// an IsDaemonRunning check that still reports true (e.g. a brand-new daemon
// raced in and replaced the PID file) must also block spawning.
func TestEnsureDaemonWith_HungDaemon_NoSpawn_IsRunningStillTrue(t *testing.T) {
	cfg := newTestCfg(t)
	f := &fakeDeps{
		running:      true,
		runningAfter: true, // PID file still points at a live process
		stackHealthy: false,
		stopErr:      nil,
		waitExited:   true, // wait thinks it's gone — but the PID file disagrees
	}

	err := ensureDaemonWith(cfg, logger.Nop(), f.toDeps())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to spawn a second daemon")
	assert.False(t, f.startCalled.Load(), "startDaemonProcess must NOT be called when IsDaemonRunning still reports alive after the wait")
}

// TestEnsureDaemonWith_RestartAfterCleanExit exercises the recovery path:
// daemon was alive+unhealthy, StopDaemon succeeded, the old process exited
// cleanly within the wait, and the PID file is no longer pointed at a live
// process. In that case EnsureDaemon should spawn a fresh daemon.
func TestEnsureDaemonWith_RestartAfterCleanExit(t *testing.T) {
	cfg := newTestCfg(t)
	f := &fakeDeps{
		running:      true,
		runningAfter: false, // old daemon is gone
		stackHealthy: false,
		stopErr:      nil,
		waitExited:   true,
	}

	err := ensureDaemonWith(cfg, logger.Nop(), f.toDeps())
	require.NoError(t, err)
	assert.True(t, f.stopCalled.Load())
	assert.True(t, f.startCalled.Load(), "after a clean teardown, a fresh daemon must be spawned")
}

// TestEnsureDaemonWith_WaitErrorTolerated covers the B3 change: a non-nil
// error from waitForDaemonExit (e.g. PID file already vanished) must not
// itself block the restart. The authoritative "should we spawn?" decision
// is (exited && !isDaemonRunning).
func TestEnsureDaemonWith_WaitErrorTolerated(t *testing.T) {
	cfg := newTestCfg(t)
	f := &fakeDeps{
		running:      true,
		runningAfter: false,
		stackHealthy: false,
		stopErr:      nil,
		waitExited:   true,
		waitErr:      errors.New("pid file already removed"),
	}

	err := ensureDaemonWith(cfg, logger.Nop(), f.toDeps())
	require.NoError(t, err)
	assert.True(t, f.startCalled.Load(), "a wait error alongside exited=true must not block spawning")
}

// --- WaitForDaemonExitReport ---

func writePID(t *testing.T, path string, pid int) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(itoa(pid)), 0o644))
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func TestWaitForDaemonExitReport_PIDFileMissing(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "firewall.pid")
	exited, err := WaitForDaemonExitReport(tmp, 50*time.Millisecond)
	assert.True(t, exited, "a missing PID file means no daemon is alive — callers should treat as exited")
	assert.Error(t, err, "the underlying ReadFile error must be surfaced so callers can distinguish 'clean exit' from 'unknown'")
}

func TestWaitForDaemonExitReport_PIDFileMalformed(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "firewall.pid")
	require.NoError(t, os.WriteFile(tmp, []byte("not-a-pid"), 0o644))
	exited, err := WaitForDaemonExitReport(tmp, 50*time.Millisecond)
	assert.True(t, exited, "an unparseable PID file is also not-alive from the caller's perspective")
	assert.Error(t, err)
}

func TestWaitForDaemonExitReport_ProcessAlreadyDead(t *testing.T) {
	// A PID of 0 or one that can never be alive: Signal(0) on a nonexistent
	// process returns an error, so isProcessAlive returns false immediately.
	// We pick a high unlikely-to-exist PID.
	tmp := filepath.Join(t.TempDir(), "firewall.pid")
	writePID(t, tmp, 999_999_999)
	start := time.Now()
	exited, err := WaitForDaemonExitReport(tmp, 500*time.Millisecond)
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.True(t, exited)
	assert.Less(t, elapsed, 300*time.Millisecond, "a non-alive process should be detected on the first poll, not after the full timeout")
}

func TestWaitForDaemonExitReport_ProcessAlive_TimesOut(t *testing.T) {
	// Spawn a real long-lived child and point the PID file at it.
	cmd := exec.Command("sleep", "30")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	tmp := filepath.Join(t.TempDir(), "firewall.pid")
	writePID(t, tmp, cmd.Process.Pid)

	start := time.Now()
	exited, err := WaitForDaemonExitReport(tmp, 400*time.Millisecond)
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.False(t, exited, "a still-running process must be reported as not-exited")
	// Should have actually waited approximately the full timeout.
	assert.GreaterOrEqual(t, elapsed, 350*time.Millisecond)
}

func TestWaitForDaemonExitReport_ProcessExitsMidWait(t *testing.T) {
	// Spawn a short-lived child that exits ~100ms in. WaitForDaemonExitReport
	// with a generous timeout should detect the exit and return (true, nil).
	cmd := exec.Command("sleep", "0.1")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	// Reap in the background so the zombie clears and Signal(0) reports the
	// process as gone.
	go func() { _, _ = cmd.Process.Wait() }()

	tmp := filepath.Join(t.TempDir(), "firewall.pid")
	writePID(t, tmp, pid)

	exited, err := WaitForDaemonExitReport(tmp, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, exited)
}

// WaitForDaemonExit is the backward-compatibility shim — verify it still
// returns silently on the exact same inputs (no panics, no crashes) so the
// firewall down command (which still calls it) keeps working.
func TestWaitForDaemonExit_ShimStillWorks(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "firewall.pid")
	// Missing pid file — should return cleanly.
	WaitForDaemonExit(tmp, 20*time.Millisecond)

	// Dead PID — should return cleanly.
	writePID(t, tmp, 999_999_999)
	WaitForDaemonExit(tmp, 20*time.Millisecond)
}
