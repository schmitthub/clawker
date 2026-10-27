package hostproxy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

// Service is the interface for host proxy operations used by container commands.
// Commands interact with the host proxy through this interface, enabling test doubles
// that don't spawn daemon subprocesses.
//
// Concrete implementation: Manager. Mock: hostproxytest.MockManager.

type Service interface {
	// EnsureRunning ensures the host proxy is running. Spawns a daemon if needed.
	EnsureRunning() error
	// IsRunning returns whether the host proxy is currently running.
	IsRunning() bool
	// PrecheckGitCredential reports whether the host can serve the git
	// credential action: the same `git` binary the handler execs
	// must be on PATH with a credential helper configured. Callers warn
	// the user before container start, so an in-container credential
	// failure has a visible cause.
	PrecheckGitCredential(ctx context.Context) error
}

// Manager manages the lifecycle of the host proxy daemon.
// It spawns a daemon subprocess that persists beyond CLI lifetime,
// enabling containers to use the proxy even after the CLI exits.
type Manager struct {
	cfg config.Config
	log *logger.Logger
	mu  sync.Mutex
}

// NewManager creates a new host proxy manager.
func NewManager(cfg config.Config, log *logger.Logger) (*Manager, error) {
	if cfg == nil {
		return nil, errors.New("host proxy manager: config is required")
	}
	return &Manager{cfg: cfg, log: log, mu: sync.Mutex{}}, nil
}

// EnsureRunning ensures the host proxy daemon is running.
// If the daemon is not running, it spawns a new daemon subprocess.
func (m *Manager) EnsureRunning() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isDaemonRunning() {
		m.log.Debug().Msg("host proxy daemon already running")
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
	return m.isDaemonRunning()
}

// isDaemonRunning checks the PID file names a live process. The daemon
// writes it only while it holds the single-instance lock.
func (m *Manager) isDaemonRunning() bool {
	pidFile, err := m.cfg.HostProxyPIDFilePath()
	if err != nil {
		return false
	}
	return IsDaemonRunning(pidFile)
}

// startDaemon spawns a daemon subprocess.
func (m *Manager) startDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	args := []string{"host-proxy", "serve"}

	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Detach from parent session
	}

	// Redirect output to log file for debugging visibility
	cmd.Stdin = nil
	logFile, err := m.openDaemonLogFile()
	if err != nil {
		m.log.Debug().Err(err).Msg("failed to open daemon log file, output will be discarded")
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
		m.log.Debug().Err(err).Msg("failed to release daemon process (non-fatal)")
	}

	m.log.Debug().Int("pid", cmd.Process.Pid).Msg("started host proxy daemon")

	// The daemon writes its PID file once it holds the single-instance
	// lock. It then connects to the CP by itself and keeps reconnecting.
	if waitErr := m.waitForPIDFile(daemonStartTimeout); waitErr != nil {
		return fmt.Errorf("daemon started but did not take its lock: %w", waitErr)
	}
	return nil
}

// daemonStartTimeout bounds the wait for a new daemon's PID file.
const daemonStartTimeout = 3 * time.Second

// waitForPIDFile waits until the PID file names a live process.
func (m *Manager) waitForPIDFile(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.isDaemonRunning() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("no live host proxy daemon after %s", timeout)
}

// openDaemonLogFile opens the daemon log file for writing.
// HostProxyLogFilePath already ensures the logs directory exists via subdirPath().
func (m *Manager) openDaemonLogFile() (*os.File, error) {
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
