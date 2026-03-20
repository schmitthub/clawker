package firewall

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

const (
	defaultHealthCheckInterval = 5 * time.Second
	defaultPollInterval        = 30 * time.Second
	defaultGracePeriod         = 60 * time.Second
	maxHealthCheckFailures     = 3
	missedCheckThreshold       = 2 // exit after this many consecutive "no containers" polls
)

// Daemon manages the firewall stack as a background process.
// It starts the Envoy+CoreDNS containers, monitors their health,
// and auto-exits (tearing down the stack) when no clawker containers are running.
type Daemon struct {
	cfg                 config.Config
	log                 *logger.Logger
	manager             *Manager
	docker              client.APIClient
	pidFile             string
	healthCheckInterval time.Duration
	pollInterval        time.Duration
	gracePeriod         time.Duration
}

// DaemonOption is a functional option for overriding daemon config values.
type DaemonOption func(*Daemon)

// WithHealthCheckInterval overrides the healthcheck poll interval.
func WithHealthCheckInterval(d time.Duration) DaemonOption {
	return func(dm *Daemon) { dm.healthCheckInterval = d }
}

// WithDaemonPollInterval overrides the container watcher poll interval.
func WithDaemonPollInterval(d time.Duration) DaemonOption {
	return func(dm *Daemon) { dm.pollInterval = d }
}

// WithDaemonGracePeriod overrides the initial grace period before container watching.
func WithDaemonGracePeriod(d time.Duration) DaemonOption {
	return func(dm *Daemon) { dm.gracePeriod = d }
}

// NewDaemon creates a new firewall daemon.
func NewDaemon(cfg config.Config, log *logger.Logger, opts ...DaemonOption) (*Daemon, error) {
	pidFile, err := cfg.FirewallPIDFilePath()
	if err != nil {
		return nil, fmt.Errorf("resolving PID file path: %w", err)
	}

	// Raw moby client for container polling — the daemon needs an unfiltered
	// view of all Docker containers. docker.Client wraps whail which injects
	// managed-label filters, hiding containers from the count.
	dockerClient, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("connecting to Docker for container polling: %w", err)
	}

	fwMgr, err := NewManager(dockerClient, cfg, log)
	if err != nil {
		return nil, fmt.Errorf("creating firewall manager: %w", err)
	}

	d := &Daemon{
		cfg:                 cfg,
		log:                 log,
		manager:             fwMgr,
		docker:              dockerClient,
		pidFile:             pidFile,
		healthCheckInterval: defaultHealthCheckInterval,
		pollInterval:        defaultPollInterval,
		gracePeriod:         defaultGracePeriod,
	}

	for _, opt := range opts {
		opt(d)
	}

	return d, nil
}

// Run starts the firewall stack, then blocks until signal, healthcheck failure, or auto-exit.
func (d *Daemon) Run(ctx context.Context) error {
	d.log.Info().
		Int("pid", os.Getpid()).
		Str("pid_file", d.pidFile).
		Dur("health_interval", d.healthCheckInterval).
		Dur("poll_interval", d.pollInterval).
		Dur("grace_period", d.gracePeriod).
		Int("missed_threshold", missedCheckThreshold).
		Msg("firewall daemon starting")

	if err := writePIDFile(d.pidFile); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}
	defer removePIDFile(d.pidFile, d.log)

	// Start the firewall stack (pulls images, creates containers, waits for healthy).
	d.log.Debug().Msg("calling EnsureRunning to start firewall stack")
	if err := d.manager.EnsureRunning(ctx); err != nil {
		d.log.Error().Err(err).Msg("firewall stack failed to start, tearing down")
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if stopErr := d.manager.Stop(stopCtx); stopErr != nil {
			d.log.Warn().Err(stopErr).Msg("failed to clean up after startup failure")
		}
		return fmt.Errorf("firewall startup failed: %w", err)
	}

	d.log.Info().Msg("firewall daemon started, stack healthy")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// Healthcheck loop — 5s interval, exits after 3 consecutive failures.
	healthDone := make(chan struct{})
	go func() {
		defer close(healthDone)
		d.healthCheckLoop(runCtx)
	}()

	// Container watcher — 30s interval, exits when no clawker containers found.
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		d.watchContainers(runCtx)
	}()

	// Block until any exit condition.
	select {
	case sig := <-sigCh:
		d.log.Debug().Str("signal", sig.String()).Msg("received signal, shutting down firewall")
	case <-healthDone:
		d.log.Error().Msg("firewall healthcheck failed, shutting down")
	case <-watcherDone:
		d.log.Debug().Msg("no clawker containers running, shutting down firewall")
	case <-ctx.Done():
		d.log.Debug().Msg("context cancelled, shutting down firewall")
	}

	runCancel()

	// Stop firewall containers.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()

	if err := d.manager.Stop(stopCtx); err != nil {
		d.log.Warn().Err(err).Msg("error stopping firewall containers")
	}

	return nil
}

