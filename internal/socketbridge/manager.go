// Package socketbridge provides socket forwarding between host and container
// via muxrpc-style protocol over docker exec stdin/stdout.
package socketbridge

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

// SocketBridgeManager is the interface for managing socket bridge daemons.
// Commands interact with this interface (not the concrete Manager) to enable
// test mocking via socketbridgetest.MockManager.
type SocketBridgeManager interface {
	// EnsureBridge ensures a bridge daemon is running for the given container.
	// It is idempotent — if a bridge is already running, it returns immediately.
	EnsureBridge(containerID string, gpgEnabled bool) error
	// StopBridge stops the bridge daemon for the given container.
	StopBridge(containerID string) error
	// StopAll stops all known bridge daemons.
	StopAll() error
	// IsRunning returns true if a bridge daemon is running for the given container.
	IsRunning(containerID string) bool
}

// Manager tracks per-container bridge daemon processes.
// It spawns detached "clawker bridge serve" subprocesses that forward
// GPG and SSH agent sockets into running containers.
//
// Manager implements SocketBridgeManager.
type Manager struct {
	mu      sync.Mutex
	bridges map[string]*bridgeProcess // containerID -> running bridge
}

// bridgeProcess tracks a running bridge daemon for a container.
type bridgeProcess struct {
	pid     int
	pidFile string
}

// Compile-time assertion that Manager implements SocketBridgeManager.
var _ SocketBridgeManager = (*Manager)(nil)

// shortID returns a truncated container ID suitable for log messages.
// Safe to call with IDs shorter than 12 characters.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// NewManager creates a new socket bridge Manager.
func NewManager() *Manager {
	return &Manager{
		bridges: make(map[string]*bridgeProcess),
	}
}

// EnsureBridge ensures a bridge daemon is running for the given container.
// It is idempotent — if a bridge is already running, it returns immediately.
func (m *Manager) EnsureBridge(containerID string, gpgEnabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if we already track a running bridge
	if bp, ok := m.bridges[containerID]; ok {
		if isProcessAlive(bp.pid) {
			logger.Debug().Str("container", shortID(containerID)).Int("pid", bp.pid).Msg("bridge already running")
			return nil
		}
		// Process died — clean up stale entry
		m.cleanupBridgeLocked(containerID, bp)
	}

	// Check PID file from a previous CLI invocation
	pidFile, err := config.BridgePIDFile(containerID)
	if err != nil {
		return fmt.Errorf("failed to get bridge PID file path: %w", err)
	}

	if pid := readPIDFile(pidFile); pid > 0 && isProcessAlive(pid) {
		logger.Debug().Str("container", shortID(containerID)).Int("pid", pid).Msg("found existing bridge via PID file")
		m.bridges[containerID] = &bridgeProcess{pid: pid, pidFile: pidFile}
		return nil
	}

	// Spawn a new bridge daemon
	return m.startBridge(containerID, gpgEnabled, pidFile)
}

// StopBridge stops the bridge daemon for the given container.
func (m *Manager) StopBridge(containerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pidFile, err := config.BridgePIDFile(containerID)
	if err != nil {
		return fmt.Errorf("failed to get bridge PID file path: %w", err)
	}

	// Check in-memory tracking first
	if bp, ok := m.bridges[containerID]; ok {
		m.cleanupBridgeLocked(containerID, bp)
	}

	// Also check PID file (handles cross-process cleanup)
	if pid := readPIDFile(pidFile); pid > 0 {
		killProcess(pid)
	}
	os.Remove(pidFile)

	return nil
}

// StopAll stops all known bridge daemons.
func (m *Manager) StopAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop in-memory tracked bridges
	for id, bp := range m.bridges {
		m.cleanupBridgeLocked(id, bp)
	}

	// Scan bridges directory for PID files from other CLI invocations
	bridgesDir, err := config.BridgesDir()
	if err != nil {
		return nil // Non-fatal: can't find directory
	}

	entries, err := os.ReadDir(bridgesDir)
	if err != nil {
		return nil // Non-fatal: directory may not exist
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}
		pidFile := filepath.Join(bridgesDir, entry.Name())
		if pid := readPIDFile(pidFile); pid > 0 {
			killProcess(pid)
		}
		os.Remove(pidFile)
	}

	return nil
}

