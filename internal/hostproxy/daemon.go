package hostproxy

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

// ContainerLister is the minimal interface needed for container watcher functionality.
// This allows the daemon to work without directly coupling to the full Docker client,
// and enables testing with mock implementations.
//
// Note: This package imports github.com/moby/moby/client directly rather than going
// through pkg/whail. This is intentional because the daemon runs as a standalone
// subprocess that only needs to list containers - it doesn't need whail's jail
// semantics or label enforcement. The interface pattern still allows for testing.
type ContainerLister interface {
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	io.Closer
}

// Daemon manages the host proxy server as a background process.
// It polls Docker for clawker containers and auto-exits when none are running.
type Daemon struct {
	cfg                config.Config
	server             *Server
	docker             ContainerLister
	pidFile            string
	pollInterval       time.Duration
	gracePeriod        time.Duration
	maxConsecutiveErrs int
}

// DaemonOption is a functional option for overriding daemon config values.
// CLI flags use these to take precedence over config without mutating the config object.
type DaemonOption func(*Daemon)

// WithDaemonPort overrides the daemon listen port.
func WithDaemonPort(port int) DaemonOption {
	return func(d *Daemon) {
		d.server = NewServer(port)
	}
}

// WithPollInterval overrides the container poll interval.
func WithPollInterval(interval time.Duration) DaemonOption {
	return func(d *Daemon) {
		d.pollInterval = interval
	}
}

// WithGracePeriod overrides the initial grace period.
func WithGracePeriod(period time.Duration) DaemonOption {
	return func(d *Daemon) {
		d.gracePeriod = period
	}
}

// NewDaemon creates a new daemon that reads all settings from cfg.HostProxyConfig().
// Optional DaemonOption values override individual config settings (used by CLI flags).
// It creates a Docker client internally. Tests that need a mock docker client
// construct &Daemon{...} directly (the pattern used by watchContainers tests).
func NewDaemon(cfg config.Config, opts ...DaemonOption) (*Daemon, error) {
	daemonCfg := cfg.HostProxyConfig().Daemon

	if err := validatePort(daemonCfg.Port, "host proxy daemon"); err != nil {
		return nil, err
	}
	if daemonCfg.PollInterval <= 0 {
		return nil, fmt.Errorf("invalid poll interval %v: must be positive", daemonCfg.PollInterval)
	}
	if daemonCfg.GracePeriod < 0 {
		return nil, fmt.Errorf("invalid grace period %v: must be non-negative", daemonCfg.GracePeriod)
	}
	if daemonCfg.MaxConsecutiveErrs <= 0 {
		return nil, fmt.Errorf("invalid max consecutive errors %d: must be positive", daemonCfg.MaxConsecutiveErrs)
	}

	pidFile, err := cfg.HostProxyPIDFilePath()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve host proxy PID file path: %w", err)
	}

	dockerClient, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	d := &Daemon{
		cfg:                cfg,
		server:             NewServer(daemonCfg.Port),
		docker:             dockerClient,
		pidFile:            pidFile,
		pollInterval:       daemonCfg.PollInterval,
		gracePeriod:        daemonCfg.GracePeriod,
		maxConsecutiveErrs: daemonCfg.MaxConsecutiveErrs,
	}

	for _, opt := range opts {
		opt(d)
	}

	return d, nil
}