// healthCheckLoop probes envoy (TCP) and coredns (HTTP) via published localhost
// ports on a fixed interval. Returns after maxHealthCheckFailures consecutive failures.
func (d *Daemon) healthCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(d.healthCheckInterval)
	defer ticker.Stop()

	consecutiveFailures := 0
	envoyAddr := fmt.Sprintf("localhost:%d", d.cfg.EnvoyHealthHostPort())
	corednsURL := fmt.Sprintf("http://localhost:%d%s", d.cfg.CoreDNSHealthHostPort(), d.cfg.CoreDNSHealthPath())
	httpClient := &http.Client{Timeout: 2 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			healthy := d.probeHealth(envoyAddr, corednsURL, httpClient)
			if healthy {
				if consecutiveFailures > 0 {
					d.log.Debug().Msg("firewall healthcheck recovered")
				}
				consecutiveFailures = 0
				continue
			}

			consecutiveFailures++
			d.log.Warn().
				Int("consecutive_failures", consecutiveFailures).
				Int("max_failures", maxHealthCheckFailures).
				Msg("firewall healthcheck failed")

			if consecutiveFailures >= maxHealthCheckFailures {
				d.log.Error().Msg("firewall healthcheck exceeded max failures, daemon exiting")
				return
			}
		}
	}
}

// probeHealth checks envoy via TCP and coredns via HTTP.
// Returns true only if both are healthy.
func (d *Daemon) probeHealth(envoyAddr, corednsURL string, httpClient *http.Client) bool {
	// Envoy: TCP connect to TLS listener.
	conn, err := net.DialTimeout("tcp", envoyAddr, 2*time.Second)
	if err != nil {
		d.log.Debug().Err(err).Str("addr", envoyAddr).Msg("envoy health probe failed")
		return false
	}
	conn.Close()

	// CoreDNS: HTTP health endpoint.
	resp, err := httpClient.Get(corednsURL)
	if err != nil {
		d.log.Debug().Err(err).Str("url", corednsURL).Msg("coredns health probe failed")
		return false
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		d.log.Debug().Int("status", resp.StatusCode).Msg("coredns health probe unexpected status")
		return false
	}

	return true
}

// watchContainers polls Docker for clawker containers and exits when none are found
// after missedCheckThreshold consecutive empty checks.
func (d *Daemon) watchContainers(ctx context.Context) {
	d.log.Debug().Dur("grace_period", d.gracePeriod).Msg("container watcher: waiting for grace period")
	select {
	case <-ctx.Done():
		d.log.Debug().Msg("container watcher: context cancelled during grace period")
		return
	case <-time.After(d.gracePeriod):
	}
	d.log.Debug().Dur("poll_interval", d.pollInterval).Msg("container watcher: grace period elapsed, starting polling")

	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	missedChecks := 0
	pollCount := 0

	for {
		select {
		case <-ctx.Done():
			d.log.Debug().Msg("container watcher: context cancelled")
			return
		case <-ticker.C:
			pollCount++
			count, err := d.countClawkerContainers(ctx)
			if err != nil {
				d.log.Warn().Err(err).Int("poll", pollCount).Msg("container watcher: failed to count containers")
				continue
			}
			if count == 0 {
				missedChecks++
				d.log.Warn().
					Int("poll", pollCount).
					Int("missed", missedChecks).
					Int("threshold", missedCheckThreshold).
					Msg("container watcher: no clawker containers found")
				if missedChecks >= missedCheckThreshold {
					d.log.Warn().Int("missed", missedChecks).Msg("container watcher: threshold reached, daemon will exit")
					return
				}
			} else {
				if missedChecks > 0 {
					d.log.Debug().Int("count", count).Msg("container watcher: containers recovered after missed checks")
				}
				missedChecks = 0
				d.log.Debug().Int("poll", pollCount).Int("count", count).Msg("container watcher: containers alive")
			}
		}
	}
}

