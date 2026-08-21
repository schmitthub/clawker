// Package socketbridge provides socket forwarding between host and container
// via muxrpc-style protocol over docker exec stdin/stdout.
package socketbridge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

const (
	bridgedSocketsFileSuffix = ".sockets.json"
	bridgePIDFileTimeout     = 5 * time.Second
)

// SocketBridgeManager is the interface for managing socket bridge daemons.
// Commands interact with this interface (not the concrete Manager) to enable
// test mocking via sockebridgemocks.SocketBridgeManagerMock.
//
//go:generate moq -rm -pkg mocks -out mocks/manager_mock.go . SocketBridgeManager
type SocketBridgeManager interface {
	// EnsureBridge ensures a bridge daemon is running for the given container.
	// It is idempotent — if a bridge is already running, it returns immediately.
	EnsureBridge(opts EnsureBridgeOpts) error
	// StopBridge stops the bridge daemon for the given container.
	StopBridge(containerID string) error
	// StopAll stops all known bridge daemons.
	StopAll() error
	// IsRunning returns true if a bridge daemon is running for the given container.
	IsRunning(containerID string) bool
	// Precheck reports whether the host can serve the forwarding lanes the
	// options request, before the bridge daemon spawns. Lanes the options
	// leave off are not probed. It returns nil when every requested lane is
	// usable, or an error wrapping ErrGPGUnavailable,
	// ErrSSHAgentUnavailable, or both ([errors.Join]) — match with
	// [errors.Is]. Callers warn the user, so a later bridge failure has a
	// visible cause instead of an opaque daemon-side error.
	Precheck(ctx context.Context, opts PrecheckOptions) error
}

// EnsureBridgeOpts contains the complete start-time bridge registration.
type EnsureBridgeOpts struct {
	ContainerID string
	GPGEnabled  bool
	Sockets     []BridgedSocket
}

// PrecheckOptions selects the forwarding lanes Precheck probes. Callers set
// each lane from the project's git-credential config, so a disabled lane is
// never checked.
type PrecheckOptions struct {
	// GPG probes the host GPG material and gpg-agent extra socket.
	GPG bool
	// SSH probes the host SSH agent socket with a dial.
	SSH bool
}

// Manager tracks per-container bridge daemon processes.
// It spawns detached "clawker bridge serve" subprocesses that forward
// GPG and SSH agent sockets into running containers.
//
// Manager implements SocketBridgeManager.
type Manager struct {
	cfg     config.Config
	log     *logger.Logger
	mu      sync.Mutex
	bridges map[string]*bridgeProcess // containerID -> running bridge
}

// bridgeProcess tracks a running bridge daemon for a container.
type bridgeProcess struct {
	pid         int
	pidFile     string
	socketsFile string
}

// Compile-time assertion that Manager implements SocketBridgeManager.
var _ SocketBridgeManager = (*Manager)(nil)

// ShortID returns a truncated container ID suitable for log messages.
// Safe to call with IDs shorter than 12 characters.
func ShortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// NewManager creates a new socket bridge Manager.
func NewManager(cfg config.Config, log *logger.Logger) *Manager {
	return &Manager{
		cfg:     cfg,
		log:     log,
		bridges: make(map[string]*bridgeProcess),
	}
}

// EnsureBridge ensures a bridge daemon is running for the given container.
// It is idempotent — if a bridge is already running, it returns immediately.
func (m *Manager) EnsureBridge(opts EnsureBridgeOpts) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	containerID := opts.ContainerID
	pidFile, socketsFile, err := m.bridgeStatePaths(containerID)
	if err != nil {
		return err
	}

	if bp, ok := m.bridges[containerID]; ok {
		current, reuseErr := m.reuseBridgeIfCurrent(containerID, bp, opts.Sockets)
		if reuseErr != nil {
			return reuseErr
		}
		if current {
			return nil
		}
	}

	if pid := readPIDFile(pidFile); pid > 0 {
		bp := &bridgeProcess{pid: pid, pidFile: pidFile, socketsFile: socketsFile}
		current, reuseErr := m.reuseBridgeIfCurrent(containerID, bp, opts.Sockets)
		if reuseErr != nil {
			return reuseErr
		}
		if current {
			return nil
		}
	}

	return m.startBridge(opts, pidFile, socketsFile)
}

