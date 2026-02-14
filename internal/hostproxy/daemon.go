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
	server             *Server
	docker             ContainerLister
	pidFile            string
	pollInterval       time.Duration
	gracePeriod        time.Duration
	maxConsecutiveErrs int
}

// DaemonOptions configures the daemon behavior.
type DaemonOptions struct {
	Port               int
	PIDFile            string
	PollInterval       time.Duration
	GracePeriod        time.Duration
	MaxConsecutiveErrs int
	// DockerClient allows injecting a custom container lister for testing.
	// If nil, a default Docker client will be created.
	DockerClient ContainerLister
}

// DefaultDaemonOptions returns the default daemon configuration.
func DefaultDaemonOptions() DaemonOptions {
	pidFile, err := config.HostProxyPIDFile()
	if err != nil {
		logger.Warn().Err(err).Msg("failed to get host proxy PID file path, daemon tracking disabled")
	}
	return DaemonOptions{
		Port:               DefaultPort,
		PIDFile:            pidFile,
		PollInterval:       30 * time.Second,
		GracePeriod:        60 * time.Second,
		MaxConsecutiveErrs: 10,
	}
}

// NewDaemon creates a new daemon with the given options.
func NewDaemon(opts DaemonOptions) (*Daemon, error) {
	dockerClient := opts.DockerClient
	if dockerClient == nil {
		var err error
		dockerClient, err = client.New(
			client.FromEnv,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create docker client: %w", err)
		}
	}

	maxErrs := opts.MaxConsecutiveErrs
	if maxErrs <= 0 {
		maxErrs = 10 // Default to 10 consecutive errors before exit
	}

	return &Daemon{
		server:             NewServer(opts.Port),
		docker:             dockerClient,
		pidFile:            opts.PIDFile,
		pollInterval:       opts.PollInterval,
		gracePeriod:        opts.GracePeriod,
		maxConsecutiveErrs: maxErrs,
	}, nil
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
		logger.Info().Str("signal", sig.String()).Msg("received signal, shutting down")
	case <-watcherDone:
		logger.Info().Msg("container watcher exited, shutting down")
	case <-ctx.Done():
		logger.Info().Msg("context cancelled, shutting down")
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
				logger.Info().Msg("no clawker containers running, initiating shutdown")
				return
			}
		}
	}
}

// countClawkerContainers returns the number of running clawker containers.
func (d *Daemon) countClawkerContainers(ctx context.Context) (int, error) {
	// Use the moby client Filters type
	f := client.Filters{}
	f = f.Add("label", config.LabelManaged+"="+config.ManagedLabelValue).Add("label", config.LabelMonitoringStack+"!="+config.ManagedLabelValue)

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