// Run starts the daemon and blocks until it receives a signal or auto-exits.
func (d *Daemon) Run(ctx context.Context) error {
	// Write PID file
	if err := writePIDFile(d.pidFile); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}
	defer removePIDFile(d.pidFile)

	// Start the server
	if err := d.server.Start(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Start container watcher
	watcherDone := make(chan struct{})
	watcherCtx, watcherCancel := context.WithCancel(ctx)
	defer watcherCancel() // Ensure context is cancelled on all exit paths

	go func() {
		d.watchContainers(watcherCtx)
		close(watcherDone)
	}()

	// Wait for signal or watcher exit
	select {
	case sig := <-sigCh:
		logger.Debug().Str("signal", sig.String()).Msg("received signal, shutting down")
	case <-watcherDone:
		logger.Debug().Msg("container watcher exited, shutting down")
	case <-ctx.Done():
		logger.Debug().Msg("context cancelled, shutting down")
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := d.server.Stop(shutdownCtx); err != nil {
		logger.Warn().Err(err).Msg("error during server shutdown")
	}

	if d.docker != nil {
		d.docker.Close()
	}

	return nil
}

// watchContainers polls Docker for clawker containers and exits when none are found.
// It also exits if Docker API errors exceed the configured threshold.
func (d *Daemon) watchContainers(ctx context.Context) {
	// Initial grace period before first check
	select {
	case <-ctx.Done():
		return
	case <-time.After(d.gracePeriod):
	}

	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	consecutiveErrs := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count, err := d.countClawkerContainers(ctx)
			if err != nil {
				consecutiveErrs++
				logger.Warn().Err(err).Int("consecutive_errors", consecutiveErrs).Msg("failed to count containers")
				if consecutiveErrs >= d.maxConsecutiveErrs {
					logger.Error().Int("threshold", d.maxConsecutiveErrs).Msg("too many consecutive Docker API errors, initiating shutdown")
					return
				}
				continue
			}
			// Reset error counter on successful API call
			consecutiveErrs = 0
			logger.Debug().Int("count", count).Msg("checked clawker containers")
			if count == 0 {
				logger.Debug().Msg("no clawker containers running, initiating shutdown")
				return
			}
		}
	}
}

// countClawkerContainers returns the number of running clawker containers.
func (d *Daemon) countClawkerContainers(ctx context.Context) (int, error) {
	labelManaged := d.cfg.LabelManaged()
	managedValue := d.cfg.ManagedLabelValue()
	labelMonStack := d.cfg.LabelMonitoringStack()

	f := client.Filters{}
	f = f.Add("label", labelManaged+"="+managedValue).Add("label", labelMonStack+"!="+managedValue)

	result, err := d.docker.ContainerList(ctx, client.ContainerListOptions{
		Filters: f,
	})
	if err != nil {
		return 0, err
	}
	return len(result.Items), nil
}

// PID file operations

// writePIDFile writes the current process ID to the specified file.
func writePIDFile(path string) error {
	pid := os.Getpid()
	content := strconv.Itoa(pid)
	return os.WriteFile(path, []byte(content), 0644)
}

// readPIDFile reads the process ID from the specified file.
func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(string(data))
}

// removePIDFile removes the PID file.
func removePIDFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		logger.Warn().Err(err).Str("path", path).Msg("failed to remove PID file")
	}
}

// isProcessAlive checks if a process with the given PID is running.
// On Unix, this uses kill -0 which checks process existence without sending a signal.
func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, sending signal 0 checks if the process exists
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// IsDaemonRunning checks if a daemon is running by checking the PID file
// and verifying the process is alive.
func IsDaemonRunning(pidFile string) bool {
	pid, err := readPIDFile(pidFile)
	if err != nil {
		return false
	}
	return isProcessAlive(pid)
}

// GetDaemonPID returns the PID of the running daemon, or 0 if not running.
func GetDaemonPID(pidFile string) int {
	pid, err := readPIDFile(pidFile)
	if err != nil {
		return 0
	}
	if !isProcessAlive(pid) {
		return 0
	}
	return pid
}

// StopDaemon sends SIGTERM to the daemon process.
func StopDaemon(pidFile string) error {
	pid, err := readPIDFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Not running
		}
		return fmt.Errorf("failed to read PID file: %w", err)
	}

	if !isProcessAlive(pid) {
		// Process not running, clean up stale PID file
		removePIDFile(pidFile)
		return nil
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}

	return nil
}