func (m *Manager) bridgeStatePaths(containerID string) (string, string, error) {
	pidFile, err := m.cfg.BridgePIDFilePath(containerID)
	if err != nil {
		return "", "", fmt.Errorf("failed to get bridge PID file path: %w", err)
	}
	bridgesDir, err := m.cfg.BridgesSubdir()
	if err != nil {
		return "", "", fmt.Errorf("failed to get bridges directory: %w", err)
	}
	return pidFile, filepath.Join(bridgesDir, containerID+bridgedSocketsFileSuffix), nil
}

func (m *Manager) reuseBridgeIfCurrent(
	containerID string,
	bp *bridgeProcess,
	desired []BridgedSocket,
) (bool, error) {
	if !isProcessAlive(bp.pid) {
		return false, m.cleanupBridgeLocked(containerID, bp)
	}
	if !bridgeRegistrationsMatch(bp.socketsFile, desired) {
		m.log.Debug().
			Str("container", ShortID(containerID)).
			Int("pid", bp.pid).
			Msg("socket registrations changed; restarting bridge")
		return false, m.cleanupBridgeLocked(containerID, bp)
	}

	m.log.Debug().Str("container", ShortID(containerID)).Int("pid", bp.pid).Msg("bridge already running")
	m.bridges[containerID] = bp
	return true, nil
}

func bridgeRegistrationsMatch(path string, desired []BridgedSocket) bool {
	registered, err := ReadBridgedSocketsFile(path)
	if err != nil {
		return false
	}
	return bridgedSocketSetsEqual(registered, desired)
}

func bridgedSocketSetsEqual(left, right []BridgedSocket) bool {
	if len(left) != len(right) {
		return false
	}

	remaining := make(map[bridgedSocketJSON]int, len(left))
	for _, socket := range left {
		remaining[socket.jsonValue()]++
	}
	for _, socket := range right {
		key := socket.jsonValue()
		remaining[key]--
		if remaining[key] < 0 {
			return false
		}
	}
	return true
}

// StopBridge stops the bridge daemon for the given container.
func (m *Manager) StopBridge(containerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pidFile, socketsFile, err := m.bridgeStatePaths(containerID)
	if err != nil {
		return err
	}

	// Check in-memory tracking first
	if bp, ok := m.bridges[containerID]; ok {
		if cleanupErr := m.cleanupBridgeLocked(containerID, bp); cleanupErr != nil {
			return cleanupErr
		}
	}

	// Also check PID file (handles cross-process cleanup)
	if pid := readPIDFile(pidFile); pid > 0 {
		m.killProcess(pid)
	}
	return RemoveBridgeStateFiles(pidFile, socketsFile)
}

// StopAll stops all known bridge daemons.
func (m *Manager) StopAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var cleanupErrors []error

	// Stop in-memory tracked bridges
	for id, bp := range m.bridges {
		if err := m.cleanupBridgeLocked(id, bp); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}

	// Scan bridges directory for PID files from other CLI invocations
	bridgesDir, err := m.cfg.BridgesSubdir()
	if err != nil {
		m.log.Debug().Err(err).Msg("failed to find bridge state directory during cleanup")
		return errors.Join(cleanupErrors...)
	}

	entries, err := os.ReadDir(bridgesDir)
	if err != nil {
		m.log.Debug().Err(err).Msg("failed to read bridge state directory during cleanup")
		return errors.Join(cleanupErrors...)
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}
		pidFile := filepath.Join(bridgesDir, entry.Name())
		if pid := readPIDFile(pidFile); pid > 0 {
			m.killProcess(pid)
		}
		containerID := strings.TrimSuffix(entry.Name(), ".pid")
		socketsFile := filepath.Join(bridgesDir, containerID+bridgedSocketsFileSuffix)
		if cleanupErr := RemoveBridgeStateFiles(pidFile, socketsFile); cleanupErr != nil {
			cleanupErrors = append(cleanupErrors, cleanupErr)
		}
	}

	return errors.Join(cleanupErrors...)
}

// IsRunning returns true if a bridge daemon is running for the given container.
func (m *Manager) IsRunning(containerID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if bp, ok := m.bridges[containerID]; ok {
		return isProcessAlive(bp.pid)
	}

	// Check PID file
	pidFile, err := m.cfg.BridgePIDFilePath(containerID)
	if err != nil {
		return false
	}
	pid := readPIDFile(pidFile)
	return pid > 0 && isProcessAlive(pid)
}