// IsRunning returns true if a bridge daemon is running for the given container.
func (m *Manager) IsRunning(containerID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if bp, ok := m.bridges[containerID]; ok {
		return isProcessAlive(bp.pid)
	}

	// Check PID file
	pidFile, err := config.BridgePIDFile(containerID)
	if err != nil {
		return false
	}
	pid := readPIDFile(pidFile)
	return pid > 0 && isProcessAlive(pid)
}

// startBridge spawns a detached "clawker bridge serve" subprocess.
func (m *Manager) startBridge(containerID string, gpgEnabled bool, pidFile string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Ensure bridges directory exists
	bridgesDir, err := config.BridgesDir()
	if err != nil {
		return fmt.Errorf("failed to get bridges directory: %w", err)
	}
	if err := config.EnsureDir(bridgesDir); err != nil {
		return fmt.Errorf("failed to create bridges directory: %w", err)
	}

	args := []string{
		"bridge", "serve",
		"--container", containerID,
		"--pid-file", pidFile,
	}
	if gpgEnabled {
		args = append(args, "--gpg")
	}

	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Detach from parent session
	}

	// Redirect output to log file
	logFile, err := m.openBridgeLogFile(containerID)
	if err != nil {
		logger.Debug().Err(err).Msg("failed to open bridge log file, output will be discarded")
		cmd.Stdout = nil
		cmd.Stderr = nil
	} else {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("failed to start bridge daemon: %w", err)
	}

	// Capture PID before Release — the Process handle may be invalid after Release.
	pid := cmd.Process.Pid

	// Close the log file in parent — child inherited the fd.
	if logFile != nil {
		logFile.Close()
	}

	// Release the child process so it can run independently
	if err := cmd.Process.Release(); err != nil {
		logger.Debug().Err(err).Msg("failed to release bridge process (non-fatal)")
	}

	logger.Debug().Str("container", shortID(containerID)).Int("pid", pid).Msg("started bridge daemon")

	// Wait for PID file to appear (confirms bridge is initialized)
	if err := waitForPIDFile(pidFile, 5*time.Second); err != nil {
		return fmt.Errorf("bridge started but PID file not created: %w", err)
	}

	m.bridges[containerID] = &bridgeProcess{pid: pid, pidFile: pidFile}
	return nil
}

// cleanupBridgeLocked removes the bridge from tracking and kills the process.
// Must be called with m.mu held.
func (m *Manager) cleanupBridgeLocked(containerID string, bp *bridgeProcess) {
	if isProcessAlive(bp.pid) {
		killProcess(bp.pid)
	}
	os.Remove(bp.pidFile)
	delete(m.bridges, containerID)
}

// openBridgeLogFile opens a log file for bridge daemon output.
func (m *Manager) openBridgeLogFile(containerID string) (*os.File, error) {
	logsDir, err := config.LogsDir()
	if err != nil {
		return nil, err
	}
	if err := config.EnsureDir(logsDir); err != nil {
		return nil, err
	}
	return os.OpenFile(
		filepath.Join(logsDir, fmt.Sprintf("bridge-%s.log", shortID(containerID))),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
}

// readPIDFile reads a PID from a file. Returns 0 if the file doesn't exist or is invalid.
func readPIDFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// isProcessAlive checks if a process with the given PID exists and is alive.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks if process exists without actually sending a signal
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// killProcess sends SIGTERM to a process.
func killProcess(pid int) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		logger.Debug().Err(err).Int("pid", pid).Msg("failed to send SIGTERM to bridge")
	}
}

// waitForPIDFile waits for a PID file to appear within the given timeout.
func waitForPIDFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for PID file %s", path)
}