// countClawkerContainers returns the number of running agent containers.
// Filters directly on purpose=agent — every managed container now has an
// explicit purpose label ("agent", "monitoring", "firewall").
func (d *Daemon) countClawkerContainers(ctx context.Context) (int, error) {
	f := purposeFilter(d.cfg, d.cfg.PurposeAgent())

	d.log.Debug().
		Str("filter", fmt.Sprintf("managed=%s, purpose=%s", d.cfg.ManagedLabelValue(), d.cfg.PurposeAgent())).
		Msg("container count: querying Docker")

	result, err := d.docker.ContainerList(ctx, client.ContainerListOptions{
		Filters: f,
	})
	if err != nil {
		return 0, err
	}

	for _, ctr := range result.Items {
		names := ""
		if len(ctr.Names) > 0 {
			names = ctr.Names[0]
		}
		d.log.Debug().
			Str("id", ctr.ID[:12]).
			Str("name", names).
			Str("state", string(ctr.State)).
			Msg("container count: matched agent container")
	}

	return len(result.Items), nil
}

// --- EnsureDaemon: CLI entry point ---

// EnsureDaemon checks if the firewall daemon is running and spawns it if not.
// Returns immediately — does not wait for the daemon to become healthy.
func EnsureDaemon(cfg config.Config, log *logger.Logger) error {
	pidFile, err := cfg.FirewallPIDFilePath()
	if err != nil {
		return fmt.Errorf("resolving firewall PID file path: %w", err)
	}

	if IsDaemonRunning(pidFile) {
		log.Debug().Msg("firewall daemon already running")
		return nil
	}

	return startDaemonProcess(cfg, log)
}

// startDaemonProcess spawns `clawker firewall serve` as a detached subprocess.
func startDaemonProcess(cfg config.Config, log *logger.Logger) error {
	// CLAWKER_EXECUTABLE overrides os.Executable() for test environments
	// where the running binary is a Go test binary, not the clawker CLI.
	exe := os.Getenv("CLAWKER_EXECUTABLE")
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return fmt.Errorf("failed to get executable path: %w", err)
		}
	}

	cmd := exec.Command(exe, "firewall", "serve")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Detach from parent session.
	}
	cmd.Stdin = nil

	logFile, err := openDaemonLogFile(cfg)
	if err != nil {
		log.Debug().Err(err).Msg("failed to open firewall daemon log file, output will be discarded")
		cmd.Stdout = nil
		cmd.Stderr = nil
	} else {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		// logFile is intentionally not closed — the daemon subprocess inherits the fd.
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return fmt.Errorf("failed to start firewall daemon: %w", err)
	}

	// Release the child process so it runs independently.
	if err := cmd.Process.Release(); err != nil {
		log.Debug().Err(err).Msg("failed to release daemon process (non-fatal)")
	}

	log.Debug().Int("pid", cmd.Process.Pid).Msg("spawned firewall daemon")
	return nil
}

// openDaemonLogFile opens the daemon log file for writing.
func openDaemonLogFile(cfg config.Config) (*os.File, error) {
	logPath, err := cfg.FirewallLogFilePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get log file path: %w", err)
	}
	return os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

// --- PID file helpers ---

func writePIDFile(path string) error {
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func removePIDFile(path string, log *logger.Logger) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Warn().Err(err).Str("path", path).Msg("failed to remove PID file")
	}
}

func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(string(data))
}

func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// IsDaemonRunning checks if the firewall daemon is running via PID file.
func IsDaemonRunning(pidFile string) bool {
	pid, err := readPIDFile(pidFile)
	if err != nil {
		return false
	}
	return isProcessAlive(pid)
}

// GetDaemonPID returns the PID of the running firewall daemon, or 0 if not running.
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

// StopDaemon sends SIGTERM to the firewall daemon. No-op if not running.
func StopDaemon(pidFile string) error {
	pid, err := readPIDFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read PID file: %w", err)
	}

	if !isProcessAlive(pid) {
		if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove stale PID file: %w", err)
		}
		return nil
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", pid, err)
	}
	return process.Signal(syscall.SIGTERM)
}

// purposeFilter builds a Docker label filter for managed containers with a
// specific purpose (e.g. "agent", "firewall", "monitoring").
func purposeFilter(cfg config.Config, purpose string) client.Filters {
	f := client.Filters{}
	return f.
		Add("label", cfg.LabelManaged()+"="+cfg.ManagedLabelValue()).
		Add("label", cfg.LabelPurpose()+"="+purpose)
}