// Sentinel errors for Precheck — one per forwarding lane. Callers match
// with [errors.Is]; the wrapped detail carries the concrete cause.
var (
	ErrGPGUnavailable      = errors.New("host GPG material unavailable")
	ErrSSHAgentUnavailable = errors.New("host SSH agent unavailable")
)

// Precheck implements SocketBridgeManager by running the same host lookups
// the bridge daemon performs when it serves each requested lane.
func (m *Manager) Precheck(ctx context.Context, opts PrecheckOptions) error {
	var errs []error
	if opts.GPG {
		errs = append(errs, checkHostGPG())
	}
	if opts.SSH {
		errs = append(errs, checkHostSSHAgent(ctx))
	}
	return errors.Join(errs...)
}

// checkHostGPG wraps ErrGPGUnavailable around the exact host lookups the
// bridge performs for the GPG lane: the pubkey export sent at startup and
// the gpg-agent extra socket dialed per connection.
func checkHostGPG() error {
	if _, err := getHostGPGPubkey(); err != nil {
		return fmt.Errorf("%w: %w", ErrGPGUnavailable, err)
	}
	if _, err := getGPGExtraSocket(); err != nil {
		return fmt.Errorf("%w: %w", ErrGPGUnavailable, err)
	}
	return nil
}

// checkHostSSHAgent wraps ErrSSHAgentUnavailable around the same agent
// socket resolution the daemon performs per connection, plus a dial to
// prove the agent answers.
func checkHostSSHAgent(ctx context.Context) error {
	path, err := resolveHostSocket(consts.SocketTypeSSHAgent)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSSHAgentUnavailable, err)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSSHAgentUnavailable, err)
	}
	conn.Close() //nolint:gosec // probe-only connection, nothing was written
	return nil
}

// bridgeExecutable returns the CLI binary used for a bridge daemon. The e2e
// harness supplies the built CLI because [os.Executable] is its Go test binary.
func bridgeExecutable() (string, error) {
	if executable := os.Getenv(consts.EnvExecutable); executable != "" {
		return executable, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get current executable: %w", err)
	}
	return executable, nil
}

// startBridge spawns a detached "clawker bridge serve" subprocess.
func (m *Manager) startBridge(opts EnsureBridgeOpts, pidFile, socketsFile string) error {
	containerID := opts.ContainerID
	exe, executableErr := bridgeExecutable()
	if executableErr != nil {
		return fmt.Errorf("failed to get executable path: %w", executableErr)
	}

	writeErr := WriteBridgedSocketsFile(socketsFile, opts.Sockets)
	if writeErr != nil {
		return fmt.Errorf("failed to write bridge registrations: %w", writeErr)
	}

	cmd := newBridgeCommand(exe, opts, pidFile, socketsFile)
	logFile := m.configureBridgeOutput(cmd)
	if startErr := cmd.Start(); startErr != nil {
		if logFile != nil {
			_ = logFile.Close() // The start error is the actionable error.
		}
		return fmt.Errorf("failed to start bridge daemon: %w", startErr)
	}

	return m.registerBridgeProcess(containerID, pidFile, socketsFile, cmd, logFile)
}

func newBridgeCommand(executable string, opts EnsureBridgeOpts, pidFile, socketsFile string) *exec.Cmd {
	args := []string{
		"bridge", "serve",
		"--container", opts.ContainerID,
		"--pid-file", pidFile,
		"--" + consts.BridgeSocketsFileFlag, socketsFile,
	}
	if opts.GPGEnabled {
		args = append(args, "--gpg")
	}

	cmd := exec.CommandContext(
		context.Background(),
		executable,
		args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Detach from parent session
	}
	cmd.Stdin = nil
	return cmd
}

