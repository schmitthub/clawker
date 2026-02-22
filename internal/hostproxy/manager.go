package hostproxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

// HostProxyService is the interface for host proxy operations used by container commands.
// Commands interact with the host proxy through this interface, enabling test doubles
// that don't spawn daemon subprocesses.
//
// Concrete implementation: Manager. Mock: hostproxytest.MockManager.
type HostProxyService interface {
	// EnsureRunning ensures the host proxy is running. Spawns a daemon if needed.
	EnsureRunning() error
	// IsRunning returns whether the host proxy is currently running.
	IsRunning() bool
	// ProxyURL returns the URL containers should use to reach the host proxy.
	ProxyURL() string
}

// validatePort checks that a port number is in the valid TCP range.
func validatePort(port int, label string) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid %s port %d: must be between 1 and 65535", label, port)
	}
	return nil
}

// Manager manages the lifecycle of the host proxy daemon.
// It spawns a daemon subprocess that persists beyond CLI lifetime,
// enabling containers to use the proxy even after the CLI exits.
type Manager struct {
	cfg  config.Config
	port int
	mu   sync.Mutex
}

// NewManager creates a new host proxy manager.
// Port is read and validated from cfg.HostProxyConfig().Manager.Port at construction time.
func NewManager(cfg config.Config) (*Manager, error) {
	port := cfg.HostProxyConfig().Manager.Port
	if err := validatePort(port, "host proxy manager"); err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, port: port}, nil
}

// EnsureRunning ensures the host proxy daemon is running.
// If the daemon is not running, it spawns a new daemon subprocess.
func (m *Manager) EnsureRunning() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if daemon is already running via PID file + health check
	if m.isDaemonRunning() {
		logger.Debug().Int("port", m.port).Msg("host proxy daemon already running")
		return nil
	}

	// Check if something is on the port (might be a daemon we didn't start)
	if m.isPortInUse() {
		logger.Debug().Int("port", m.port).Msg("host proxy already running on port")
		return nil
	}

	// Start daemon subprocess
	return m.startDaemon()
}

// Stop does nothing for the daemon-based manager.
// The daemon self-terminates when no clawker containers are running.
// Use StopDaemon() for explicit daemon teardown.
func (m *Manager) Stop() {
	// No-op: daemon manages its own lifecycle
	// This method exists for API compatibility
}

// StopDaemon explicitly stops the daemon process.
func (m *Manager) StopDaemon() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cfg == nil {
		return nil
	}
	pidFile, err := m.cfg.HostProxyPIDFilePath()
	if err != nil {
		return fmt.Errorf("failed to get PID file path: %w", err)
	}
	return StopDaemon(pidFile)
}

// IsRunning returns whether the host proxy daemon is running.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isDaemonRunning() || m.isPortInUse()
}

// Port returns the port the host proxy daemon is configured to use.
func (m *Manager) Port() int {
	return m.port
}

// ProxyURL returns the URL containers should use to reach the host proxy.
// This uses host.docker.internal which Docker automatically resolves to the host.
func (m *Manager) ProxyURL() string {
	return fmt.Sprintf("http://host.docker.internal:%d", m.port)
}

// isDaemonRunning checks if the daemon is running via PID file and health check.
func (m *Manager) isDaemonRunning() bool {
	if m.cfg == nil {
		return false
	}

	pidFile, err := m.cfg.HostProxyPIDFilePath()
	if err != nil {
		return false
	}

	// Check PID file
	if !IsDaemonRunning(pidFile) {
		return false
	}

	// Verify with health check
	return m.isPortInUse()
}

// startDaemon spawns a daemon subprocess.
func (m *Manager) startDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Build command arguments — port passed explicitly to ensure manager and daemon agree
	args := []string{
		"host-proxy", "serve",
		"--port", strconv.Itoa(m.port),
	}

	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Detach from parent session
	}

	// Redirect output to log file for debugging visibility
	cmd.Stdin = nil
	logFile, err := m.openDaemonLogFile()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to open daemon log file, output will be discarded")
		cmd.Stdout = nil
		cmd.Stderr = nil
	} else {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		// Note: logFile is intentionally not closed here - the daemon subprocess
		// inherits the file descriptor and will write to it
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Release the child process so it can run independently
	if err := cmd.Process.Release(); err != nil {
		logger.Debug().Err(err).Msg("failed to release daemon process (non-fatal)")
	}

	logger.Debug().Int("port", m.port).Int("pid", cmd.Process.Pid).Msg("started host proxy daemon")

	// Wait for health check with retry
	if err := m.waitForHealthy(3 * time.Second); err != nil {
		return fmt.Errorf("daemon started but not responding: %w", err)
	}

	logger.Debug().Msg("host proxy daemon health check passed")
	return nil
}

// waitForHealthy waits for the daemon to respond to health checks.
func (m *Manager) waitForHealthy(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		if err := m.healthCheck(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("timeout waiting for daemon to become healthy")
}

// isPortInUse checks if the configured port is already in use by a clawker host proxy.
// It verifies both the status code and service identifier to avoid mistaking
// another service for the clawker host proxy.
func (m *Manager) isPortInUse() bool {
	client := &http.Client{
		Timeout: 500 * time.Millisecond,
	}

	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", m.port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	// Verify it's actually a clawker host proxy by checking the service identifier
	var health healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return false
	}

	return health.Service == "clawker-host-proxy"
}

// healthCheck verifies the daemon is responding to requests.
func (m *Manager) healthCheck() error {
	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", m.port))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// openDaemonLogFile opens the daemon log file for writing.
// HostProxyLogFilePath already ensures the logs directory exists via subdirPath().
func (m *Manager) openDaemonLogFile() (*os.File, error) {
	if m.cfg == nil {
		return nil, fmt.Errorf("config not available for log file path")
	}

	logPath, err := m.cfg.HostProxyLogFilePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get log file path: %w", err)
	}

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	return file, nil
}
