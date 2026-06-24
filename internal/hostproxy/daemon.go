package hostproxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
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
	log                *logger.Logger
	server             *Server
	docker             ContainerLister
	pidFile            string
	pollInterval       time.Duration
	gracePeriod        time.Duration
	maxConsecutiveErrs int

	// Staged startup-readiness gate: probes and per-stage wait budgets, all
	// populated by NewDaemon (probes → the real implementations, budgets → the
	// consts.HostProxy* timeouts). Tests construct the Daemon directly and set
	// these to drive each stage deterministically and keep waits fast.
	firewallRunningProbe   func(ctx context.Context) bool
	envoyHealthProbe       func(ctx context.Context) bool
	firewallRunningTimeout time.Duration
	envoyHealthTimeout     time.Duration
	rulesReadTimeout       time.Duration
	readyInterval          time.Duration
}

// DaemonOption is a functional option for overriding daemon config values.
// CLI flags use these to take precedence over config without mutating the config object.
type DaemonOption func(*Daemon)

// WithDaemonPort overrides the daemon listen port.
func WithDaemonPort(port int) DaemonOption {
	return func(d *Daemon) {
		d.server = NewServer(port, d.log, d.server.rulesFilePath)
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
func NewDaemon(cfg config.Config, log *logger.Logger, opts ...DaemonOption) (*Daemon, error) {
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

	// Resolve egress rules file path for URL egress enforcement only when the
	// global firewall is enabled. When disabled, keep rulesFilePath empty so
	// /open/url preserves its documented "skip check" behavior.
	var rulesFilePath string
	if cfg.Settings().Firewall.FirewallEnabled() {
		dataDir, err := cfg.FirewallDataSubdir()
		if err != nil {
			// Firewall is enabled, so /open/url egress enforcement is mandatory.
			// If the rules path can't be resolved we cannot enforce — fail closed
			// by refusing to construct the daemon (which fails container startup)
			// rather than silently running /open/url unchecked.
			return nil, fmt.Errorf("firewall enabled but cannot resolve egress rules path: %w", err)
		}
		rulesFilePath = filepath.Join(dataDir, cfg.EgressRulesFileName())
	}

	dockerClient, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	d := &Daemon{
		cfg:                cfg,
		log:                log,
		server:             NewServer(daemonCfg.Port, log, rulesFilePath),
		docker:             dockerClient,
		pidFile:            pidFile,
		pollInterval:       daemonCfg.PollInterval,
		gracePeriod:        daemonCfg.GracePeriod,
		maxConsecutiveErrs: daemonCfg.MaxConsecutiveErrs,

		// Readiness-gate probes default to the real implementations; the
		// per-stage budgets to the firewall-derived consts. Tests override these
		// by constructing the Daemon directly.
		firewallRunningTimeout: consts.HostProxyFirewallRunningTimeout,
		envoyHealthTimeout:     consts.HostProxyEnvoyHealthTimeout,
		rulesReadTimeout:       consts.HostProxyRulesReadTimeout,
		readyInterval:          consts.HostProxyReadyPollInterval,
	}
	d.firewallRunningProbe = d.firewallContainerRunning
	d.envoyHealthProbe = d.envoyHealthy

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
	defer removePIDFile(d.pidFile, d.log)

	// Start the server FIRST so /health answers immediately. In container
	// bootstrap the CP and firewall stack boot BEFORE the host proxy, and the
	// manager's host-proxy ensure gates on this /health — binding immediately
	// lets that proceed while the egress-rules readiness gate (which waits on
	// the already-running firewall + Envoy, then validates the rules file)
	// converges in the background. If the rules never become valid the gate
	// fails the daemon closed: Run returns and the daemon exits. Egress
	// enforcement on /open/url is fail-closed per request regardless —
	// handleOpenURL rejects an unreadable/invalid rules file — so binding
	// before the rules are confirmed never opens a hole.
	if err := d.server.Start(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Derived context cancelled on every exit path, stopping the background
	// goroutine.
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// Background lifecycle: run the staged egress-rules readiness gate first,
	// then — only once the rules are confirmed usable — start the container
	// watcher. Sequencing the watcher behind the gate keeps the watcher's
	// clean auto-exit from preempting (and silently masking) a readiness
	// failure. The gate must not block the /health bind above, so it runs here
	// off the main goroutine; until it passes, request-time /open/url
	// enforcement already fails closed.
	watcherDone := make(chan struct{})
	readyErrCh := make(chan error, 1)
	go func() {
		if err := d.ensureEgressRulesReady(runCtx); err != nil {
			// A cancelled context means we're already shutting down (signal or
			// parent ctx), not a readiness failure — don't raise a false alarm.
			if runCtx.Err() == nil {
				readyErrCh <- err
			}
			return
		}
		d.watchContainers(runCtx)
		close(watcherDone)
	}()

	// Wait for a signal, watcher exit, or a readiness failure
	var runErr error
	select {
	case sig := <-sigCh:
		d.log.Debug().Str("signal", sig.String()).Msg("received signal, shutting down")
	case <-watcherDone:
		d.log.Debug().Msg("container watcher exited, shutting down")
	case err := <-readyErrCh:
		d.log.Error().Err(err).Str("rules_file", d.server.rulesFilePath).
			Msg("host proxy egress rules never became ready; shutting down")
		runErr = fmt.Errorf("host proxy egress rules not ready: %w", err)
	case <-ctx.Done():
		d.log.Debug().Msg("context cancelled, shutting down")
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := d.server.Stop(shutdownCtx); err != nil {
		d.log.Warn().Err(err).Msg("error during server shutdown")
	}

	if d.docker != nil {
		d.docker.Close()
	}

	return runErr
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
				d.log.Warn().Err(err).Int("consecutive_errors", consecutiveErrs).Msg("failed to count containers")
				if consecutiveErrs >= d.maxConsecutiveErrs {
					d.log.Error().Int("threshold", d.maxConsecutiveErrs).Msg("too many consecutive Docker API errors, initiating shutdown")
					return
				}
				continue
			}
			// Reset error counter on successful API call
			consecutiveErrs = 0
			d.log.Debug().Int("count", count).Msg("checked clawker containers")
			if count == 0 {
				d.log.Debug().Msg("no clawker containers running, initiating shutdown")
				return
			}
		}
	}
}

// countClawkerContainers returns the number of running agent containers.
// Filters directly on purpose=agent — every managed container has an
// explicit purpose label ("agent", "monitoring", "firewall").
func (d *Daemon) countClawkerContainers(ctx context.Context) (int, error) {
	f := client.Filters{}
	f = f.
		Add("label", d.cfg.LabelManaged()+"="+d.cfg.ManagedLabelValue()).
		Add("label", d.cfg.LabelPurpose()+"="+d.cfg.PurposeAgent())

	result, err := d.docker.ContainerList(ctx, client.ContainerListOptions{
		Filters: f,
	})
	if err != nil {
		return 0, err
	}
	return len(result.Items), nil
}

// ensureEgressRulesReady runs the staged startup-readiness gate: it waits, in
// sequence, for (1) the firewall container to be running, (2) Envoy's health
// endpoint to answer, and (3) the egress rules file to be readable and valid.
// The stages are separate loops so a later stage never probes before its
// prerequisite is up (e.g. never reads the rules file before the firewall that
// writes it is healthy). Each stage's budget comes from internal/consts (the
// firewall's own bringup/health timeouts) so the host proxy never gives up
// before the firewall could be up. On exhaustion it returns an error so the
// caller can shut the daemon down — request-time /open/url enforcement is
// fail-closed regardless. No-op when the firewall is disabled (empty rules
// path → /open/url enforcement is intentionally skipped).
func (d *Daemon) ensureEgressRulesReady(ctx context.Context) error {
	path := d.server.rulesFilePath
	if path == "" {
		return nil
	}

	// Stage 1: firewall container running. Don't probe Envoy or read the rules
	// file until the container that serves them exists.
	if err := d.waitForCondition(ctx, d.firewallRunningTimeout, d.readyInterval,
		"firewall container to start", d.firewallRunningProbe); err != nil {
		return err
	}

	// Stage 2: Envoy health endpoint answering. Don't read the rules file until
	// the proxy that enforces them is healthy.
	if err := d.waitForCondition(ctx, d.envoyHealthTimeout, d.readyInterval,
		"Envoy health endpoint", d.envoyHealthProbe); err != nil {
		return err
	}

	// Stage 3: egress rules file present and valid. Re-read each tick so an
	// in-flight atomic write (temp+rename) can settle; a still-invalid file
	// when the budget elapses is surfaced loud via errEgressRulesInvalid.
	var lastErr error
	if err := d.waitForCondition(ctx, d.rulesReadTimeout, d.readyInterval,
		"egress rules file to be readable",
		func(context.Context) bool {
			lastErr = validateEgressRulesFile(path)
			return lastErr == nil
		}); err != nil {
		if lastErr != nil {
			return fmt.Errorf("%w: %w", err, lastErr)
		}
		return err
	}
	return nil
}

// waitForCondition polls cond until it returns true or budget elapses, sleeping
// interval between attempts and logging a Warn each attempt so a slow firewall
// bringup is visible. Returns an error naming what it waited for, or ctx.Err()
// if the context is cancelled mid-wait.
func (d *Daemon) waitForCondition(ctx context.Context, budget, interval time.Duration, what string, cond func(context.Context) bool) error {
	deadline := time.Now().Add(budget)
	for attempt := 1; ; attempt++ {
		if cond(ctx) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out after %s waiting for %s", budget, what)
		}
		d.log.Warn().
			Int("attempt", attempt).
			Dur("budget", budget).
			Msgf("host proxy waiting for %s", what)
		if !sleepCtx(ctx, interval) {
			return ctx.Err()
		}
	}
}