func (m *Manager) configureBridgeOutput(cmd *exec.Cmd) *os.File {
	logFile, err := m.openBridgeLogFile()
	if err != nil {
		m.log.Debug().Err(err).Msg("failed to open bridge log file, output will be discarded")
		cmd.Stdout = nil
		cmd.Stderr = nil
	} else {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	return logFile
}

func (m *Manager) registerBridgeProcess(
	containerID string,
	pidFile string,
	socketsFile string,
	cmd *exec.Cmd,
	logFile *os.File,
) error {
	// Capture PID before Release — the Process handle may be invalid after Release.
	pid := cmd.Process.Pid

	// Close the log file in parent — child inherited the fd.
	if logFile != nil {
		if closeErr := logFile.Close(); closeErr != nil {
			m.log.Debug().Err(closeErr).Msg("failed to close parent bridge log file")
		}
	}

	// Release the child process so it can run independently
	if releaseErr := cmd.Process.Release(); releaseErr != nil {
		m.log.Debug().Err(releaseErr).Msg("failed to release bridge process (non-fatal)")
	}

	m.log.Debug().Str("container", ShortID(containerID)).Int("pid", pid).Msg("started bridge daemon")

	// Wait for PID file to appear (confirms bridge is initialized)
	if waitErr := waitForPIDFile(pidFile, bridgePIDFileTimeout); waitErr != nil {
		return fmt.Errorf("bridge started but PID file not created: %w", waitErr)
	}

	m.bridges[containerID] = &bridgeProcess{pid: pid, pidFile: pidFile, socketsFile: socketsFile}
	return nil
}

// cleanupBridgeLocked removes the bridge from tracking and kills the process.
// Must be called with m.mu held.
func (m *Manager) cleanupBridgeLocked(containerID string, bp *bridgeProcess) error {
	if isProcessAlive(bp.pid) {
		m.killProcess(bp.pid)
	}
	delete(m.bridges, containerID)
	if err := RemoveBridgeStateFiles(bp.pidFile, bp.socketsFile); err != nil {
		return fmt.Errorf("remove bridge state for %s: %w", ShortID(containerID), err)
	}
	return nil
}

// RemoveBridgeStateFiles removes the PID file and socket registration file.
func RemoveBridgeStateFiles(pidFile, socketsFile string) error {
	return errors.Join(
		removeBridgeStateFile("PID", pidFile),
		removeBridgeStateFile("socket registrations", socketsFile),
	)
}

// RemoveOwnedBridgeStateFiles removes state only when the PID file still belongs to the caller.
func RemoveOwnedBridgeStateFiles(pidFile, socketsFile string, ownerPID int) error {
	pid, err := readPIDFileValue(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read bridge PID file before cleanup: %w", err)
	}
	if pid != ownerPID {
		return nil
	}
	return RemoveBridgeStateFiles(pidFile, socketsFile)
}

func removeBridgeStateFile(name, path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove bridge %s file: %w", name, err)
	}
	return nil
}

// bridgeLogMaxBytes caps the shared bridge log; openBridgeLogFile rotates
// the file past this size before a new daemon is spawned. The Manager is
// the file's single rotation owner — daemons only append.
const bridgeLogMaxBytes = 10 << 20 // 10MB

// openBridgeLogFile opens the shared bridge daemon log file for appending,
// rotating it first when over cap. All bridge daemons and the Manager's
// child-output redirect append to the same file; see
// consts.SocketBridgeLogFile for why concurrent appenders are safe.
// LogsSubdir() ensures the directory exists via MkdirAll.
func (m *Manager) openBridgeLogFile() (*os.File, error) {
	logsDir, err := m.cfg.LogsSubdir()
	if err != nil {
		return nil, err
	}
	logPath := filepath.Join(logsDir, consts.SocketBridgeLogFile)
	logger.RotateAtCap(logPath, filepath.Join(logsDir, consts.SocketBridgeLogBackupFile), bridgeLogMaxBytes)
	f, err := logger.OpenAppend(logPath)
	if err != nil {
		return nil, fmt.Errorf("opening bridge log: %w", err)
	}
	return f, nil
}

// readPIDFile reads a PID from a file. Returns 0 if the file doesn't exist or is invalid.
func readPIDFile(path string) int {
	pid, err := readPIDFileValue(path)
	if err != nil {
		return 0
	}
	return pid
}

func readPIDFileValue(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read PID file %s: %w", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse PID file %s: %w", path, err)
	}
	return pid, nil
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
func (m *Manager) killProcess(pid int) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		m.log.Debug().Err(err).Int("pid", pid).Msg("failed to send SIGTERM to bridge")
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
