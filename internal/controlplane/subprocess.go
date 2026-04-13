package controlplane

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

// HealthCheck defines how to check if a subprocess is healthy.
type HealthCheck struct {
	URL      string        // e.g. "https://127.0.0.1:4444/health/alive"
	Interval time.Duration // polling interval
	Timeout  time.Duration // overall timeout before giving up
	TLS      *tls.Config   // optional TLS config for HTTPS health endpoints
}

// ManagedSubprocess tracks a running subprocess.
type ManagedSubprocess struct {
	Name    string
	Cmd     *exec.Cmd
	done    chan struct{} // closed when cmd.Wait() returns
	waitErr error         // set before done is closed
}

// SubprocessManager starts, monitors, and shuts down a set of subprocesses.
// Fail-fast: if any subprocess exits unexpectedly, the crash is reported via
// CrashChan. Once Shutdown begins, subsequent subprocess exits are expected
// and are NOT reported as crashes.
// Shutdown is reverse order of start.
type SubprocessManager struct {
	log          *logger.Logger
	mu           sync.Mutex
	processes    []*ManagedSubprocess
	crashed      chan error
	shuttingDown atomic.Bool
}

// NewSubprocessManager creates a new subprocess manager.
func NewSubprocessManager(log *logger.Logger) *SubprocessManager {
	if log == nil {
		log = logger.Nop()
	}
	return &SubprocessManager{
		log: log,
		// crashed has buffer size 1: only the first subprocess crash is
		// reported. This is intentional — the CP exits on first crash, so
		// only the triggering error matters. Subsequent crashes during
		// shutdown are expected and silently dropped.
		crashed: make(chan error, 1),
	}
}

// Start launches a subprocess and begins monitoring its PID.
// Stdout/stderr are forwarded to the CP's stderr (visible in docker logs).
func (m *SubprocessManager) Start(name string, cmd *exec.Cmd) error {
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}

	proc := &ManagedSubprocess{
		Name: name,
		Cmd:  cmd,
		done: make(chan struct{}),
	}

	// Monitor the subprocess in a goroutine.
	go func() {
		proc.waitErr = cmd.Wait()
		close(proc.done)
		// Only report unexpected exits. Once Shutdown() has been called,
		// subprocess exits are expected and must not be misclassified as crashes.
		if m.shuttingDown.Load() {
			m.log.Info().Str("subprocess", name).Msg("exited during shutdown (expected)")
			return
		}
		// Report crash to the manager — first one wins.
		select {
		case m.crashed <- fmt.Errorf("subprocess %s exited unexpectedly: %v", name, proc.waitErr):
		default:
		}
	}()

	m.mu.Lock()
	m.processes = append(m.processes, proc)
	m.mu.Unlock()

	m.log.Info().Str("subprocess", name).Int("pid", cmd.Process.Pid).Msg("started")
	return nil
}

// WaitHealthy polls a health endpoint until it returns 200 or the timeout
// expires. Returns an error if the subprocess crashes before becoming healthy.
func (m *SubprocessManager) WaitHealthy(ctx context.Context, name string, check HealthCheck) error {
	ctx, cancel := context.WithTimeout(ctx, check.Timeout)
	defer cancel()

	var transport *http.Transport
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = dt.Clone()
	} else {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		}
	}
	if check.TLS != nil {
		transport.TLSClientConfig = check.TLS
		transport.ForceAttemptHTTP2 = true
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}

	proc := m.findProcess(name)
	if proc == nil {
		return fmt.Errorf("subprocess %s not found", name)
	}

	ticker := time.NewTicker(check.Interval)
	defer ticker.Stop()

	for {
		// Check if the subprocess crashed.
		select {
		case <-proc.done:
			return fmt.Errorf("subprocess %s crashed before becoming healthy: %v", name, proc.waitErr)
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, check.URL, nil)
		if err != nil {
			return fmt.Errorf("build health request for %s: %w", name, err)
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				m.log.Info().Str("subprocess", name).Msg("healthy")
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("subprocess %s did not become healthy within %s", name, check.Timeout)
		case <-ticker.C:
		}
	}
}

// CrashChan returns a channel that receives an error when any managed
// subprocess exits unexpectedly. The CP main loop selects on this
// alongside signal handling.
func (m *SubprocessManager) CrashChan() <-chan error {
	return m.crashed
}

// BeginShutdown marks the manager as shutting down, suppressing crash
// reporting for any subprocess that exits from this point forward.
// Call this as early as possible in the shutdown sequence — before
// stopping servers or other components that may cause cascading
// subprocess exits. Shutdown() also sets this flag, but calling
// BeginShutdown early closes the window between signal receipt
// and Shutdown() invocation.
func (m *SubprocessManager) BeginShutdown() {
	m.shuttingDown.Store(true)
}

// Shutdown sends SIGTERM to all subprocesses in reverse start order,
// waits up to timeout for each to exit, then sends SIGKILL if needed.
// Sets the shuttingDown flag if BeginShutdown was not already called.
func (m *SubprocessManager) Shutdown(timeout time.Duration) {
	m.shuttingDown.Store(true)

	m.mu.Lock()
	procs := make([]*ManagedSubprocess, len(m.processes))
	copy(procs, m.processes)
	m.mu.Unlock()

	// Reverse order.
	for i := len(procs) - 1; i >= 0; i-- {
		p := procs[i]
		if p.Cmd.Process == nil {
			continue
		}

		m.log.Info().Str("subprocess", p.Name).Msg("sending SIGTERM")
		_ = p.Cmd.Process.Signal(syscall.SIGTERM)

		select {
		case <-p.done:
			m.log.Info().Str("subprocess", p.Name).Msg("exited cleanly")
		case <-time.After(timeout):
			m.log.Warn().Str("subprocess", p.Name).Msg("SIGTERM timeout, sending SIGKILL")
			_ = p.Cmd.Process.Kill()
			<-p.done
		}
	}
}

// ForwardSignal sends the given signal to all managed subprocesses.
func (m *SubprocessManager) ForwardSignal(sig os.Signal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.processes {
		if p.Cmd.Process != nil {
			_ = p.Cmd.Process.Signal(sig)
		}
	}
}

func (m *SubprocessManager) findProcess(name string) *ManagedSubprocess {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.processes {
		if p.Name == name {
			return p
		}
	}
	return nil
}