// firewallContainerRunning reports whether a running container carries the
// firewall purpose label. Mirrors countClawkerContainers' filter but keys on
// PurposeFirewall; ContainerList returns running containers by default.
func (d *Daemon) firewallContainerRunning(ctx context.Context) bool {
	if d.docker == nil {
		return false
	}
	f := client.Filters{}.
		Add("label", d.cfg.LabelManaged()+"="+d.cfg.ManagedLabelValue()).
		Add("label", d.cfg.LabelPurpose()+"="+d.cfg.PurposeFirewall())
	result, err := d.docker.ContainerList(ctx, client.ContainerListOptions{Filters: f})
	if err != nil {
		d.log.Warn().Err(err).Msg("failed to list firewall containers while waiting for readiness")
		return false
	}
	return len(result.Items) > 0
}

// envoyHealthy reports whether Envoy's host-published health listener (a 200-OK
// HTTP endpoint) is answering — the firewall stack's readiness signal. The leaf
// host proxy reaches it by port (from config) without importing the firewall
// package.
func (d *Daemon) envoyHealthy(ctx context.Context) bool {
	url := fmt.Sprintf("http://127.0.0.1:%d/", d.cfg.EnvoyHealthHostPort())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// sleepCtx waits for d or ctx cancellation; returns false if ctx was cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
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
// log may be nil, in which case removal errors are silently ignored.
func removePIDFile(path string, log *logger.Logger) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		if log != nil {
			log.Warn().Err(err).Str("path", path).Msg("failed to remove PID file")
		}
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
		removePIDFile(pidFile, nil)
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
